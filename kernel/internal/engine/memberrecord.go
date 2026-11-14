package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Member records (contract 0056 §2, §5): when the registry has a model `user` with a field `account_id`, every
// membership in rk_members has a `user` record the kernel keeps in step with it. The kernel owns email, role,
// outlets, access and account_id (read_only in the model); these writes run as the system actor, so the read_only
// guard does not drop them. The membership is written first, in its own transaction; the sync runs after it and is
// idempotent, and RepairMemberRecords catches up whatever a failed or skipped sync left behind.

const memberRecordModel = "user"

// Values of user.access.
const (
	accessActive  = "active"
	accessPending = "pending"
	accessRemoved = "removed"
)

// memberRecord returns the member record model of reg, nil when it has none.
func memberRecord(reg *meta.Registry) *meta.Model {
	m := reg.Model(memberRecordModel)
	if m == nil || m.Field("account_id") == nil {
		return nil
	}
	return m
}

// HasMemberRecord reports whether the registry has the member record model (`user` with `account_id`).
func (e *Engine) HasMemberRecord() bool { return memberRecord(e.Reg()) != nil }

// SyncMemberRecord brings the `user` record of account userID in org in step with its membership and returns the
// record's name ("" when there is no member record model, or the account was never a member and has no record).
func (e *Engine) SyncMemberRecord(ctx context.Context, org string, userID int64) (record string, err error) {
	record, _, err = e.syncMemberRecord(ctx, org, userID)
	return record, err
}

// RepairMemberRecords syncs every member of org, and every record that still points at an account that is no longer
// a member, and returns how many it had to change. A second run right after changes nothing.
func (e *Engine) RepairMemberRecords(ctx context.Context, org string) (synced int, err error) {
	m := memberRecord(e.Reg())
	if m == nil {
		return 0, nil
	}
	ctx = pg.WithOrg(ctx, org)
	res, err := e.DB.Query(ctx, fmt.Sprintf(`select user_id from rk_members where org_id = $1
		union select account_id::bigint from %s where org_id = $1 and coalesce(account_id, 0) <> 0
		order by 1`, pg.QuoteIdent(store.TableName(m.Name))), org)
	if err != nil {
		return 0, err
	}
	var errs []string
	for _, row := range res.Rows {
		id := toInt(row[0])
		_, changed, err := e.syncMemberRecord(ctx, org, id)
		if err != nil {
			errs = append(errs, fmt.Sprintf("account %d: %v", id, err))
			continue
		}
		if changed {
			synced++
		}
	}
	if len(errs) > 0 {
		return synced, fmt.Errorf("member records of %s: %s", org, strings.Join(errs, "; "))
	}
	return synced, nil
}

// syncMemberRecord is SyncMemberRecord; changed reports a write (to the record or to rk_members.record).
func (e *Engine) syncMemberRecord(ctx context.Context, org string, userID int64) (record string, changed bool, err error) {
	if !e.HasMemberRecord() {
		return "", false, nil
	}
	ctx = pg.WithOrg(ctx, org)
	sys := Actor{Org: org, ID: "system", System: true}
	err = e.run(ctx, sys, func(o *op) error {
		record, changed = "", false
		m := memberRecord(o.registry())
		if m == nil {
			return nil
		}
		// one sync per account and org at a time: a member write and the repair loop must not both create a record
		if _, err := o.tx.Query(o.ctx, "select pg_advisory_xact_lock(hashtext('rk_member_record'), hashtext($1 || ':' || $2::text))", org, userID); err != nil {
			return err
		}
		res, err := o.tx.Query(o.ctx, "select email, full_name, signed_in_at is not null from rk_users where id = $1", userID)
		if err != nil {
			return err
		}
		if len(res.Rows) == 0 {
			return nil // the account is gone; a record pointing at it keeps what it has
		}
		email, fullName, signedIn := res.Rows[0][0].(string), res.Rows[0][1].(string), res.Rows[0][2].(bool)
		res, err = o.tx.Query(o.ctx, "select role, outlets, coalesce(record, '') from rk_members where org_id = $1 and user_id = $2", org, userID)
		if err != nil {
			return err
		}
		member, role, outlets, linked := len(res.Rows) > 0, "", []string{}, ""
		if member {
			role, outlets, linked = res.Rows[0][0].(string), textList(res.Rows[0][1]), res.Rows[0][2].(string)
		}

		name, err := o.findMemberRecord(m, linked, userID, email)
		if err != nil {
			return err
		}
		if name == "" && !member {
			return nil // never a member here with a record: nothing to mark removed
		}

		want := map[string]any{"account_id": userID, "email": email}
		switch {
		case member && signedIn:
			want["access"], want["role"] = accessActive, role
		case member:
			want["access"], want["role"] = accessPending, role
		default:
			want["access"], outlets = accessRemoved, []string{}
		}
		if m.Field("outlets") != nil {
			want["outlets"] = outletRows(outlets)
		}

		if name == "" {
			if fullName == "" {
				fullName = email
			}
			want["full_name"] = fullName
			res, err := o.create(m.Name, want, CreateOpts{})
			if err != nil {
				return err
			}
			name, changed = str(res.Record["name"]), true
		} else {
			cur, err := o.load(m, name)
			if err != nil {
				return err
			}
			patch := memberRecordPatch(m, cur, want)
			if str(cur["full_name"]) == "" {
				if fullName == "" {
					fullName = email
				}
				patch["full_name"] = fullName
			}
			if len(patch) > 0 {
				if _, err := o.update(m.Name, name, patch, false, false); err != nil {
					return err
				}
				changed = true
			}
		}
		if member && linked != name {
			if _, err := o.tx.Query(o.ctx, "update rk_members set record = $3 where org_id = $1 and user_id = $2", org, userID, name); err != nil {
				return err
			}
			changed = true
		}
		record = name
		return nil
	})
	return record, changed, err
}

// findMemberRecord looks the account's record up in the order of contract 0056 §5: the membership's record, else a
// record with this account_id, else the one record with no account_id whose e-mail is the account's (two or more:
// none is taken). An int field is filled with 0 when unset, so 0 counts as no account. "" means a new record is needed.
func (o *op) findMemberRecord(m *meta.Model, linked string, userID int64, email string) (string, error) {
	table := pg.QuoteIdent(store.TableName(m.Name))
	if linked != "" {
		// a linked record that carries another account's account_id is not this account's (bad data from before
		// record_taken was enforced): adopting it would flip account_id back and forth on every repair
		res, err := o.tx.Query(o.ctx, "select coalesce(account_id, 0) from "+table+" where org_id = $1 and name = $2", o.actor.Org, linked)
		if err != nil {
			return "", err
		}
		if len(res.Rows) > 0 {
			if acc := toInt(res.Rows[0][0]); acc == 0 || acc == userID {
				return linked, nil
			}
		}
	}
	res, err := o.tx.Query(o.ctx, "select name from "+table+" where org_id = $1 and account_id = $2 order by created_at, name limit 1", o.actor.Org, userID)
	if err != nil {
		return "", err
	}
	if len(res.Rows) > 0 {
		return res.Rows[0][0].(string), nil
	}
	if m.Field("email") == nil {
		return "", nil
	}
	res, err = o.tx.Query(o.ctx, "select name from "+table+" where org_id = $1 and coalesce(account_id, 0) = 0 and lower(email) = lower($2) limit 2", o.actor.Org, email)
	if err != nil {
		return "", err
	}
	if len(res.Rows) == 1 {
		return res.Rows[0][0].(string), nil
	}
	return "", nil
}

// memberRecordPatch keeps the fields of want (that the model has) whose stored value differs, so a sync of a record
// already in step writes nothing.
func memberRecordPatch(m *meta.Model, cur store.Record, want map[string]any) map[string]any {
	patch := map[string]any{}
	for k, v := range want {
		f := m.Field(k)
		if f == nil {
			continue
		}
		switch k {
		case "outlets":
			var have []string
			for _, r := range rows(cur[k]) {
				have = append(have, str(r["outlet"]))
			}
			var next []string
			for _, r := range rows(v) {
				next = append(next, str(r["outlet"]))
			}
			if !slices.Equal(have, next) {
				patch[k] = v
			}
		case "account_id":
			if cur[k] == nil || toInt(cur[k]) != toInt(v) {
				patch[k] = v
			}
		default:
			if str(cur[k]) != str(v) {
				patch[k] = v
			}
		}
	}
	return patch
}

// outletRows are the user_outlet rows of a membership's outlets, in its order.
func outletRows(outlets []string) []any {
	out := []any{}
	for _, o := range outlets {
		out = append(out, map[string]any{"outlet": o})
	}
	return out
}

// textList converts a scanned text[] to []string (never nil).
func textList(v any) []string {
	out := []string{}
	switch a := v.(type) {
	case []any:
		for _, x := range a {
			out = append(out, fmt.Sprint(x))
		}
	case []string:
		out = append(out, a...)
	}
	return out
}

// Refusals of MemberRecordClaim (contract 0056 §3).
var (
	ErrUnknownRecord = errors.New("unknown_record")
	ErrRecordTaken   = errors.New("record_taken")
)

// MemberRecordClaim checks whether user record `record` of org may become the record of account userID (0: an
// account not created yet), for every path that sets rk_members.record (contract 0056 §3: the member API's record,
// `rangka user add --record`, the seed's user record). ErrUnknownRecord: no such record. ErrRecordTaken: it is
// another account's (it carries another account_id, or another membership in org points at it), or the account
// already has another record in org (its membership points at one, or one carries its account_id): a member is
// never moved to a second record, which would leave the first one stale. It reads the database only (the CLI may
// run without metadata): without a `user` table with account_id, it checks nothing.
func MemberRecordClaim(ctx context.Context, q pg.Querier, org, record string, userID int64) error {
	table := store.TableName(memberRecordModel)
	res, err := q.Query(ctx, `select exists (select 1 from pg_attribute where attrelid = to_regclass($1) and attname = 'account_id' and not attisdropped)`, table)
	if err != nil || !res.Rows[0][0].(bool) {
		return err
	}
	qt := pg.QuoteIdent(table)
	res, err = q.Query(ctx, "select coalesce(account_id, 0) from "+qt+" where org_id = $1 and name = $2", org, record)
	if err != nil {
		return err
	}
	if len(res.Rows) == 0 {
		return ErrUnknownRecord
	}
	if acc := toInt(res.Rows[0][0]); acc != 0 && acc != userID {
		return ErrRecordTaken
	}
	res, err = q.Query(ctx, "select 1 from rk_members where org_id = $1 and record = $2 and user_id <> $3 limit 1", org, record, userID)
	if err != nil {
		return err
	}
	if len(res.Rows) > 0 {
		return ErrRecordTaken
	}
	if userID == 0 {
		return nil
	}
	res, err = q.Query(ctx, "select 1 from "+qt+" where org_id = $1 and name <> $2 and coalesce(account_id, 0) = $3 limit 1", org, record, userID)
	if err != nil {
		return err
	}
	if len(res.Rows) > 0 {
		return ErrRecordTaken
	}
	res, err = q.Query(ctx, "select 1 from rk_members m join "+qt+" r on r.org_id = m.org_id and r.name = m.record "+
		"where m.org_id = $1 and m.user_id = $2 and m.record <> $3", org, userID, record)
	if err != nil {
		return err
	}
	if len(res.Rows) > 0 {
		return ErrRecordTaken
	}
	return nil
}
