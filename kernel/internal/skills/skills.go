// Package skills loads and checks Rangka skills (contract 0035): Agent Skills folders, a SKILL.md with YAML
// frontmatter (name, description, optional metadata) and optional references/. Platform skills are embedded from
// the repository's skills/ (make sync-schemas copies them); apps and modules ship theirs in <unit>/skills/.
package skills

import (
	"archive/zip"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

//go:embed all:platform
var platformFS embed.FS

// Skill is one loaded skill.
type Skill struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Source      string         `json:"source"` // rangka-os, or app:<name> / module:<name>
	Body        string         `json:"-"`
	Files       []string       `json:"files,omitempty"` // other files, relative to the skill folder, sorted
	dir         string         // folder name
	fsys        fs.FS          // rooted at the skill folder
}

// Library is every skill the kernel can serve, by name. The first skill with a name wins; Problems lists duplicates.
type Library struct {
	Skills   []*Skill
	byName   map[string]*Skill
	Problems []Problem
}

// Problem is one finding of Check or of loading.
type Problem struct {
	Skill, File, Code, Message string
}

func (p Problem) String() string {
	return fmt.Sprintf("%s: %s %s: %s", p.Skill, p.File, p.Code, p.Message)
}

// Get returns a skill by name, or nil.
func (l *Library) Get(name string) *Skill {
	if l == nil {
		return nil
	}
	return l.byName[name]
}

// ReadFile returns one file of a skill (SKILL.md or a path from Files).
func (s *Skill) ReadFile(rel string) (string, error) {
	clean := path.Clean(strings.TrimPrefix(rel, "./"))
	if clean == "." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return "", fmt.Errorf("bad path %q", rel)
	}
	b, err := fs.ReadFile(s.fsys, clean)
	if err != nil {
		return "", fmt.Errorf("%s has no file %s", s.Name, clean)
	}
	return string(b), nil
}

// Platform returns the embedded platform skills.
func Platform() []*Skill {
	sub, _ := fs.Sub(platformFS, "platform")
	out, _ := loadDir(sub, "rangka-os")
	return out
}

// Load builds the library: the embedded platform skills, then <apps>/<app>/skills/<skill>/ and
// <modules>/<module>/skills/<skill>/, units in name order. Either directory may be "".
func Load(appsDir, modulesDir string) *Library { return LoadAll(appsDir, modulesDir) }

// Folder is a skills folder outside the units, such as rangka-connectors/skills.
type Folder struct{ Dir, Source string }

// LoadAll is Load plus extra skills folders (missing ones are skipped), loaded last.
func LoadAll(appsDir, modulesDir string, extra ...Folder) *Library {
	l := &Library{byName: map[string]*Skill{}}
	add := func(list []*Skill, probs []Problem) {
		l.Problems = append(l.Problems, probs...)
		for _, s := range list {
			if l.byName[s.Name] != nil {
				l.Problems = append(l.Problems, Problem{s.Name, "SKILL.md", "skill_duplicate", "another skill (" + l.byName[s.Name].Source + ") has this name"})
				continue
			}
			l.byName[s.Name] = s
			l.Skills = append(l.Skills, s)
		}
	}
	add(Platform(), nil)
	for _, u := range []struct{ dir, kind string }{{appsDir, "app"}, {modulesDir, "module"}} {
		dir, kind := u.dir, u.kind
		if dir == "" {
			continue
		}
		units, _ := os.ReadDir(dir)
		for _, u := range units {
			sd := filepath.Join(dir, u.Name(), "skills")
			if st, err := os.Stat(sd); err != nil || !st.IsDir() {
				continue
			}
			add(loadDir(os.DirFS(sd), kind+":"+u.Name()))
		}
	}
	for _, f := range extra {
		if st, err := os.Stat(f.Dir); err == nil && st.IsDir() {
			add(loadDir(os.DirFS(f.Dir), f.Source))
		}
	}
	return l
}

// AllFiles returns SKILL.md and every other file of the skill, as paths relative to its folder.
func (s *Skill) AllFiles() []string { return append([]string{"SKILL.md"}, s.Files...) }

// CopyTo writes the skill into dir/<name>/, replacing a folder of that name (it is ours: rangka-*).
func (s *Skill) CopyTo(dir string) error {
	if !nameRe.MatchString(s.Name) {
		return fmt.Errorf("refusing to install %q: not a rangka- skill name", s.Name)
	}
	dst := filepath.Join(dir, s.Name)
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	for _, f := range s.AllFiles() {
		b, err := fs.ReadFile(s.fsys, f)
		if err != nil {
			return err
		}
		p := filepath.Join(dst, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Pack writes the skills as a zip with <name>/SKILL.md at the top, so `unzip -d .claude/skills` installs them.
func Pack(w io.Writer, list []*Skill) error {
	zw := zip.NewWriter(w)
	for _, s := range list {
		for _, f := range s.AllFiles() {
			b, err := fs.ReadFile(s.fsys, f)
			if err != nil {
				return err
			}
			h := &zip.FileHeader{Name: s.Name + "/" + f, Method: zip.Deflate}
			h.SetMode(0o644)
			fw, err := zw.CreateHeader(h)
			if err != nil {
				return err
			}
			if _, err := fw.Write(b); err != nil {
				return err
			}
		}
	}
	return zw.Close()
}

// LoadFolder loads the skills directly under dir (a repository's skills/), for rangka skills check.
func LoadFolder(dir, source string) ([]*Skill, []Problem) {
	return loadDir(os.DirFS(dir), source)
}

func loadDir(fsys fs.FS, source string) ([]*Skill, []Problem) {
	var out []*Skill
	var probs []Problem
	entries, _ := fs.ReadDir(fsys, ".")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub, _ := fs.Sub(fsys, e.Name())
		s, err := parse(sub, e.Name(), source)
		if err != nil {
			probs = append(probs, Problem{e.Name(), "SKILL.md", "skill_unreadable", err.Error()})
			continue
		}
		out = append(out, s)
	}
	return out, probs
}

func parse(fsys fs.FS, dir, source string) (*Skill, error) {
	b, err := fs.ReadFile(fsys, "SKILL.md")
	if err != nil {
		return nil, fmt.Errorf("no SKILL.md")
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil, fmt.Errorf("SKILL.md must start with a --- frontmatter block")
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return nil, fmt.Errorf("frontmatter is not closed with ---")
	}
	front, body := text[4:4+end+1], text[4+end+5:]
	doc, err := rk.LoadBytes(dir+"/SKILL.md", []byte(front))
	if err != nil {
		return nil, fmt.Errorf("frontmatter: %v", err)
	}
	m, _ := doc.Data.(map[string]any)
	s := &Skill{Source: source, Body: body, dir: dir, fsys: fsys}
	s.Name, _ = m["name"].(string)
	s.Description, _ = m["description"].(string)
	s.Metadata, _ = m["metadata"].(map[string]any)
	fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && p != "SKILL.md" {
			s.Files = append(s.Files, p)
		}
		return nil
	})
	sort.Strings(s.Files)
	return s, nil
}

var (
	nameRe     = regexp.MustCompile(`^rangka-[a-z0-9]+(-[a-z0-9]+)*$`)
	linkRe     = regexp.MustCompile(`\]\(([^)#\s]+)(#[^)]*)?\)`)
	toolRe     = regexp.MustCompile("\\brangka_[a-z_]+\\b")
	forbidRe   = regexp.MustCompile("(?i)\\brangka_approve\\b|/apply\\b|POST /api/v1/changesets/[^ ]*/(approve|apply)|/api/v1/secrets")
	secretLike = regexp.MustCompile(`\b(sk-ant-[A-Za-z0-9_-]{8,}|sk-[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16}|AGE-SECRET-KEY-1[0-9A-Z]{20,}|rk_[A-Za-z0-9]{20,})\b`)
)

// MaxDescription is the Agent Skills limit on description length.
const MaxDescription = 1024

// Check lints one skill (contract 0035 §6). tools is the set of MCP tool names this kernel serves; a skill naming
// another rangka_ tool fails.
func Check(s *Skill, tools map[string]bool) []Problem {
	var out []Problem
	bad := func(code, file, msg string) { out = append(out, Problem{s.dir, file, code, msg}) }
	if s.Name != s.dir {
		bad("skill_name_folder", "SKILL.md", fmt.Sprintf("name %q must equal the folder %q", s.Name, s.dir))
	}
	if !nameRe.MatchString(s.Name) {
		bad("skill_name", "SKILL.md", fmt.Sprintf("name %q must be kebab-case and start with rangka-", s.Name))
	}
	if d := strings.TrimSpace(s.Description); d == "" || len(d) > MaxDescription {
		bad("skill_description", "SKILL.md", fmt.Sprintf("description must be 1 to %d characters, is %d", MaxDescription, len(d)))
	}
	if strings.TrimSpace(s.Body) == "" {
		bad("skill_body", "SKILL.md", "SKILL.md has no instructions after the frontmatter")
	}
	files := map[string]string{"SKILL.md": s.Body}
	for _, f := range s.Files {
		if t, err := s.ReadFile(f); err == nil && strings.HasSuffix(f, ".md") {
			files[f] = t
		}
	}
	for _, f := range sortedKeys(files) {
		t := files[f]
		for _, m := range linkRe.FindAllStringSubmatch(t, -1) {
			target := m[1]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			if _, err := s.ReadFile(path.Join(path.Dir(f), target)); err != nil {
				bad("skill_link", f, "link to a missing file: "+target)
			}
		}
		if tools != nil {
			seen := map[string]bool{}
			for _, tool := range toolRe.FindAllString(t, -1) {
				if !tools[tool] && !seen[tool] {
					seen[tool] = true
					bad("skill_unknown_tool", f, "names an MCP tool this kernel does not serve: "+tool)
				}
			}
		}
		if loc := forbidRe.FindString(t); loc != "" {
			bad("skill_forbidden", f, "tells an agent to approve, apply or read secrets ("+loc+"); agents never do (contract 0020)")
		}
		if secretLike.MatchString(t) {
			bad("skill_secret", f, "contains something that looks like a key or token")
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
