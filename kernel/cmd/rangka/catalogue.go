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

const skillsUsage = `usage: rangka skills list [--apps <dir>] [--modules <dir>]
       rangka skills check [dir ...]
list: the platform skills and the skills of every app and module (contract 0035).
check: lint skills. Each dir is a skills folder (its children hold SKILL.md) or a folder of units whose children
have skills/ (apps, modules). With no dir: the platform skills.`

func cmdSkills(args []string) int {
	if len(args) == 0 || (args[0] != "list" && args[0] != "check") {
		fmt.Fprintln(os.Stderr, skillsUsage)
		return 2
	}
	if args[0] == "list" {
		var c common
		fs := flag.NewFlagSet("skills list", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		c.flags(fs)
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
			fmt.Fprintln(os.Stderr, skillsUsage)
			return 2
		}
		modules := c.modules
		if modules == "" {
			modules = meta.DefaultModulesDir(c.apps)
		}
		lib := skills.Load(c.apps, modules)
		for _, s := range lib.Skills {
			fmt.Fprintf(out(), "%-28s %-20s %s\n", s.Name, s.Source, firstSentence(s.Description))
		}
		return 0
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
