// Package secrets holds connector credentials (contract 0017). Values are bound per org and per connector, never in
// metadata files. The site picks one driver in system.rk secrets.driver:
//
//   - env, file, vault: read only. system.rk connector_auth: binds each field to a ${VAR} reference (or a literal for a
//     field that is not secret); env reads the process environment, file reads <secrets.path>/<VAR>. vault is not
//     available yet, so its fields report unset.
//   - db: values set through the write-only API, stored in rk_secrets with envelope encryption (keys.go).
//
// Nothing in this package returns a value to a caller outside the kernel: Status reports set, hint and version only.
// Value is for the connector runtime, which renders a request in memory at send time (§6).
package secrets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Errors with their API codes (contract 0017 §2, §3).
var (
	ErrReadOnlyDriver = errors.New("secrets_read_only_driver")
	ErrKeyMissing     = errors.New("secrets_key_missing")
	ErrUnknownField   = errors.New("unknown_field")
	ErrEmptyValue     = errors.New("empty_value")
)

// Field is one auth field of a connector, as declared in connector.rk.
type Field struct {
	Name     string
	Label    any
	Secret   bool
	Required bool
}

// Status is what the API says about one field. It never carries a secret value; Value is set for fields that are not
// secret only (they are not credentials, e.g. a phone number id).
type Status struct {
	Field    string `json:"field"`
	Label    any    `json:"label,omitempty"`
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
	Set      bool   `json:"set"`
	Hint     string `json:"hint,omitempty"`
	Version  int64  `json:"version,omitempty"`
	SetBy    string `json:"set_by,omitempty"`
	SetAt    string `json:"set_at,omitempty"`
	Value    any    `json:"value,omitempty"`
}

// Service resolves and stores connector secrets.
type Service struct {
	DB  *pg.Pool
	Reg func() *meta.Registry
	KEK *KEK // nil = no key: the db driver refuses writes with secrets_key_missing
	// Getenv reads the environment (os.LookupEnv when nil).
	Getenv func(string) (string, bool)
	Now    func() time.Time
	// Grace is how long a replaced version still verifies inbound webhooks (§8); 24 hours when zero.
	Grace time.Duration

	mu    sync.Mutex
	cache map[string]cached // org|connector|field → decrypted newest value, at most 5 minutes (§6)
}

type cached struct {
	value string
	at    time.Time
}

const cacheTTL = 5 * time.Minute

// Driver is the site's secrets driver: env (the default), file, vault or db.
func (s *Service) Driver() string {
	if s.Reg == nil {
		return "env"
	}
	if d, ok := s.Reg().SystemConfig("secrets.driver").(string); ok && d != "" {
		return d
	}
	return "env"
}

// Writable reports whether values are set through the API (the db driver).
func (s *Service) Writable() bool { return s.Driver() == "db" }

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) grace() time.Duration {
	if s.Grace > 0 {
		return s.Grace
	}
	return 24 * time.Hour
}

// Fields lists the auth fields a connector declares; nil when the connector does not exist.
func Fields(reg *meta.Registry, connector string) []Field {
	c := reg.Connectors[connector]
	if c == nil {
		return nil
	}
	out := []Field{}
	for _, x := range meta.AsList(meta.AsMap(meta.AsMap(c.Data)["auth"])["fields"]) {
		f := meta.AsMap(x)
		out = append(out, Field{Name: meta.Str(f, "name"), Label: f["label"], Secret: f["secret"] == true, Required: f["required"] != false})
	}
	return out
}

// Status reports every auth field of a connector for one org: set, and for the db driver hint, version, set_by and
// set_at. Fields that are not secret also carry their value.
func (s *Service) Status(ctx context.Context, org, connector string) ([]Status, error) {
	reg := s.Reg()
	fields := Fields(reg, connector)
	out := make([]Status, 0, len(fields))
	stored := map[string]Status{}
	if s.Driver() == "db" {
		res, err := s.DB.Query(ctx, `select distinct on (field) field, version, hint, set_by, set_at from rk_secrets
			where org_id = $1 and connector = $2 and retire_at is null order by field, version desc`, org, connector)
		if err != nil {
			return nil, err
		}
		for _, r := range res.Rows {
			st := Status{Set: true, Version: toInt(r[1]), Hint: r[2].(string), SetBy: r[3].(string)}
			if t, ok := r[4].(time.Time); ok {
				st.SetAt = t.UTC().Format(time.RFC3339)
			}
			stored[r[0].(string)] = st
		}
	}
	for _, f := range fields {
		st := Status{Field: f.Name, Label: f.Label, Secret: f.Secret, Required: f.Required}
		if s.Driver() == "db" {
			if v, ok := stored[f.Name]; ok {
				st.Set, st.Version, st.Hint, st.SetBy, st.SetAt = true, v.Version, v.Hint, v.SetBy, v.SetAt
				if !f.Secret && s.KEK != nil { // not a credential: its value is shown (§3)
					if val, ok, err := s.Value(ctx, org, connector, f.Name); err == nil && ok {
						st.Value = val
					}
				}
			}
			// a field that is not secret may still be bound in system.rk (e.g. phone_number_id)
			if !st.Set && !f.Secret {
				if v, ok := s.bound(reg, org, connector, f.Name); ok {
					st.Set, st.Value = true, v
				}
			}
		} else if v, ok := s.bound(reg, org, connector, f.Name); ok {
			st.Set = true
			if !f.Secret {
				st.Value = v
			}
		}
		if f.Secret {
			st.Value = nil
		}
		out = append(out, st)
	}
	return out, nil
}

// SetMap reports, per field name, whether the field has a value (for the catalogue).
func (s *Service) SetMap(ctx context.Context, org, connector string) map[string]bool {
	out := map[string]bool{}
	list, err := s.Status(ctx, org, connector)
	if err != nil {
		return out
	}
	for _, st := range list {
		out[st.Field] = st.Set
	}
	return out
}

var varRef = regexp.MustCompile(`^\$\{([A-Z][A-Z0-9_]*)\}$`)

// bound resolves a system.rk connector_auth binding: orgs.<org>.<connector>.<field> first, then
// <connector>.<field>. A ${VAR} reads the environment (env) or <secrets.path>/<VAR> (file); an unset variable is unset.
func (s *Service) bound(reg *meta.Registry, org, connector, field string) (string, bool) {
	ca := meta.AsMap(reg.SystemConfig("connector_auth"))
	raw, ok := meta.AsMap(meta.AsMap(meta.AsMap(ca["orgs"])[org])[connector])[field]
	if !ok {
		raw, ok = meta.AsMap(ca[connector])[field]
	}
	if !ok || raw == nil {
		return "", false
	}
	text := fmt.Sprint(raw)
	m := varRef.FindStringSubmatch(text)
	if m == nil {
		return text, text != ""
	}
	switch s.Driver() {
	case "file":
		dir, _ := reg.SystemConfig("secrets.path").(string)
		if dir == "" {
			return "", false
		}
		b, err := os.ReadFile(filepath.Join(dir, m[1]))
		if err != nil {
			return "", false
		}
		v := strings.TrimRight(string(b), "\r\n")
		return v, v != ""
	case "vault":
		return "", false // the vault driver is not available yet (build order step 3)
	default: // env, and db for fields that are not secret
		get := s.Getenv
		if get == nil {
			get = os.LookupEnv
		}
		v, ok := get(m[1])
		return v, ok && v != ""
	}
}

func (s *Service) checkField(connector, field string) (Field, error) {
	for _, f := range Fields(s.Reg(), connector) {
		if f.Name == field {
			return f, nil
		}
	}
	return Field{}, ErrUnknownField
}

// Hint is the last 4 characters of a value of 16 characters or more, else empty (§2).
func Hint(v string) string {
	r := []rune(v)
	if len(r) < 16 {
		return ""
	}
	return string(r[len(r)-4:])
}

// Set stores a new version of a field for one org (db driver only) and audits secret.set without the value.
// The previous version stays valid for inbound verification until its retire_at.
func (s *Service) Set(ctx context.Context, org, connector, field, value, actor string) (Status, error) {
	if !s.Writable() {
		return Status{}, ErrReadOnlyDriver
	}
	f, err := s.checkField(connector, field)
	if err != nil {
		return Status{}, err
	}
	if value == "" {
		return Status{}, ErrEmptyValue
	}
	if s.KEK == nil {
		return Status{}, ErrKeyMissing
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Status{}, err
	}
	defer tx.Rollback(ctx)
	// one writer per (org, connector, field) at a time, so versions do not collide
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock(hashtextextended($1, 17))", org+"|"+connector+"|"+field); err != nil {
		return Status{}, err
	}
	dekID, dek, err := s.orgKey(ctx, tx, org)
	if err != nil {
		return Status{}, err
	}
	res, err := tx.Query(ctx, "select coalesce(max(version), 0) from rk_secrets where org_id = $1 and connector = $2 and field = $3", org, connector, field)
	if err != nil {
		return Status{}, err
	}
	version := toInt(res.Rows[0][0]) + 1
	ct, nonce, err := seal(dek, []byte(value), valueAAD(org, connector, field, version))
	if err != nil {
		return Status{}, err
	}
	now := s.now()
	if _, err := tx.Exec(ctx, `update rk_secrets set retire_at = $4 where org_id = $1 and connector = $2 and field = $3 and retire_at is null`,
		org, connector, field, now.Add(s.grace())); err != nil {
		return Status{}, err
	}
	hint := ""
	if f.Secret {
		hint = Hint(value)
	}
	if _, err := tx.Exec(ctx, `insert into rk_secrets (org_id, connector, field, version, ciphertext, nonce, dek_id, hint, set_by, set_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, org, connector, field, version, ct, nonce, dekID, hint, actor, now); err != nil {
		return Status{}, err
	}
	// drop versions whose grace period is over
	if _, err := tx.Exec(ctx, `delete from rk_secrets where org_id = $1 and connector = $2 and field = $3 and retire_at < $4`,
		org, connector, field, now); err != nil {
		return Status{}, err
	}
	data := map[string]any{"connector": connector, "field": field, "version": version}
	if hint != "" {
		data["hint"] = hint
	}
	if err := store.Audit(ctx, tx, org, actor, "secret.set", "connector/"+connector, "secret "+field+" set (version "+fmt.Sprint(version)+")", data); err != nil {
		return Status{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Status{}, err
	}
	s.forget(org, connector)
	return Status{Field: field, Secret: f.Secret, Required: f.Required, Set: true, Version: version, Hint: hint, SetBy: actor,
		SetAt: now.UTC().Format(time.RFC3339)}, nil
}

// Delete removes every version of a field for one org (db driver only) and audits secret.delete.
func (s *Service) Delete(ctx context.Context, org, connector, field, actor string) error {
	if !s.Writable() {
		return ErrReadOnlyDriver
	}
	if _, err := s.checkField(connector, field); err != nil {
		return err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	res, err := tx.Query(ctx, "delete from rk_secrets where org_id = $1 and connector = $2 and field = $3 returning version", org, connector, field)
	if err != nil {
		return err
	}
	var version int64
	for _, r := range res.Rows {
		if v := toInt(r[0]); v > version {
			version = v
		}
	}
	data := map[string]any{"connector": connector, "field": field, "version": version}
	if err := store.Audit(ctx, tx, org, actor, "secret.delete", "connector/"+connector, "secret "+field+" deleted", data); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.forget(org, connector)
	return nil
}

// Value returns the newest value of a field for the connector runtime, which renders a request in memory and drops it
// after the call (§6). It is never served by the API.
func (s *Service) Value(ctx context.Context, org, connector, field string) (string, bool, error) {
	vals, err := s.values(ctx, org, connector, field, false)
	if err != nil || len(vals) == 0 {
		return "", false, err
	}
	return vals[0], true, nil
}

// Accepted returns the values that verify an inbound webhook: the newest, and the previous one until its retire_at
// (§8). For read-only drivers it is the bound value.
func (s *Service) Accepted(ctx context.Context, org, connector, field string) ([]string, error) {
	return s.values(ctx, org, connector, field, true)
}

func (s *Service) values(ctx context.Context, org, connector, field string, inGrace bool) ([]string, error) {
	if s.Driver() != "db" {
		if v, ok := s.bound(s.Reg(), org, connector, field); ok {
			return []string{v}, nil
		}
		return nil, nil
	}
	key := org + "|" + connector + "|" + field
	if !inGrace {
		s.mu.Lock()
		if c, ok := s.cache[key]; ok && s.now().Sub(c.at) < cacheTTL {
			s.mu.Unlock()
			return []string{c.value}, nil
		}
		s.mu.Unlock()
	}
	if s.KEK == nil {
		return nil, ErrKeyMissing
	}
	res, err := s.DB.Query(ctx, `select s.version, s.ciphertext, s.nonce, k.wrapped, k.nonce, k.dek_id, k.kek_id from rk_secrets s
		join rk_org_keys k on k.org_id = s.org_id and k.dek_id = s.dek_id
		where s.org_id = $1 and s.connector = $2 and s.field = $3 and (s.retire_at is null or ($4 and s.retire_at > $5))
		order by s.version desc`, org, connector, field, inGrace, s.now())
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, r := range res.Rows {
		dek, err := s.KEK.unwrap(org, r[5].(string), r[6].(string), r[3].([]byte), r[4].([]byte))
		if err != nil {
			return nil, err
		}
		v, err := open(dek, r[1].([]byte), r[2].([]byte), valueAAD(org, connector, field, toInt(r[0])))
		if err != nil {
			return nil, err
		}
		out = append(out, string(v))
		if !inGrace {
			break
		}
	}
	if len(out) > 0 && !inGrace {
		s.mu.Lock()
		if s.cache == nil {
			s.cache = map[string]cached{}
		}
		s.cache[key] = cached{value: out[0], at: s.now()}
		s.mu.Unlock()
	}
	return out, nil
}

// forget clears the cached values of one (org, connector) after a set or delete (§6).
func (s *Service) forget(org, connector string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := org + "|" + connector + "|"
	for k := range s.cache {
		if strings.HasPrefix(k, prefix) {
			delete(s.cache, k)
		}
	}
}

func valueAAD(org, connector, field string, version int64) []byte {
	return []byte(org + "|" + connector + "|" + field + "|" + fmt.Sprint(version))
}

func toInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int32:
		return int64(n)
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}
