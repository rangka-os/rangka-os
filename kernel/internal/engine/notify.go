package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/mail"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// NotifyEmailJob is the job kind that mails one notify step's text to one address (system.rk notify.email driver
// smtp). Its args: to, subject, text, flow, actor {id}. The server registers the handler.
const NotifyEmailJob = "notify_email"

// notifyEmail queues the mail of a notify step when notify.email selects the smtp driver: one job per address, in
// the flow's transaction, so the step never waits on the mail server and a rolled-back run sends nothing. Who gets
// mail: members of the organisation only (contract 0066 §1.3, §2: never an address outside it), found by role
// (notify: owner), by e-mail address, or by their user record (a variable holding the record). Targets that match no
// member are counted in Dropped. Without the smtp driver, or without a queue, n is left on the log driver.
func (r *flowRun) notifyEmail(n *Notification) error {
	n.Driver = "log"
	if r.e.Queue == nil {
		return nil
	}
	if d, _ := mail.NotifyDriver(meta.AsMap(r.registry().SystemConfig("notify.email"))); d != "smtp" {
		return nil
	}
	o, a := r.o, r.a
	addrs, dropped, err := memberEmails(o.ctx, o.tx, a.Org, n.Recipients)
	if err != nil {
		return err
	}
	n.Driver, n.Dropped = "smtp", dropped
	subject := r.f.Label.Text()
	if subject == "" {
		subject = r.f.Name
	}
	for _, to := range addrs {
		id, err := r.e.Queue.Enqueue(o.ctx, o.tx, NotifyEmailJob, a.Org, map[string]any{"to": to, "subject": subject, "text": n.Text,
			"flow": r.f.Name, "actor": map[string]any{"id": a.ID}}, EnqueueOpts{})
		if err != nil {
			return fmt.Errorf("notify: queue mail: %w", err)
		}
		n.Jobs = append(n.Jobs, id)
	}
	return nil
}

// memberEmails resolves notify targets to the e-mail addresses of members of org: a target is a role (every member
// holding it), an address (that member) or a user record name (rk_members.record). It returns the addresses, sorted
// and without repeats, and how many targets matched nobody.
func memberEmails(ctx context.Context, q pg.Querier, org string, targets []string) ([]string, int, error) {
	if len(targets) == 0 {
		return nil, 0, nil
	}
	res, err := q.Query(ctx, `select lower(u.email), m.role, coalesce(m.record, '') from rk_members m join rk_users u on u.id = m.user_id
		where m.org_id = $1`, org)
	if err != nil {
		return nil, 0, err
	}
	seen := map[string]bool{}
	var out []string
	dropped := 0
	for _, t := range targets {
		t = strings.TrimSpace(t)
		lt := strings.ToLower(t)
		hit := false
		for _, row := range res.Rows {
			email, role, rec := row[0].(string), row[1].(string), row[2].(string)
			if role == t || email == lt || (rec != "" && rec == t) {
				hit = true
				if !seen[email] {
					seen[email] = true
					out = append(out, email)
				}
			}
		}
		if !hit {
			dropped++
		}
	}
	sort.Strings(out)
	return out, dropped, nil
}

// notifyAudit writes the notify.<channel> audit entry of a step.
func (r *flowRun) notifyAudit(n Notification) error {
	return store.Audit(r.o.ctx, r.o.tx, r.a.Org, r.a.ID, "notify."+n.Channel, "flow/"+r.f.Name, n.Text, n)
}
