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

// validateFlags parses validate's flags. Like migrate, seed and serve, the directories default to RANGKA_APPS,
// RANGKA_SITE, RANGKA_MODULES and RANGKA_CONNECTORS (set in the environment or .env).
func validateFlags(args []string) (opts meta.Options, files []string, asJSON bool, schemas string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	fs.StringVar(&opts.AppsDir, "apps", envOr("RANGKA_APPS", "apps"), "apps directory")
	fs.StringVar(&opts.SiteDir, "site", os.Getenv("RANGKA_SITE"), "site directory (system.rk, features.rk, config.rk, themes/, layouts/, custom/)")
	fs.StringVar(&opts.ConnectorsDir, "connectors", os.Getenv("RANGKA_CONNECTORS"), "connectors directory (<name>/connector.rk)")
	fs.StringVar(&opts.ModulesDir, "modules", os.Getenv("RANGKA_MODULES"), "modules directory (<name>/module.rk; default: modules next to the apps directory)")
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	fs.StringVar(&schemas, "schemas", "", "schema directory (default: the schemas built into the binary)")
	fs.Parse(reorder(args))
	return opts, fs.Args(), asJSON, schemas
}

func cmdValidate(args []string) int {
	opts, files, asJSON, schemas := validateFlags(args)
	set, err := loadSchemas(schemas)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if st, err := os.Stat(opts.AppsDir); err != nil || !st.IsDir() {
		fmt.Fprintf(os.Stderr, "apps directory %q not found\n", opts.AppsDir)
		return 2
	}
	_, rep := meta.ValidateOpts(opts, files, set)
	var errs int
	if asJSON {
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
