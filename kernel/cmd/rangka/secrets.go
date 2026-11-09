package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/secrets"
)

const keygenUsage = `usage: rangka keygen
  prints a new key-encryption key (base64 of 32 random bytes) for RANGKA_MASTER_KEY (contract 0017 §2).
  Keep it outside the database and outside backups; without it the db driver's secrets cannot be read.`

const secretsUsage = `usage: rangka secrets rewrap --new-key-file <file> [--db <url>]
  re-wraps every org data key under a new key-encryption key without touching the secret values (contract 0017 §2).
  The current key comes from RANGKA_MASTER_KEY or RANGKA_MASTER_KEY_FILE. Afterwards start the kernel with the new key.`

func cmdKeygen(args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, keygenUsage)
		return 2
	}
	k, err := secrets.NewKEKText()
	if err != nil {
		return fail(err)
	}
	fmt.Fprintln(out(), k)
	return 0
}

func cmdSecrets(args []string) int {
	if len(args) == 0 || args[0] != "rewrap" {
		fmt.Fprintln(os.Stderr, secretsUsage)
		return 2
	}
	fs := flag.NewFlagSet("secrets rewrap", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	db := fs.String("db", os.Getenv("DATABASE_URL"), "Postgres URL")
	newFile := fs.String("new-key-file", "", "file holding the new KEK (base64)")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 || *newFile == "" {
		fmt.Fprintln(os.Stderr, secretsUsage)
		return 2
	}
	if *db == "" {
		return fail(errNoDB)
	}
	old, err := secrets.LoadKEK(nil)
	if err != nil {
		return fail(err)
	}
	if old == nil {
		return fail(fmt.Errorf("set the current key in RANGKA_MASTER_KEY or RANGKA_MASTER_KEY_FILE"))
	}
	b, err := os.ReadFile(*newFile)
	if err != nil {
		return fail(err)
	}
	next, err := secrets.ParseKEK(strings.TrimSpace(string(b)))
	if err != nil {
		return fail(err)
	}
	ctx := context.Background()
	c := common{db: *db}
	pool, err := c.pool(ctx)
	if err != nil {
		return fail(err)
	}
	defer pool.Close()
	n, err := secrets.Rewrap(ctx, pool, old, next)
	if err != nil {
		return fail(err)
	}
	fmt.Fprintf(out(), "re-wrapped %d data key(s) under KEK %s\n", n, next.ID)
	return 0
}
