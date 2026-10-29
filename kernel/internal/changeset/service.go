// Package changeset implements change sets (changeset.json): the only way Rangka AI and the Console change
// a running installation. A change set is proposed, validated on a temporary copy of the metadata,
// previewed (DDL that would run, records affected), approved by a person, applied (files written, database
// migrated, metadata reloaded live, audited, announced over SSE) and can be reverted.
package changeset

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/rk"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Op is one file operation.
type Op struct {
	Op      string           `json:"op"`
	Path    string           `json:"path"`
	Content string           `json:"content,omitempty"`
	Patch   []map[string]any `json:"patch,omitempty"`
}

// Author proposed the change set.
type Author struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Model string `json:"model,omitempty"`
}

// Event is one status change.
type Event struct {
	Status string `json:"status"`
	At     string `json:"at"`
	By     string `json:"by"`
	Note   string `json:"note,omitempty"`
}

// Validation holds validator output for the changed metadata.
type Validation struct {
	Errors   []meta.Item `json:"errors"`
	Warnings []meta.Item `json:"warnings"`
}

// Preview is a dry run on a copy.
type Preview struct {
	Files      []FileDiff       `json:"files"`
	Migrations []string         `json:"migrations"`
	Affected   map[string]int64 `json:"affected"`
	Reversible bool             `json:"reversible"`
	Notes      []string         `json:"notes,omitempty"`
}

// FileDiff is a file's full text before and after (null when absent), for diffs.
type FileDiff struct {
	Path   string  `json:"path"`
	Before *string `json:"before"`
	After  *string `json:"after"`
}

// ChangeSet follows changeset.json.
type ChangeSet struct {
	ID         string      `json:"id"`
	Title      string      `json:"title"`
	Reason     string      `json:"reason,omitempty"`
	Author     Author      `json:"author"`
	Org        string      `json:"org,omitempty"`
	Ops        []Op        `json:"ops"`
	Layers     []int       `json:"layers"`
	Risk       string      `json:"risk"`
	Status     string      `json:"status"`
	Validation *Validation `json:"validation,omitempty"`
	Preview    *Preview    `json:"preview,omitempty"`
	History    []Event     `json:"history"`
	CreatedAt  string      `json:"created_at,omitempty"`
	UpdatedAt  string      `json:"updated_at,omitempty"`
	previous   map[string]*string
}

// Actor is who acts on a change set.
type Actor struct {
	ID    string
	Org   string
	Roles []string
	Human bool   // a person with a browser session (approval needs one)
	Kind  string // user, ai, console, cli (author kind for proposals)
}

// Error carries an HTTP status.
type Error struct {
	Status int
	Code   string
	Msg    string
	Detail any
}

func (e *Error) Error() string { return e.Msg }

// Service runs change sets against the installation's directories.
type Service struct {
	DB                              *pg.Pool
	AppsDir, SiteDir, ConnectorsDir string
	Schemas                         *schema.Set
	Reg                             func() *meta.Registry
	Reload                          func(ctx context.Context) error // re-read dirs, migrate, swap the registry
	Now                             func() time.Time
	mu                              sync.Mutex
}

func (s *Service) now() string {
	t := time.Now()
	if s.Now != nil {
		t = s.Now()
	}
	return t.UTC().Format(time.RFC3339)
}

func newID() string {
	const al = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 10)
	rand.Read(b)
	for i := range b {
		b[i] = al[int(b[i])%len(al)]
	}
	return "cs_" + string(b)
}

// ---------- paths and files

type roots struct{ apps, site, connectors string }

func (s *Service) live() roots { return roots{s.AppsDir, s.SiteDir, s.ConnectorsDir} }

// file maps a change-set path to a file under the roots.
func (r roots) file(p string) (string, error) {
	switch {
	case strings.HasPrefix(p, "apps/"):
		return filepath.Join(r.apps, filepath.FromSlash(strings.TrimPrefix(p, "apps/"))), nil
	case strings.HasPrefix(p, "connectors/"):
		if r.connectors == "" {
			return "", fmt.Errorf("no connectors directory configured for %s", p)
		}
		stem := strings.TrimSuffix(strings.TrimPrefix(p, "connectors/"), ".rk")
		if alt := filepath.Join(r.connectors, stem, "connector.rk"); fileExists(alt) {
			return alt, nil
		}
		return filepath.Join(r.connectors, stem+".rk"), nil
	}
	if r.site == "" {
		return "", fmt.Errorf("no site directory configured for %s", p)
	}
	return filepath.Join(r.site, filepath.FromSlash(p)), nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func readFile(p string) (*string, error) {
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s := string(b)
	return &s, nil
}

// writeFile writes atomically (temp file + rename); nil content deletes the file.
func writeFile(p string, content *string) error {
	if content == nil {
		err := os.Remove(p)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".rk-tmp"
	if err := os.WriteFile(tmp, []byte(*content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func copyDir(src, dst string) error {
	if src == "" {
		return nil
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

// result computes the new text of every op's file under r (nil = deleted).
func (r roots) result(ops []Op) (map[string]*string, error) {
	out := map[string]*string{}
	for i, op := range ops {
		f, err := r.file(op.Path)
		if err != nil {
			return nil, err
		}
		cur, err := readFile(f)
		if err != nil {
			return nil, err
		}
		if prev, ok := out[op.Path]; ok {
			cur = prev
		}
		switch op.Op {
		case "create":
			if cur != nil {
				return nil, fmt.Errorf("op %d: %s already exists; use replace or patch", i, op.Path)
			}
			c := op.Content
			out[op.Path] = &c
		case "replace":
			if cur == nil {
				return nil, fmt.Errorf("op %d: %s does not exist; use create", i, op.Path)
			}
			c := op.Content
			out[op.Path] = &c
		case "delete":
			if cur == nil {
				return nil, fmt.Errorf("op %d: %s does not exist", i, op.Path)
			}
			out[op.Path] = nil
		case "patch":
			if cur == nil {
				return nil, fmt.Errorf("op %d: %s does not exist; use create", i, op.Path)
			}
			doc, err := rk.LoadBytes(f, []byte(*cur))
			if err != nil {
				return nil, fmt.Errorf("op %d: %s does not parse: %v", i, op.Path, err)
			}
			data, err := ApplyPatch(doc.Data, op.Patch)
			if err != nil {
				return nil, fmt.Errorf("op %d: %v", i, err)
			}
			kind := doc.Kind()
			url := ""
			if kind != "" {
				url = schema.Base + kind + ".json"
			}
			text := rk.Dump(data, doc.Root, url)
			out[op.Path] = &text
		default:
			return nil, fmt.Errorf("op %d: unknown op %q", i, op.Op)
		}
	}
	return out, nil
}

// sandbox copies the installation into a temp dir and applies the ops there.
func (s *Service) sandbox(ops []Op) (roots, func(), map[string]*string, error) {
	dir, err := os.MkdirTemp("", "rangka-cs-*")
	if err != nil {
		return roots{}, nil, nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	r := roots{apps: filepath.Join(dir, "apps")}
	if err := copyDir(s.AppsDir, r.apps); err != nil {
		cleanup()
		return roots{}, nil, nil, err
	}
	if s.SiteDir != "" {
		r.site = filepath.Join(dir, "site", filepath.Base(s.SiteDir))
		if err := copyDir(s.SiteDir, r.site); err != nil {
			cleanup()
			return roots{}, nil, nil, err
		}
	}
	if s.ConnectorsDir != "" {
		r.connectors = filepath.Join(dir, "connectors")
		if err := copyDir(s.ConnectorsDir, r.connectors); err != nil {
			cleanup()
			return roots{}, nil, nil, err
		}
	}
	files, err := r.result(ops)
	if err != nil {
		cleanup()
		return roots{}, nil, nil, &Error{422, "bad_ops", err.Error(), nil}
	}
	for p, c := range files {
		f, _ := r.file(p)
		if err := writeFile(f, c); err != nil {
			cleanup()
			return roots{}, nil, nil, err
		}
	}
	return r, cleanup, files, nil
}

// ---------- persistence

func (s *Service) save(ctx context.Context, q pg.Querier, cs *ChangeSet, insert bool) error {
	var prev any
	if cs.previous != nil {
		prev = pg.JSON{V: cs.previous}
	}
	var val, pre any
	if cs.Validation != nil {
		val = pg.JSON{V: cs.Validation}
	}
	if cs.Preview != nil {
		pre = pg.JSON{V: cs.Preview}
	}
	if insert {
		_, err := q.Query(ctx, `insert into rk_changesets (id, org_id, title, reason, author, ops, layers, risk, status, validation, preview, history, previous)
			values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, cs.ID, cs.Org, cs.Title, cs.Reason, pg.JSON{V: cs.Author}, pg.JSON{V: cs.Ops},
			pg.JSON{V: cs.Layers}, cs.Risk, cs.Status, val, pre, pg.JSON{V: cs.History}, prev)
		return err
	}
	_, err := q.Query(ctx, `update rk_changesets set status = $2, validation = $3, preview = $4, history = $5, previous = coalesce($6, previous), updated_at = now()
		where id = $1`, cs.ID, cs.Status, val, pre, pg.JSON{V: cs.History}, prev)
	return err
}

func fromRow(m map[string]any) *ChangeSet {
	cs := &ChangeSet{ID: str(m["id"]), Org: str(m["org_id"]), Title: str(m["title"]), Reason: str(m["reason"]), Risk: str(m["risk"]), Status: str(m["status"])}
	remarshal(m["author"], &cs.Author)
	remarshal(m["ops"], &cs.Ops)
	remarshal(m["layers"], &cs.Layers)
	remarshal(m["history"], &cs.History)
	if m["validation"] != nil {
		cs.Validation = &Validation{}
		remarshal(m["validation"], cs.Validation)
	}
	if m["preview"] != nil {
		cs.Preview = &Preview{}
		remarshal(m["preview"], cs.Preview)
	}
	if m["previous"] != nil {
		remarshal(m["previous"], &cs.previous)
	}
	if cs.Layers == nil {
		cs.Layers = []int{}
	}
	for k, dst := range map[string]*string{"created_at": &cs.CreatedAt, "updated_at": &cs.UpdatedAt} {
		if t, ok := m[k].(time.Time); ok {
			*dst = t.UTC().Format(time.RFC3339)
		}
	}
	return cs
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// Get loads a change set.
func (s *Service) Get(ctx context.Context, org, id string) (*ChangeSet, error) {
	res, err := s.DB.Query(ctx, "select * from rk_changesets where id = $1 and (org_id = $2 or org_id = '')", id, org)
	if err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, &Error{404, "not_found", "change set " + id + " not found", nil}
	}
	return fromRow(res.Maps()[0]), nil
}

// List returns change sets, newest first, optionally by status.
func (s *Service) List(ctx context.Context, org, status string, limit int) ([]*ChangeSet, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	res, err := s.DB.Query(ctx, `select * from rk_changesets where (org_id = $1 or org_id = '') and ($2 = '' or status = $2) order by created_at desc limit $3`, org, status, limit)
	if err != nil {
		return nil, err
	}
	out := []*ChangeSet{}
	for _, m := range res.Maps() {
		out = append(out, fromRow(m))
	}
	return out, nil
}

func (s *Service) transition(ctx context.Context, cs *ChangeSet, a Actor, status, note string, insert bool) error {
	cs.Status = status
	cs.History = append(cs.History, Event{Status: status, At: s.now(), By: a.ID, Note: note})
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := s.save(ctx, tx, cs, insert); err != nil {
		return err
	}
	summary := cs.Title
	if note != "" {
		summary += " — " + note
	}
	if err := store.Audit(ctx, tx, cs.Org, a.ID, "changeset."+status, cs.ID, summary, map[string]any{"risk": cs.Risk, "layers": cs.Layers}); err != nil {
		return err
	}
	if err := store.Notify(ctx, tx, store.Event{Type: "changeset", Org: cs.Org, ID: cs.ID, Status: status}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if fresh, err := s.Get(ctx, cs.Org, cs.ID); err == nil {
		cs.CreatedAt, cs.UpdatedAt = fresh.CreatedAt, fresh.UpdatedAt
	}
	return nil
}
