package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/mcp"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/skills"
)

// cmdOut is where catalogue and skills report (tests swap it; nil is stdout).
var cmdOut io.Writer

func out() io.Writer {
	if cmdOut != nil {
		return cmdOut
	}
	return os.Stdout
}

const catalogueUsage = `usage: rangka catalogue [--json] [--apps <dir>] [--site <dir>] [--modules <dir>] [--connectors <dir>]
Every app, module and connector, with its state for the site (installed, available, not_in_plan) and, per app, the
installed modules it shares and the modules it adds (contract 0035). Without --site nothing counts as installed.`

func cmdCatalogue(args []string) int {
	var c common
	fs := flag.NewFlagSet("catalogue", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	c.flags(fs)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, catalogueUsage)
		return 2
	}
	reg, err := c.registry()
	if err != nil {
		return fail(err)
	}
	cat := reg.Catalogue()
	if *asJSON {
		enc := json.NewEncoder(out())
		enc.SetIndent("", "  ")
		if err := enc.Encode(cat); err != nil {
			return fail(err)
		}
		return 0
	}
	w := out()
	if cat.Plan != "" {
		fmt.Fprintf(w, "plan %s\n", cat.Plan)
	}
	fmt.Fprintln(w, "apps:")
	for _, a := range cat.Apps {
		fmt.Fprintf(w, "  %-20s %-12s %s\n", a.Name, a.State, a.Title.Text())
		if a.State != meta.StateInstalled {
			fmt.Fprintf(w, "  %-20s %-12s shares [%s] adds [%s]\n", "", "", strings.Join(a.Shares, ", "), strings.Join(a.Adds, ", "))
		}
	}
	fmt.Fprintln(w, "modules:")
	for _, m := range cat.Modules {
		by := ""
		if len(m.InstalledBy) > 0 {
			by = "by " + strings.Join(m.InstalledBy, ", ")
		}
		fmt.Fprintf(w, "  %-20s %-12s %s\n", m.Name, m.State, by)
	}
	if len(cat.Connectors) > 0 {
		run := "the kernel runs them"
		if !cat.ConnectorsRun {
			run = "listed only: this kernel does not run connectors yet (contract 0002)"
		}
		fmt.Fprintf(w, "connectors (%s):\n", run)
		for _, k := range cat.Connectors {
			fmt.Fprintf(w, "  %-20s %s\n", k.Name, k.Label.Text())
		}
	}
	return 0
}

const skillsUsage = `usage: rangka skills list [--apps <dir>] [--modules <dir>] [--connectors <dir>]
       rangka skills install [--to <dir> | --harness claude|agents] [--apps <dir>] [--modules <dir>] [--connectors <dir>]
       rangka skills pack --out <file.zip> [--apps <dir>] [--modules <dir>] [--connectors <dir>]
       rangka skills check [dir ...]
list: the platform skills and the skills of every app, module and connector folder found (contract 0035).
install: copy them into a harness's skills folder: --harness claude (.claude/skills, the default), --harness agents
  (.agents/skills), or any --to. Each rangka-* folder of the same name is replaced; nothing else is touched.
pack: the same skills as a zip with <skill>/SKILL.md at the top (the release asset rangka-skills-<version>.zip).
  Refuses when a skill fails the check.
check: lint skills. Each dir is a skills folder (its children hold SKILL.md) or a folder of units whose children
have skills/ (apps, modules). With no dir: the platform skills.`

// harnessDirs are the skills folders harnesses read, relative to a project (contract 0035 §3).
var harnessDirs = map[string]string{"claude": filepath.Join(".claude", "skills"), "agents": filepath.Join(".agents", "skills")}

// skillLibrary is every skill this command can see: platform, apps, modules and <connectors>/skills.
func (c *common) skillLibrary() *skills.Library {
	modules := c.modules
	if modules == "" {
		modules = meta.DefaultModulesDir(c.apps)
	}
	var extra []skills.Folder
	if d := c.connectorsDir(); d != "" {
		extra = append(extra, skills.Folder{Dir: filepath.Join(d, "skills"), Source: "connectors"})
	}
	return skills.LoadAll(c.apps, modules, extra...)
}

func cmdSkills(args []string) int {
	if len(args) == 0 || (args[0] != "list" && args[0] != "check" && args[0] != "install" && args[0] != "pack") {
		fmt.Fprintln(os.Stderr, skillsUsage)
		return 2
	}
	if args[0] != "check" {
		var c common
		fs := flag.NewFlagSet("skills "+args[0], flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		c.flags(fs)
		to := fs.String("to", "", "install: target skills folder")
		harness := fs.String("harness", "", "install: claude or agents")
		zipOut := fs.String("out", "", "pack: zip file to write")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
			fmt.Fprintln(os.Stderr, skillsUsage)
			return 2
		}
		lib := c.skillLibrary()
		for _, p := range lib.Problems {
			fmt.Fprintln(os.Stderr, "rangka skills:", p.String())
		}
		switch args[0] {
		case "list":
			for _, s := range lib.Skills {
				fmt.Fprintf(out(), "%-28s %-20s %s\n", s.Name, s.Source, firstSentence(s.Description))
			}
			return 0
		case "install":
			dir, err := installDir(*to, *harness)
			if err != nil {
				fmt.Fprintln(os.Stderr, "rangka skills install:", err)
				fmt.Fprintln(os.Stderr, skillsUsage)
				return 2
			}
			for _, s := range lib.Skills {
				if err := s.CopyTo(dir); err != nil {
					return fail(err)
				}
				fmt.Fprintf(out(), "installed %s (%s)\n", filepath.Join(dir, s.Name), s.Source)
			}
			fmt.Fprintf(out(), "%d skill(s) in %s\n", len(lib.Skills), dir)
			return 0
		default: // pack
			if *zipOut == "" {
				fmt.Fprintln(os.Stderr, "rangka skills pack: --out is required")
				return 2
			}
			tools := mcp.ToolNames()
			bad := len(lib.Problems)
			for _, s := range lib.Skills {
				for _, p := range skills.Check(s, tools) {
					fmt.Fprintln(os.Stderr, "rangka skills pack:", p.String())
					bad++
				}
			}
			if bad > 0 {
				return fail(fmt.Errorf("%d problem(s); nothing packed", bad))
			}
			if err := os.MkdirAll(filepath.Dir(*zipOut), 0o755); err != nil {
				return fail(err)
			}
			f, err := os.Create(*zipOut)
			if err != nil {
				return fail(err)
			}
			if err := skills.Pack(f, lib.Skills); err != nil {
				f.Close()
				return fail(err)
			}
			if err := f.Close(); err != nil {
				return fail(err)
			}
			fmt.Fprintf(out(), "packed %d skill(s) into %s\n", len(lib.Skills), *zipOut)
			return 0
		}
	}
	n, err := checkSkills(args[1:])
	if err != nil {
		return fail(err)
	}
	if n > 0 {
		fmt.Fprintf(os.Stderr, "rangka skills check: %d problem(s)\n", n)
		return 1
	}
	return 0
}

// checkSkills lints the skills under dirs (or the platform skills) and prints each problem; it returns the count.
func checkSkills(dirs []string) (int, error) {
	tools := mcp.ToolNames()
	var list []*skills.Skill
	var probs []skills.Problem
	if len(dirs) == 0 {
		list = skills.Platform()
	}
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			return 0, err
		}
		folder := false
		for _, e := range entries {
			if _, err := os.Stat(filepath.Join(d, e.Name(), "SKILL.md")); err == nil {
				folder = true
				break
			}
		}
		if folder {
			l, p := skills.LoadFolder(d, d)
			list, probs = append(list, l...), append(probs, p...)
			continue
		}
		for _, e := range entries {
			sd := filepath.Join(d, e.Name(), "skills")
			if st, err := os.Stat(sd); err == nil && st.IsDir() {
				l, p := skills.LoadFolder(sd, sd)
				list, probs = append(list, l...), append(probs, p...)
			}
		}
	}
	seen := map[string]string{}
	for _, s := range list {
		probs = append(probs, skills.Check(s, tools)...)
		if src, dup := seen[s.Name]; dup {
			probs = append(probs, skills.Problem{Skill: s.Name, File: "SKILL.md", Code: "skill_duplicate", Message: "also in " + src})
		}
		seen[s.Name] = s.Source
	}
	for _, p := range probs {
		fmt.Fprintln(out(), p.String())
	}
	if len(list) == 0 && len(probs) == 0 {
		return 0, errors.New("no skills found")
	}
	fmt.Fprintf(out(), "%d skill(s) checked\n", len(list))
	return len(probs), nil
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}

// installDir picks the target folder: --to wins, else the harness's folder in the current directory.
func installDir(to, harness string) (string, error) {
	if to != "" {
		if harness != "" {
			return "", errors.New("pass --to or --harness, not both")
		}
		return to, nil
	}
	if harness == "" {
		harness = "claude"
	}
	d, ok := harnessDirs[harness]
	if !ok {
		return "", fmt.Errorf("unknown harness %q (claude or agents; any other: --to <dir>)", harness)
	}
	return d, nil
}
