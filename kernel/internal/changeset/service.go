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
	// Base is store.FileHash of the file text the op was computed from (contract 0014 §5). When set, the op
	// only runs while the file still has that text: otherwise validate, preview and apply answer stale_base.
	Base string `json:"base,omitempty"`
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
	// Modules is the difference in installed units a config.rk change makes (contract 0032 §3); nil otherwise.
	Modules *ModulesDiff `json:"modules,omitempty"`
	// Requires lists the auth fields still unset for the connectors this change installs (contract 0017 §4). The
	// change can be applied; each connector stays needs_auth until a person sets them. Never a value.
	Requires []Requirement `json:"requires,omitempty"`
}

// Requirement is one auth field a newly installed connector still needs.
type Requirement struct {
	Connector string `json:"connector"`
	Field     string `json:"field"`
	Label     any    `json:"label,omitempty"`
	Secret    bool   `json:"secret"`
}

// ModulesDiff lists the units a change installs (the whole closure) and removes, the rows each removed model holds
// in all organisations, and the units that leave config but stay installed because another unit requires them.
type ModulesDiff struct {
	Added   []string         `json:"added"`
	Removed []string         `json:"removed"`
	Rows    map[string]int64 `json:"rows"`
	Kept    []KeptUnit       `json:"kept"`
}

// KeptUnit is a unit config no longer names that stays installed.
type KeptUnit struct {
	Name       string   `json:"name"`
	RequiredBy []string `json:"required_by"`
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
	Source     string      `json:"source,omitempty"` // "ai" from the embedded assistant (contract 0020), "catalogue" from the catalogue (0036)
	Org        string      `json:"org,omitempty"`
	Ops        []Op        `json:"ops"`
	Layers     []int       `json:"layers"`
	Risk       string      `json:"risk"`
	Status     string      `json:"status"`
	Validation *Validation `json:"validation,omitempty"`
	Preview    *Preview    `json:"preview,omitempty"`
	History    []Event     `json:"history"`
	// ApprovableBy lists the roles that may approve this change set; computed on every return, not stored.
	ApprovableBy []string `json:"approvable_by"`
	CreatedAt    string   `json:"created_at,omitempty"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
	previous     map[string]*string
}

// Actor is who acts on a change set.
type Actor struct {
	ID    string
	Org   string
	Roles []string
	Human bool   // a person with a browser session (approval needs one)
	Kind  string // user, ai, console, cli (author kind for proposals)
	// Assistant is the embedded assistant acting for the person (auth Via ai): it proposes, never applies
	Assistant bool
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
	ModulesDir                      string // default: modules next to AppsDir (contract 0001)
	Schemas                         *schema.Set
	Reg                             func() *meta.Registry
	Reload                          func(ctx context.Context) error // re-read dirs, migrate, swap the registry
	Now                             func() time.Time
	// Requires lists the unset auth fields of the connectors a change installs (contract 0017 §4); nil = none.
	// before is the live registry, after the change's copy (which has no connector files of its own).
	Requires func(ctx context.Context, org string, before, after *meta.Registry) []Requirement
	mu       sync.Mutex
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

type roots struct{ apps, site, connectors, modules string }

func (s *Service) live() roots { return roots{s.AppsDir, s.SiteDir, s.ConnectorsDir, s.modulesDir()} }

// modulesDir is ModulesDir, else the modules directory next to the apps directory (created by the first
// change set that adds a module).
func (s *Service) modulesDir() string {
	if s.ModulesDir != "" {
		return s.ModulesDir
	}
	return filepath.Join(filepath.Dir(filepath.Clean(s.AppsDir)), "modules")
}

// file maps a change-set path to a file under the roots.
func (r roots) file(p string) (string, error) {
	switch {
	case strings.HasPrefix(p, "apps/"):
		return filepath.Join(r.apps, filepath.FromSlash(strings.TrimPrefix(p, "apps/"))), nil
	case strings.HasPrefix(p, "modules/"):
		if r.modules == "" {
			return "", fmt.Errorf("no modules directory configured for %s", p)
		}
		return filepath.Join(r.modules, filepath.FromSlash(strings.TrimPrefix(p, "modules/"))), nil
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
	root, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	return copyTree(root, dst, map[string]bool{})
}

// copyTree copies the directory dir into dst, following symlinks to files and directories. active holds the
// resolved directories on the current path, so a link back to an ancestor is skipped instead of looping.
func copyTree(dir, dst string, active map[string]bool) error {
	if active[dir] {
		return nil
	}
	active[dir] = true
	defer delete(active, dir)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		target := filepath.Join(dst, e.Name())
		st, err := os.Stat(p) // follows a symlink
		if err != nil {
			return err
		}
		if st.IsDir() {
			if e.Name() == ".git" || e.Name() == "node_modules" {
				continue
			}
			real, err := filepath.EvalSymlinks(p)
			if err != nil {
				return err
			}
			if err := copyTree(real, target, active); err != nil {
				return err
			}
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// result computes the new text of every op's file under r (nil = deleted). An op whose base no longer matches
// its file on disk answers stale_base (*Error, 409).
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
		if op.Base != "" && (cur == nil || store.FileHash(*cur) != op.Base) {
			return nil, staleBase(op.Path)
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
			url := headerURL(doc.Kind(), data)
			text := rk.Dump(data, doc.Root, url)
			out[op.Path] = &text
		default:
			return nil, fmt.Errorf("op %d: unknown op %q", i, op.Op)
		}
	}
	return out, nil
}

// staleBase is the 409 an op answers when its file changed after the op was computed: applying it would
// silently undo that change (a lost update).
func staleBase(path string) *Error {
	return &Error{409, "stale_base", "Berkas " + path + " sudah berubah sejak perubahan ini dibuat; buat ulang dari kanvas.", map[string]any{"path": path}}
}

// headerURL is the schema URL a re-dumped document declares: 0.5, except an app without modules, which is a
// 0.4 app (contract 0001) and keeps the 0.4 app.json it is checked against.
func headerURL(kind string, data any) string {
	if kind == "" {
		return ""
	}
	if m, ok := data.(map[string]any); kind == "app" && ok {
		if _, has := m["modules"]; !has {
			return schema.Legacy + "app.json"
		}
	}
	return schema.Base + kind + ".json"
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
	r.modules = filepath.Join(dir, "modules") // next to apps, where the validator looks by default
	if st, err := os.Stat(s.modulesDir()); err == nil && st.IsDir() {
		if err := copyDir(s.modulesDir(), r.modules); err != nil {
			cleanup()
			return roots{}, nil, nil, err
		}
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
		if e, ok := err.(*Error); ok {
			return roots{}, nil, nil, e
		}
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
		_, err := q.Query(ctx, `insert into rk_changesets (id, org_id, title, reason, author, ops, layers, risk, status, validation, preview, history, previous, source)
			values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, cs.ID, cs.Org, cs.Title, cs.Reason, pg.JSON{V: cs.Author}, pg.JSON{V: cs.Ops},
			pg.JSON{V: cs.Layers}, cs.Risk, cs.Status, val, pre, pg.JSON{V: cs.History}, prev, cs.Source)
		return err
	}
	_, err := q.Query(ctx, `update rk_changesets set status = $2, validation = $3, preview = $4, history = $5, previous = coalesce($6, previous), updated_at = now()
		where id = $1`, cs.ID, cs.Status, val, pre, pg.JSON{V: cs.History}, prev)
	return err
}

func fromRow(m map[string]any) *ChangeSet {
	cs := &ChangeSet{ID: str(m["id"]), Org: str(m["org_id"]), Title: str(m["title"]), Reason: str(m["reason"]), Risk: str(m["risk"]), Status: str(m["status"]), Source: str(m["source"])}
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
	cs := fromRow(res.Maps()[0])
	cs.ApprovableBy = s.ApprovableBy(cs)
	return cs, nil
}

// List returns change sets, newest first, optionally by status.
func (s *Service) List(ctx context.Context, org, status string, limit int) ([]*ChangeSet, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	res, err := s.DB.Query(ctx, `select * from rk_changesets where (org_id = $1 or org_id = '') and ($2 = '' or status = $2) order by created_at desc limit $3`, org, status, limit)
	if err != nil {
		return nil, err
	}
	out := []*ChangeSet{}
	for _, m := range res.Maps() {
		cs := fromRow(m)
		cs.ApprovableBy = s.ApprovableBy(cs)
		out = append(out, cs)
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
