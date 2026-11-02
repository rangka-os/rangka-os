// Command rangka is the Rangka OS kernel: validate metadata, migrate the database, seed demo data and serve
// the REST/MCP API.
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
  rangka migrate  --apps <dir> [--site <dir>] --db <url> [--dry-run]
  rangka seed     --apps <dir> [--site <dir>] --db <url> [--fixtures <dir>] [--org kopi-senja]
  rangka serve    --apps <dir> [--site <dir>] --db <url> [--addr :8080] [--web <dir>] [--dev-open]
  rangka user add <email> --org <id> --role <role> [--outlets a,b] [--record USR-001] [--name "..."] [--db <url>]
  rangka user remove <email> --org <id> [--db <url>]
  rangka version

Settings can also come from the environment or a .env file in the current directory:
  DATABASE_URL, RANGKA_APPS, RANGKA_SITE, RANGKA_CONNECTORS, RANGKA_FIXTURES, RANGKA_ORG, RANGKA_ADDR,
  RANGKA_OWNER_EMAIL, RANGKA_OWNER_PASSWORD (seed), RANGKA_USER_PASSWORD (user add, new users only). Values already set in the environment win.

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
	case "serve":
		code = cmdServe(os.Args[2:])
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
