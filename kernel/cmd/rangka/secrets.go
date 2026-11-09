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

const secretsUsage = `usage: rangka secrets rewrap (--new-key-file <file> | --new-vault-key <name>) [--db <url>]
  re-wraps every org data key under a new key-encryption key without touching the secret values (contract 0017 §2).
  --new-key-file   a file holding the new KEK (base64, rangka keygen prints one)
  --new-vault-key  a Vault transit key name: the new KEK stays in Vault (VAULT_ADDR, VAULT_TOKEN)
  The current key comes from RANGKA_MASTER_KEY, RANGKA_MASTER_KEY_VAULT or RANGKA_MASTER_KEY_FILE. Afterwards start
  the kernel with the new key (for a transit key: RANGKA_MASTER_KEY_VAULT=<name>).`

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
	newVault := fs.String("new-vault-key", "", "Vault transit key name for the new KEK")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 || (*newFile == "") == (*newVault == "") {
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
		return fail(fmt.Errorf("set the current key in RANGKA_MASTER_KEY, RANGKA_MASTER_KEY_VAULT or RANGKA_MASTER_KEY_FILE"))
	}
	next, err := newKEK(*newFile, *newVault)
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

// newKEK reads the rewrap target: a base64 key file, or a Vault transit key (the key material never leaves Vault).
func newKEK(file, vaultKey string) (*secrets.KEK, error) {
	if vaultKey != "" {
		v := secrets.VaultFromEnv(nil)
		if v == nil || v.Token == "" {
			return nil, fmt.Errorf("--new-vault-key: %w", secrets.ErrVaultConfig)
		}
		return secrets.TransitKEK(v, vaultKey), nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return secrets.ParseKEK(strings.TrimSpace(string(b)))
}
