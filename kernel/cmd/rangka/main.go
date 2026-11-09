// Command rangka is the Rangka OS kernel: validate metadata, migrate the database, seed demo data, serve
// the REST/MCP API, and back up and restore an installation.
package main

import (
	"fmt"
	"os"
)

// Version is set at build time with -ldflags "-X main.Version=…".
var Version = "0.4.0-dev"

func usage() {
	fmt.Fprint(os.Stderr, `rangka — Rangka OS kernel

Usage:
  rangka validate --apps <dir> [--site <dir>] [--json] [app ...]
  rangka migrate  --apps <dir> [--site <dir>] --db <url> [--dry-run] [--allow-destructive] [--rollback n]
  rangka seed     --apps <dir> [--site <dir>] --db <url> [--fixtures <dir>] [--org kopi-senja]
  rangka serve    --apps <dir> [--site <dir>] --db <url> [--addr :8080] [--web <dir>] [--dev-open]
  rangka backup   [--apps <dir>] [--site <dir>] --db <url> [--out file] [--org id] [--files dir]
  rangka restore  <file> --db <url> [--into dir] [--files dir]
  rangka user add <email> --org <id> --role <role> [--outlets a,b] [--record USR-001] [--name "..."] [--reset-password] [--create-org] [--db <url>]
  rangka user remove <email> --org <id> [--db <url>]
    (the email may also be --email <e>; the password comes from RANGKA_USER_PASSWORD or --password, new accounts or --reset-password only)
  rangka numbering set <org> <series> <next> [--period 2026|2026-11] [--apps <dir>] [--site <dir>] [--db <url>]
    (once at go-live: continue the client's own numbering; never lowers a counter or reissues a name)
  rangka plan show|set <plan>|clear [--note "..."] [--apps <dir>] [--site <dir>] [--db <url>]
    (the tenant's plan, contract 0028: an operator's upgrade or downgrade; a downgrade deletes nothing)
  rangka catalogue [--json] [--apps <dir>] [--site <dir>]
    (apps, modules and connectors with their state for the site; per app what it shares and adds, contract 0035)
  rangka skills list|install|pack|check [--to <dir> | --harness claude|agents] [--out <file.zip>] [dir ...]
    (Rangka skills for AI agents, contract 0035: list, install into a harness's folder, pack the zip, lint)
  rangka version

Settings can also come from the environment or a .env file in the current directory:
  DATABASE_URL, RANGKA_APPS, RANGKA_SITE, RANGKA_CONNECTORS, RANGKA_FIXTURES, RANGKA_ORG, RANGKA_ADDR,
  RANGKA_OWNER_EMAIL, RANGKA_OWNER_PASSWORD (seed), RANGKA_USER_PASSWORD (user add: new accounts or --reset-password). Values already set in the environment win.

Start a new project: npm create @rangka-os@latest my-shop
`)
}

func main() {
	loadDotEnv(".env")
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var code int
	switch os.Args[1] {
	case "validate":
		code = cmdValidate(os.Args[2:])
	case "migrate":
		code = cmdMigrate(os.Args[2:])
	case "seed":
		code = cmdSeed(os.Args[2:])
	case "user":
		code = cmdUser(os.Args[2:])
	case "plan":
		code = cmdPlan(os.Args[2:])
	case "numbering":
		code = cmdNumbering(os.Args[2:])
	case "serve":
		code = cmdServe(os.Args[2:])
	case "backup":
		code = cmdBackup(os.Args[2:])
	case "restore":
		code = cmdRestore(os.Args[2:])
	case "catalogue":
		code = cmdCatalogue(os.Args[2:])
	case "skills":
		code = cmdSkills(os.Args[2:])
	case "version", "--version":
		fmt.Println("rangka", Version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		code = 2
	}
	os.Exit(code)
}
