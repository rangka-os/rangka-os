package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Kernel notices (contract 0066): what the kernel tells its people. Every notice is one event (§2) that goes to the
// kernel's own inbox (rk_notices, one row per recipient, §4) and, when the deployment names a host
// (RANGKA_HOST_NOTICES_URL), to the host as a notice_host job (§3).

// NoticeTo is an event's recipients (§2): roles, not user ids.
type NoticeTo struct {
	Owner  bool     `json:"owner"`
	Roles  []string `json:"roles"`
	Emails []string `json:"emails"`
	Outlet string   `json:"outlet,omitempty"`
}

// NoticeEvent is the event of contract 0066 §2, the body of the host call.
type NoticeEvent struct {
	ID       string         `json:"id"`
	Org      string         `json:"org"`
	Kind     string         `json:"kind"`
	Severity string         `json:"severity"`
	To       NoticeTo       `json:"to"`
	Title    string         `json:"title"`
	Text     string         `json:"text"`
	Link     string         `json:"link"`
	Data     map[string]any `json:"data"`
	At       string         `json:"at"`
}

// NoticeConfig is how this kernel delivers notices beyond its inbox.
type NoticeConfig struct {
	// Host is true when RANGKA_HOST_NOTICES_URL and RANGKA_HOST_NOTICES_SECRET are both set: every event is posted
	// to the host by a notice_host job (§3).
	Host bool
}

// noticeRecord is the record a notice is about: recipients must be able to read it (§4.1, §6).
type noticeRecord struct{ model, name string }

// noticeOpts are the parts of an emit that are not in the event.
type noticeOpts struct {
	record *noticeRecord
	except []string // emails that never get it (the requester of an approval)
}

const (
	noticeMaxRoles  = 20
	noticeMaxEmails = 20
	noticeMaxTitle  = 80
	noticeMaxText   = 300
	noticeMaxLink   = 512
	noticeMaxData   = 4096
	noticeMaxID     = 128
)

var (
	noticeKindPart = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	noticeIDChars  = regexp.MustCompile(`[^A-Za-z0-9._:-]`)
	noticeSpace    = regexp.MustCompile(`\s+`)
)

// NoticeSeverities are the severities of §2.
var NoticeSeverities = map[string]bool{"info": true, "warn": true, "high": true}

// ValidNoticeKind reports whether kind is app.<module>.<name>, site.<name> or kernel.<area>.<event> (§1).
func ValidNoticeKind(kind string) bool {
	parts := strings.Split(kind, ".")
	switch {
	case len(parts) == 3 && (parts[0] == "app" || parts[0] == "kernel"):
	case len(parts) == 2 && parts[0] == "site":
	default:
		return false
	}
	for _, p := range parts[1:] {
		if !noticeKindPart.MatchString(p) {
			return false
		}
	}
	return true
}

// NoticePlain is a title or text as plain text: control characters removed, whitespace runs made one space, cut to
// max runes (an ellipsis marks a cut).
func NoticePlain(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(noticeSpace.ReplaceAllString(s, " "))
	if utf8.RuneCountInString(s) > max {
		r := []rune(s)
		s = strings.TrimSpace(string(r[:max-1])) + "…"
	}
	return s
}

// noticeID makes an event id of §2: [A-Za-z0-9._:-], at most 128 chars, the same for the same input.
func noticeID(s string) string {
	s = noticeIDChars.ReplaceAllString(s, "_")
	if s == "" {
		s = "notice"
	}
	if len(s) > noticeMaxID {
		sum := sha256.Sum256([]byte(s))
		s = s[:noticeMaxID-17] + ":" + hex.EncodeToString(sum[:8])
	}
	return s
}

// normalize applies the field rules of §2 to ev.
func (ev *NoticeEvent) normalize() {
	ev.ID = noticeID(ev.ID)
	if !NoticeSeverities[ev.Severity] {
		ev.Severity = "info"
	}
	ev.Title = NoticePlain(ev.Title, noticeMaxTitle)
	if ev.Title == "" {
		ev.Title = NoticePlain(ev.Kind, noticeMaxTitle)
	}
	ev.Text = NoticePlain(ev.Text, noticeMaxText)
	if !strings.HasPrefix(ev.Link, "/app/") || len(ev.Link) > noticeMaxLink || strings.Contains(ev.Link, "//") || strings.Contains(ev.Link, `\`) {
		ev.Link = ""
	}
	if ev.Data == nil {
		ev.Data = map[string]any{}
	}
	if b, err := json.Marshal(ev.Data); err != nil || len(b) > noticeMaxData {
		ev.Data = map[string]any{}
	}
	ev.To.Roles = uniqueStrings(ev.To.Roles, "owner")
	ev.To.Emails = uniqueStrings(ev.To.Emails, "")
	if len(ev.To.Roles) > noticeMaxRoles {
		ev.To.Roles = ev.To.Roles[:noticeMaxRoles]
	}
	if len(ev.To.Emails) > noticeMaxEmails {
		ev.To.Emails = ev.To.Emails[:noticeMaxEmails]
	}
}

// uniqueStrings drops empties, drop and repeats, keeping the order.
func uniqueStrings(in []string, drop string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range in {
		if s == "" || s == drop || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// noticeMember is a member of the organisation as notices see them.
type noticeMember struct {
	user                int64
	email, role, record string
	outlets             []string
}

func (m noticeMember) actor(org string) Actor {
	return Actor{Org: org, ID: m.email, UserName: m.record, Roles: []string{m.role}, Outlets: m.outlets, Human: true}
}

// noticeMembers lists the organisation's members.
func (o *op) noticeMembers() ([]noticeMember, error) {
	res, err := o.tx.Query(o.ctx, `select m.user_id, u.email, m.role, coalesce(m.record, ''), m.outlets from rk_members m
		join rk_users u on u.id = m.user_id where m.org_id = $1 order by m.user_id`, o.actor.Org)
	if err != nil {
		return nil, err
	}
	out := make([]noticeMember, 0, len(res.Rows))
	for _, r := range res.Rows {
		id, _ := r[0].(int64)
		out = append(out, noticeMember{user: id, email: strings.ToLower(str(r[1])), role: str(r[2]), record: str(r[3]), outlets: meta.StrList(r[4])})
	}
	return out, nil
}

// notice emits one event: it keeps only the roles that may read the record (§6) and the emails of members, writes a
// row in the inbox of every member it reaches (§4.1) and publishes it to them (SSE notice), and posts it to the host
// when one is configured. All of it happens in o's transaction: a rolled-back write tells nobody. Past the per-minute
// cap (§3) the event is dropped and counted. A notice never fails the work that raised it: its errors are logged.
func (o *op) notice(ev NoticeEvent, opt noticeOpts) error {
	e := o.e
	if !ValidNoticeKind(ev.Kind) {
		e.Log("notice: kind %q is not a notice kind; not sent", ev.Kind)
		return nil
	}
	ev.Org = o.actor.Org
	if ev.At == "" {
		ev.At = e.Now().UTC().Format(time.RFC3339)
	}
	ev.normalize()
	members, err := o.noticeMembers()
	if err != nil {
		return err
	}
	byEmail := map[string]noticeMember{}
	for _, m := range members {
		byEmail[m.email] = m
	}
	except := map[string]bool{}
	for _, x := range opt.except {
		except[strings.ToLower(x)] = true
	}
	emails := []string{}
	for _, x := range ev.To.Emails { // never an address outside the organisation (§2)
		if m, ok := byEmail[strings.ToLower(x)]; ok && !except[m.email] {
			emails = append(emails, m.email)
		}
	}
	ev.To.Emails = emails
	var rec store.Record
	var recModel *meta.Model
	if opt.record != nil && opt.record.model != "" && opt.record.name != "" {
		if m := o.registry().Model(opt.record.model); m != nil {
			if r, err := o.load(m, opt.record.name); err == nil {
				recModel, rec = m, o.e.Output(m, copyRecord(r))
			} else if !IsNotFound(err) {
				return err
			}
		}
	}
	if recModel != nil { // only roles that may open the record (§6)
		kept := []string{}
		for _, r := range ev.To.Roles {
			ra := Actor{Org: ev.Org, ID: "role:" + r, Roles: []string{r}, Human: true}
			if e.authorizeIn(o.registry(), o.ctx, o.tx, nil, ra, recModel.Name, "read", rec) == nil {
				kept = append(kept, r)
			}
		}
		ev.To.Roles = kept
	}
	if !ev.To.Owner && len(ev.To.Roles) == 0 && len(ev.To.Emails) == 0 {
		return nil // nobody to tell
	}
	ok, err := store.TakeNoticeSlot(context.WithoutCancel(o.ctx), e.DB, ev.Org, ev.Kind, e.Now())
	if err != nil {
		e.Log("notice %s: rate count: %v", ev.ID, err)
	} else if !ok {
		return nil
	}
	roles := map[string]bool{}
	for _, r := range ev.To.Roles {
		roles[r] = true
	}
	named := map[string]bool{}
	for _, x := range ev.To.Emails {
		named[x] = true
	}
	at, _ := time.Parse(time.RFC3339, ev.At)
	for _, m := range members {
		if except[m.email] {
			continue
		}
		if !(ev.To.Owner && m.role == "owner") && !roles[m.role] && !named[m.email] {
			continue
		}
		if ev.To.Outlet != "" && len(m.outlets) > 0 && !contains(m.outlets, ev.To.Outlet) {
			continue // a member limited to other outlets (0010 §5)
		}
		if recModel != nil {
			ma := m.actor(ev.Org)
			if e.authorizeIn(o.registry(), o.ctx, o.tx, nil, ma, recModel.Name, "read", rec) != nil ||
				e.guardIn(o.registry(), o.ctx, ma, recModel.Name, rec, "read") != nil {
				continue
			}
		}
		n := store.Notice{Kind: ev.Kind, Severity: ev.Severity, Title: ev.Title, Text: ev.Text, Link: ev.Link, Data: ev.Data, At: at}
		id, err := store.InsertNotice(o.ctx, o.tx, ev.Org, ev.ID, m.user, n)
		if err != nil {
			return err
		}
		if id == "" {
			continue // already told
		}
		item := map[string]any{"id": id, "kind": n.Kind, "severity": n.Severity, "title": n.Title, "text": n.Text, "link": n.Link, "at": ev.At, "read": false}
		if err := store.Notify(o.ctx, o.tx, store.Event{Type: "notice", Org: ev.Org, ID: m.email, Notice: item}); err != nil {
			return err
		}
	}
	return o.noticeDeliver(ev)
}

// noticeDeliver queues what leaves the kernel: the host call (§3).
func (o *op) noticeDeliver(ev NoticeEvent) error {
	if !o.e.Notices.Host || o.e.Queue == nil {
		return nil
	}
	b, _ := json.Marshal(ev)
	var args map[string]any
	_ = json.Unmarshal(b, &args)
	_, err := o.e.Queue.Enqueue(o.ctx, o.tx, "notice_host", ev.Org, map[string]any{"event": args},
		EnqueueOpts{MaxAttempts: NoticeHostAttempts, UniqueKey: "notice_host:" + ev.Org + ":" + ev.ID})
	return err
}

// NoticeHostAttempts is how often the kernel tries to post an event to the host (§3).
const NoticeHostAttempts = 8

// memberName is a member's name for a notice text: full name, else the email.
func (o *op) memberName(email string) string {
	res, err := o.tx.Query(o.ctx, "select full_name from rk_users where email = $1", strings.ToLower(email))
	if err == nil && len(res.Rows) == 1 {
		if n := strings.TrimSpace(str(res.Rows[0][0])); n != "" {
			return n
		}
	}
	return email
}

// approvalNotices raises the built-in notices of §5 for a request's change: kernel.approval.requested when it is
// created pending, kernel.approval.decided when someone other than the requester approves or rejects it.
func (o *op) approvalNotices(ap *store.Approval, kind string) error {
	what := ap.Name
	if what == "" {
		what = approvalLabel(ap)
	}
	rec := &noticeRecord{model: ap.Model, name: ap.Name}
	if isFlowApproval(ap) {
		rec = nil
		if m, name := o.e.flowRecord(ap); m != nil {
			rec = &noticeRecord{model: m.Name, name: name}
		}
	}
	data := map[string]any{"model": ap.Model, "name": ap.Name, "approval": ap.ID}
	switch {
	case kind == "approval.requested" && ap.State == "pending":
		roles := append([]string{}, ap.Approvers...)
		return o.notice(NoticeEvent{
			ID: "kernel.approval.requested:" + ap.ID, Kind: "kernel.approval.requested", Severity: "warn",
			To:    NoticeTo{Owner: true, Roles: roles},
			Title: what + " menunggu persetujuanmu", Text: approvalLabel(ap) + " · diajukan oleh " + o.memberName(ap.RequestedBy),
			Link: "/app/#/approvals", Data: data,
		}, noticeOpts{record: rec, except: []string{ap.RequestedBy}})
	case (kind == "approval.approved" || kind == "approval.rejected") && ap.DecidedBy != "" && !strings.EqualFold(ap.DecidedBy, ap.RequestedBy):
		ev := NoticeEvent{ID: "kernel.approval.decided:" + ap.ID, Kind: "kernel.approval.decided", Severity: "info",
			To: NoticeTo{Emails: []string{ap.RequestedBy}}, Title: what + " disetujui",
			Text: approvalLabel(ap) + " · oleh " + o.memberName(ap.DecidedBy), Link: "/app/#/approvals", Data: data}
		if kind == "approval.rejected" {
			ev.Severity, ev.Title = "warn", what+" ditolak: "+ap.Reason
		}
		if !isFlowApproval(ap) && ap.Model != "" && ap.Name != "" {
			ev.Link = "/app/#/r/" + ap.Model + "/" + ap.Name
		}
		return o.notice(ev, noticeOpts{record: rec})
	}
	return nil
}

// flowNotice is the implicit notice of a notify step without notice: (§1.3): a role target (or owner) tells that
// role, a target that resolved to field values tells the members those values name (their email or their user
// record); other values are dropped and counted (dropped).
func (r *flowRun) flowNotice(st map[string]any, index, target string, values []string, text string) (dropped int, err error) {
	kind := r.noticeKind()
	if kind == "" {
		return 0, nil
	}
	reg := r.registry()
	to := NoticeTo{}
	resolved := !(len(values) == 1 && values[0] == target) // recipients() found a variable or field values
	_, isVar := r.vars[target]
	switch {
	case !resolved && !isVar && target == "owner":
		to.Owner = true
	case !resolved && !isVar && reg.Roles[target] != nil:
		to.Roles = []string{target}
	default:
		members, err := r.o.noticeMembers()
		if err != nil {
			return 0, err
		}
		for _, v := range values {
			found := false
			for _, m := range members {
				if strings.EqualFold(v, m.email) || (m.record != "" && v == m.record) {
					to.Emails, found = append(to.Emails, m.email), true
					break
				}
			}
			if !found {
				dropped++
			}
		}
		if len(to.Emails) == 0 {
			return dropped, nil
		}
	}
	title := r.f.Label.Text()
	if title == "" {
		title = r.f.Name
	}
	if r.noticeSeen == nil {
		r.noticeSeen = map[string]int{}
	}
	r.noticeSeen[index]++
	id := kind + ":" + r.runID + ":" + index
	if n := r.noticeSeen[index]; n > 1 { // a step under for_each runs once per item
		id += ":" + itoa(int64(n))
	}
	return dropped, r.o.notice(NoticeEvent{ID: id, Kind: kind, Severity: "info", To: to, Title: title, Text: text}, noticeOpts{})
}

// noticeKind is the kind of the implicit notices of the run's flow: site.<flow> for a site flow, else
// app.<module>.<flow>; "" when a name cannot be part of a kind.
func (r *flowRun) noticeKind() string {
	kind := "app." + r.f.App + "." + r.f.Name
	if a := r.registry().App[r.f.App]; a != nil && a.Kind == meta.KindSite {
		kind = "site." + r.f.Name
	}
	if !ValidNoticeKind(kind) {
		return ""
	}
	return kind
}

// NoticeKinds lists the kinds this installation can raise, with a label: the built-in ones (§5) and the implicit
// kinds of installed flows with a notify step (§1.3). Sorted by kind.
func (e *Engine) NoticeKinds() []map[string]any {
	reg := e.Reg()
	out := []map[string]any{
		{"kind": "kernel.approval.requested", "label": "Permintaan persetujuan"},
		{"kind": "kernel.approval.decided", "label": "Keputusan persetujuan"},
	}
	for _, name := range sortedKeysF(reg.Flows) {
		f := reg.Flows[name]
		if !reg.IsInstalled(f.App) || !flowHasNotify(f.Steps) {
			continue
		}
		r := &flowRun{f: f, reg: reg}
		if k := r.noticeKind(); k != "" {
			label := f.Label.Text()
			if label == "" {
				label = f.Name
			}
			out = append(out, map[string]any{"kind": k, "label": label})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return str(out[i]["kind"]) < str(out[j]["kind"]) })
	return out
}

func sortedKeysF(m map[string]*meta.Flow) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// flowHasNotify reports whether steps (or the steps nested under them) hold a notify step.
func flowHasNotify(steps []map[string]any) bool {
	var walk func(v any) bool
	walk = func(v any) bool {
		switch x := v.(type) {
		case map[string]any:
			if x["notify"] != nil {
				return true
			}
			for _, k := range []string{"do", "then", "else", "steps"} {
				if walk(x[k]) {
					return true
				}
			}
		case []any:
			for _, s := range x {
				if walk(s) {
					return true
				}
			}
		case []map[string]any:
			for _, s := range x {
				if walk(s) {
					return true
				}
			}
		}
		return false
	}
	return walk(steps)
}
