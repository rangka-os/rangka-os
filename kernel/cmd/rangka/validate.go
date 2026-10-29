package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

func loadSchemas(dir string) (*schema.Set, error) {
	if dir == "" {
		return schema.Embedded(), nil
	}
	return schema.LoadDir(dir)
}

func cmdValidate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	apps := fs.String("apps", "apps", "apps directory")
	site := fs.String("site", "", "site directory (system.rk, features.rk, config.rk, themes/, layouts/, custom/)")
	connectors := fs.String("connectors", "", "connectors directory (<name>/connector.rk)")
	asJSON := fs.Bool("json", false, "print JSON")
	schemas := fs.String("schemas", "", "schema directory (default: the schemas built into the binary)")
	fs.Parse(reorder(args))
	set, err := loadSchemas(*schemas)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if st, err := os.Stat(*apps); err != nil || !st.IsDir() {
		fmt.Fprintf(os.Stderr, "apps directory %q not found\n", *apps)
		return 2
	}
	_, rep := meta.ValidateOpts(meta.Options{AppsDir: *apps, SiteDir: *site, ConnectorsDir: *connectors}, fs.Args(), set)
	var errs int
	if *asJSON {
		errs = rep.JSON(os.Stdout)
	} else {
		errs = rep.Print(os.Stdout)
	}
	if errs > 0 {
		return 1
	}
	return 0
}

// reorder moves flags before positional arguments so `rangka validate apps/ --site x` works.
func reorder(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			if !containsEq(a) && i+1 < len(args) && !isBoolFlag(a) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func containsEq(s string) bool {
	for _, c := range s {
		if c == '=' {
			return true
		}
	}
	return false
}

func isBoolFlag(a string) bool {
	switch a {
	case "--json", "-json", "--dev-open", "-dev-open", "--dry-run", "-dry-run", "--reset", "-reset":
		return true
	}
	return false
}
