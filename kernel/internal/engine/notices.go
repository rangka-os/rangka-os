package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/notices"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
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
	// Mail is true when system.rk notify.email is smtp and no host is configured: the kernel mails notices itself
	// (§4.2), one notice_mail job per recipient whose choice for the kind is on.
	Mail bool
	// BaseURL is the kernel's public address (system.rk auth.issuer): mails link to BaseURL + the notice's link.
	BaseURL string
}

// NoticeMailAttempts is how often the kernel tries to mail one notice (§4.2: the backoff of §3).
const NoticeMailAttempts = 8

// NoticePrefsPath is the web app's page of a person's notice choices (Pengaturan › Notifikasi).
const NoticePrefsPath = "/app/#/pengaturan/notifikasi"

// noticeRecord is the record a notice is about: recipients must be able to read it (§4.1, §6).
type noticeRecord struct{ model, name string }

// noticeOpts are the parts of an emit that are not in the event.
type noticeOpts struct {
	record *noticeRecord
	except []string // emails that never get it (the requester of an approval)
	// mail is the kind's mail default from the step's channel (email on, in_app off); nil = by severity (§4.2)
	mail *bool
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
		if err := o.noticeMail(ev, m, opt.mail); err != nil {
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

// NoticeMailOn decides whether a person gets a kind by mail (§4.2): their choice for the kind, else their choice for
// every kind ('*'), else the kind's default (def, else off for info and on for warn and high).
func NoticeMailOn(ctx context.Context, q pg.Querier, org string, user int64, kind, severity string, def *bool) (bool, error) {
	on := severity != "info"
	if def != nil {
		on = *def
	}
	res, err := q.Query(ctx, "select kind, mail from rk_notice_prefs where org_id = $1 and user_id = $2 and kind in ($3, '*')", org, user, kind)
	if err != nil {
		return on, err
	}
	for _, r := range res.Rows {
		if str(r[0]) == "*" {
			on, _ = r[1].(bool)
		}
	}
	for _, r := range res.Rows {
		if str(r[0]) == kind {
			on, _ = r[1].(bool)
		}
	}
	return on, nil
}

// noticeMail queues the mail of one recipient when the kernel mails notices itself (never with a host: it mails).
func (o *op) noticeMail(ev NoticeEvent, m noticeMember, def *bool) error {
	e := o.e
	if !e.Notices.Mail || e.Notices.Host || e.Queue == nil || !strings.Contains(m.email, "@") {
		return nil
	}
	on, err := NoticeMailOn(o.ctx, o.tx, ev.Org, m.user, ev.Kind, ev.Severity, def)
	if err != nil || !on {
		return err
	}
	base := strings.TrimRight(e.Notices.BaseURL, "/")
	link := ""
	if ev.Link != "" && base != "" {
		link = base + ev.Link
	}
	prefs := ""
	if base != "" {
		prefs = base + NoticePrefsPath
	}
	_, err = e.Queue.Enqueue(o.ctx, o.tx, "notice_mail", ev.Org, map[string]any{"to": m.email, "user": m.user, "id": ev.ID, "kind": ev.Kind,
		"subject": ev.Title, "body": notices.MailBody(ev.Title, ev.Text, link, prefs)},
		EnqueueOpts{MaxAttempts: NoticeMailAttempts, UniqueKey: "notice_mail:" + ev.Org + ":" + ev.ID + ":" + itoa(m.user)})
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

// flowNotice is the notice of a notify step (§1.2, §1.3). With notice: it is the module's declaration (kind
// app.<module>.<notice>, its severity, roles, can: and label) plus the step's own target; without, the implicit kind
// of the flow. A role target (or owner) tells that role; a target that resolved to field values tells the members
// those values name (their email or their user record), and other values are dropped and counted (dropped). The
// step's title, record, outlet and id keys apply to both.
func (r *flowRun) flowNotice(st map[string]any, index, target string, values []string, text string) (dropped int, err error) {
	reg := r.registry()
	kind := r.noticeKind()
	decl := r.noticeDecl(str(st["notice"]))
	if decl != nil {
		kind = "app." + r.f.App + "." + str(decl["name"])
		if !ValidNoticeKind(kind) {
			kind = ""
		}
	}
	if kind == "" {
		return 0, nil
	}
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
	}
	severity, title := "info", ""
	if decl != nil {
		if NoticeSeverities[str(decl["severity"])] {
			severity = str(decl["severity"])
		}
		declTo := meta.StrList(decl["to"])
		for _, rl := range declTo {
			if rl == "owner" {
				to.Owner = true
			} else {
				to.Roles = append(to.Roles, rl)
			}
		}
		if can := meta.AsMap(decl["can"]); can != nil { // roles that hold that permission (§1.1)
			cands := to.Roles
			if len(declTo) == 0 {
				cands = append(append([]string{}, to.Roles...), sortedRoleNames(reg)...)
			}
			to.Roles = r.e.rolesThatCan(reg, cands, str(can["model"]), str(can["action"]))
		}
		title = meta.LabelOf(decl["label"]).Text()
	}
	if !to.Owner && len(to.Roles) == 0 && len(to.Emails) == 0 {
		return dropped, nil
	}
	switch t := st["title"].(type) {
	case string:
		title = cel.Interpolate(t, r.env())
	case map[string]any:
		title = cel.Interpolate(meta.LabelOf(t).Text(), r.env())
	}
	if strings.TrimSpace(title) == "" {
		if title = r.f.Label.Text(); title == "" {
			title = r.f.Name
		}
	}
	ev := NoticeEvent{Kind: kind, Severity: severity, To: to, Title: title, Text: text}
	opt := noticeOpts{mail: channelMail(str(st["channel"]))}
	if v := str(st["record"]); v != "" { // the record the notice is about: its link and who may read it
		rec := meta.AsMap(r.vars[v])
		if mn, name := str(rec["_model"]), str(rec["name"]); mn != "" && name != "" {
			opt.record = &noticeRecord{model: mn, name: name}
			ev.Data = map[string]any{"model": mn, "name": name}
			ev.Link = "/app/#/r/" + mn + "/" + name
		}
	}
	if decl != nil {
		switch str(decl["link"]) {
		case "approvals":
			ev.Link = "/app/#/approvals"
		case "none":
			ev.Link = ""
		}
	}
	if src := str(st["outlet"]); src != "" {
		v, err := cel.Eval(src, r.env())
		if err != nil {
			return dropped, fmt.Errorf("outlet: %w", err)
		}
		ev.To.Outlet = str(cel.Unwrap(v))
	}
	if src := str(st["id"]); src != "" {
		v, err := cel.Eval(src, r.env())
		if err != nil {
			return dropped, fmt.Errorf("id: %w", err)
		}
		ev.ID = kind + ":" + fmt.Sprint(cel.Unwrap(v))
	} else {
		if r.noticeSeen == nil {
			r.noticeSeen = map[string]int{}
		}
		r.noticeSeen[index]++
		ev.ID = kind + ":" + r.runID + ":" + index
		if n := r.noticeSeen[index]; n > 1 { // a step under for_each runs once per item
			ev.ID += ":" + itoa(int64(n))
		}
	}
	return dropped, r.o.notice(ev, opt)
}

// channelMail is the mail default a step's channel sets (§1.2): email on, in_app off, others none.
func channelMail(ch string) *bool {
	on := ch == "email"
	if ch == "email" || ch == "in_app" {
		return &on
	}
	return nil
}

// noticeDecl is the notices: entry name of the run's module (nil when the flow is not a module's or names none).
func (r *flowRun) noticeDecl(name string) map[string]any {
	if name == "" {
		return nil
	}
	a := r.registry().App[r.f.App]
	if a == nil || a.Kind != meta.KindModule {
		return nil
	}
	for _, x := range meta.AsList(a.Raw["notices"]) {
		if n := meta.AsMap(x); n != nil && str(n["name"]) == name {
			return n
		}
	}
	return nil
}

func sortedRoleNames(reg *meta.Registry) []string {
	out := make([]string, 0, len(reg.Roles))
	for n := range reg.Roles {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// rolesThatCan keeps the roles of cands that hold action on model (a conditional grant counts).
func (e *Engine) rolesThatCan(reg *meta.Registry, cands []string, model, action string) []string {
	m := e.permModelIn(reg, model)
	out := []string{}
	for _, rl := range uniqueStrings(cands, "owner") {
		if m != nil && e.decide(Actor{Roles: []string{rl}}, m, action).maybe() {
			out = append(out, rl)
		}
	}
	return out
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

// NoticeKind is a kind this installation can raise, for Pengaturan › Notifikasi.
type NoticeKind struct {
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Severity string `json:"severity"`
	Mail     bool   `json:"mail"` // the default before a person's choice (§4.2)
}

// NoticeKinds lists the kinds this installation can raise: the built-in ones (§5), the notices: of installed modules
// (§1.1) and the implicit kinds of installed flows with a notify step to no declaration (§1.3). A step's channel
// sets its kind's mail default (email on, in_app off). Sorted by kind.
func (e *Engine) NoticeKinds() []NoticeKind {
	reg := e.Reg()
	byKind := map[string]*NoticeKind{
		"kernel.approval.requested": {Kind: "kernel.approval.requested", Label: "Permintaan persetujuan", Severity: "warn", Mail: true},
		"kernel.approval.decided":   {Kind: "kernel.approval.decided", Label: "Keputusan persetujuan", Severity: "info"},
	}
	for _, a := range reg.Apps {
		if a.Kind != meta.KindModule || !reg.IsInstalled(a.Name) {
			continue
		}
		for _, x := range meta.AsList(a.Raw["notices"]) {
			n := meta.AsMap(x)
			k := "app." + a.Name + "." + str(n["name"])
			if n == nil || !ValidNoticeKind(k) {
				continue
			}
			sev := str(n["severity"])
			if !NoticeSeverities[sev] {
				sev = "info"
			}
			byKind[k] = &NoticeKind{Kind: k, Label: meta.LabelOf(n["label"]).Text(), Severity: sev, Mail: sev != "info"}
		}
	}
	mailed := map[string]bool{}
	for _, name := range sortedKeysF(reg.Flows) {
		f := reg.Flows[name]
		if !reg.IsInstalled(f.App) {
			continue
		}
		r := &flowRun{f: f, reg: reg}
		implicit := r.noticeKind()
		walkNotify(f.Steps, func(st map[string]any) {
			k := implicit
			if d := r.noticeDecl(str(st["notice"])); d != nil {
				k = "app." + f.App + "." + str(d["name"])
			}
			if k == "" || !ValidNoticeKind(k) {
				return
			}
			nk := byKind[k]
			if nk == nil {
				label := f.Label.Text()
				if label == "" {
					label = f.Name
				}
				nk = &NoticeKind{Kind: k, Label: label, Severity: "info"}
				byKind[k] = nk
			}
			switch st["channel"] { // a step's channel sets its kind's default (§1.2): email on, in_app off
			case "email":
				nk.Mail, mailed[k] = true, true
			case "in_app":
				if !mailed[k] {
					nk.Mail = false
				}
			}
		})
	}
	out := make([]NoticeKind, 0, len(byKind))
	for _, k := range byKind {
		out = append(out, *k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
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

// walkNotify calls fn for every notify step of steps, nested steps included.
func walkNotify(steps []map[string]any, fn func(st map[string]any)) {
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["notify"] != nil {
				fn(x)
			}
			for _, k := range []string{"do", "then", "else"} {
				walk(x[k])
			}
		case []any:
			for _, s := range x {
				walk(s)
			}
		case []map[string]any:
			for _, s := range x {
				walk(s)
			}
		}
	}
	walk(steps)
}

// ChangesetNotice is what the change-set service tells the engine about a proposal (kernel.changeset.proposed).
type ChangesetNotice struct {
	ID, Title, Risk, Source, Author string
	Approvers                       []string // roles that may approve it, besides the owner
}

// NoticeChangesetProposed raises kernel.changeset.proposed (contract 0066 as built): a change set waits for someone
// who decides, so the owner and the roles that may approve it hear about it, never the person who proposed it. It
// runs after the proposal is saved and never fails it: a failure is logged.
func (e *Engine) NoticeChangesetProposed(ctx context.Context, org string, c ChangesetNotice) {
	ctx = pg.WithOrg(ctx, org)
	sev := "warn"
	switch c.Risk {
	case "high":
		sev = "high"
	case "low":
		sev = "info"
	}
	who := "Perubahan baru"
	if c.Source == "ai" {
		who = "Raka mengusulkan perubahan"
	}
	roles := []string{}
	for _, r := range c.Approvers {
		if r != "owner" {
			roles = append(roles, r)
		}
	}
	err := e.run(ctx, Actor{Org: org, ID: "system", System: true}, func(o *op) error {
		return o.notice(NoticeEvent{
			ID: "kernel.changeset.proposed:" + c.ID, Kind: "kernel.changeset.proposed", Severity: sev,
			To:    NoticeTo{Owner: true, Roles: roles},
			Title: "Perubahan sistem menunggu keputusanmu", Text: who + ": " + c.Title,
			Link: "/app/#/approvals?tab=sistem", Data: map[string]any{"changeset": c.ID, "risk": c.Risk, "source": c.Source},
		}, noticeOpts{except: []string{c.Author}})
	})
	if err != nil {
		e.Log("notice: kernel.changeset.proposed %s: %v", c.ID, err)
	}
}

// exceptionWords names an exception kind for the head-office notice (contract 0058 §10).
var exceptionWords = map[string]string{"edge_price_drift": "harga kasir beda", "negative_stock": "stok minus",
	"device_number_gap": "nomor perangkat loncat", "rule_overridden": "aturan dilewati"}

// NoticeExceptionDigest raises the hourly kernel.exceptions.new (contract 0066 as built): the exceptions raised and
// still open since the last digest, as one notice to the owner and head office. It tells nobody when there are none.
// The event id carries the hour, and the window starts at the newest digest (at most a day back), so a rerun inside
// the hour or a job retried after it committed cannot tell twice.
func (e *Engine) NoticeExceptionDigest(ctx context.Context, org string, now time.Time) error {
	ctx = pg.WithOrg(ctx, org)
	const kind = "kernel.exceptions.new"
	// whole seconds: a notice's time is stored to the second, so the window of the next digest starts exactly where
	// this one ends, and an exception raised in the second that is still running is told next time, not twice
	now = now.UTC().Truncate(time.Second)
	return e.run(ctx, Actor{Org: org, ID: "system", System: true}, func(o *op) error {
		since, err := store.LastNoticeAt(o.ctx, o.tx, org, kind)
		if err != nil {
			return err
		}
		if floor := now.Add(-24 * time.Hour); since.Before(floor) {
			since = floor
		}
		counts, err := store.OpenExceptionsBetween(o.ctx, o.tx, org, since, now)
		if err != nil || len(counts) == 0 {
			return err
		}
		total, parts, by := 0, []string{}, map[string]any{}
		for _, c := range counts {
			total += c.Count
			w := exceptionWords[c.Kind]
			if w == "" {
				w = "lainnya"
			}
			parts = append(parts, fmt.Sprintf("%d %s", c.Count, w))
			by[c.Kind] = c.Count
		}
		return o.notice(NoticeEvent{
			ID: fmt.Sprintf("%s:%d", kind, now.Truncate(time.Hour).Unix()), Kind: kind, Severity: "warn",
			To:    NoticeTo{Owner: true, Roles: []string{"head_office"}},
			Title: fmt.Sprintf("%d pengecualian baru", total), Text: strings.Join(parts, ", "),
			Link: "/app/", Data: map[string]any{"count": total, "kinds": by}, At: now.Format(time.RFC3339),
		}, noticeOpts{})
	})
}

// ExceptionDigestDue reports whether the org has unresolved exceptions the next digest would tell. The scheduler asks
// first so a quiet org never gets a queue job (no job event, no row) each hour.
func (e *Engine) ExceptionDigestDue(ctx context.Context, org string, now time.Time) bool {
	ctx = pg.WithOrg(ctx, org)
	now = now.UTC().Truncate(time.Second)
	due := false
	err := e.run(ctx, Actor{Org: org, ID: "system", System: true}, func(o *op) error {
		since, err := store.LastNoticeAt(o.ctx, o.tx, org, "kernel.exceptions.new")
		if err != nil {
			return err
		}
		if floor := now.Add(-24 * time.Hour); since.Before(floor) {
			since = floor
		}
		counts, err := store.OpenExceptionsBetween(o.ctx, o.tx, org, since, now)
		due = err == nil && len(counts) > 0
		return err
	})
	return err == nil && due
}
