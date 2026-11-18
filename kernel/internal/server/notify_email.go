package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
	"github.com/rangka-os/rangka-os/kernel/internal/mail"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// runNotifyEmailJob sends the mail a notify step queued (engine.NotifyEmailJob) with mail.Send, the same SMTP client
// mail connectors use (contract 0068), and the notify.email settings and secrets of now: a password rotated after the
// step is used, and nothing secret sits in the job's args. A send writes the audit entry notify.email.sent; a failure
// is in the job's errors on every attempt and, once the job gives up (its last attempt, or an error a retry cannot fix:
// a 5xx reply, failed authentication, a bad setting), in notify.email.failed. When the site switched notify.email back
// to log meanwhile, the job only logs.
func (s *Server) runNotifyEmailJob(ctx context.Context, j *jobs.Job) (any, error) {
	ctx = pg.WithOrg(ctx, j.Org)
	to, flow := meta.Str(j.Args, "to"), meta.Str(j.Args, "flow")
	actor := meta.Str(meta.AsMap(j.Args["actor"]), "id")
	if actor == "" {
		actor = "system"
	}
	subject, text := meta.Str(j.Args, "subject"), meta.Str(j.Args, "text")
	cfg := meta.AsMap(s.Engine.Reg().SystemConfig("notify.email"))
	if d, why := mail.NotifyDriver(cfg); d != "smtp" {
		s.Cfg.Logf("notify[email/log] %s: %s (%s)", to, text, why)
		return map[string]any{"driver": "log", "to": to}, nil
	}
	err := s.sendNotifyEmail(ctx, cfg, to, subject, text)
	if err != nil {
		permanent := mail.Permanent(err)
		if permanent || j.Attempt >= j.MaxAttempts {
			if aerr := store.Audit(ctx, s.DB, j.Org, actor, "notify.email.failed", "flow/"+flow, fmt.Sprintf("mail to %s not sent: %v", to, err),
				map[string]any{"job": j.ID, "to": to, "attempts": j.Attempt, "error": err.Error()}); aerr != nil {
				s.Cfg.Logf("notify: audit: %v", aerr)
			}
		}
		if permanent {
			return nil, &jobs.Permanent{Err: err}
		}
		return nil, err
	}
	if err := store.Audit(ctx, s.DB, j.Org, actor, "notify.email.sent", "flow/"+flow, "mail to "+to+": "+subject,
		map[string]any{"job": j.ID, "to": to, "attempt": j.Attempt}); err != nil {
		s.Cfg.Logf("notify: audit: %v", err)
	}
	return map[string]any{"driver": "smtp", "to": to}, nil
}

func (s *Server) sendNotifyEmail(ctx context.Context, cfg map[string]any, to, subject, text string) error {
	srv, cred, from, err := mail.NotifyConfig(cfg, func(v string) (string, bool) { return s.API.Secrets.Resolve(ctx, v, "notify.email") })
	if err != nil {
		return err
	}
	rcpt, err := mail.ParseAddresses(to)
	if err != nil || len(rcpt) != 1 {
		return fmt.Errorf("%w: recipient %q", mail.ErrConfig, to)
	}
	now := time.Now()
	if s.Cfg.Now != nil {
		now = s.Cfg.Now()
	}
	id := make([]byte, 12)
	rand.Read(id)
	domain := from.Address[strings.LastIndexByte(from.Address, '@')+1:]
	raw, err := mail.Build(&mail.Outgoing{From: from, To: rcpt, Subject: subject, Text: text, Date: now,
		MessageID: hex.EncodeToString(id) + "@" + domain})
	if err != nil {
		return fmt.Errorf("%w: %v", mail.ErrConfig, err)
	}
	// the platform's egress policy holds for the SMTP host as for connectors; MailTLS is a test's trusted root
	d := &net.Dialer{Timeout: 30 * time.Second, Control: s.mailControl}
	opts := mail.Options{Dial: d.DialContext}
	if s.Connectors != nil {
		opts.TLS = s.Connectors.MailTLS
	}
	return mail.Send(ctx, srv, cred, from.Address, []string{rcpt[0].Address}, raw, opts)
}
