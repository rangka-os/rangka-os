package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// Envelope encryption for the db driver (contract 0017 §2). Each org has a data key (DEK, 32 random bytes) stored in
// rk_org_keys sealed by the key-encryption key (KEK). The KEK never sits in the database: it comes from
// RANGKA_MASTER_KEY (base64 of 32 bytes), a Vault transit key (RANGKA_MASTER_KEY_VAULT) or the file named by
// RANGKA_MASTER_KEY_FILE. Cloud KMS sources wait for the Rangka Cloud hosting decision.

// KEK is a key-encryption key.
type KEK struct {
	key []byte
	ID  string // first 16 hex characters of sha256(key), or vault-transit:<name>: names the key in rk_org_keys and backup manifests
	// transit, when set, wraps data keys in Vault transit instead of key (RANGKA_MASTER_KEY_VAULT)
	transit *transitKEK
}

// TransitKEK is a KEK held in Vault transit under key name: data keys are encrypted and decrypted by Vault.
func TransitKEK(v *Vault, name string) *KEK {
	return &KEK{ID: "vault-transit:" + name, transit: &transitKEK{v: v, name: name}}
}

// ErrBadKey is a KEK that is not base64 of 32 bytes.
var ErrBadKey = errors.New("RANGKA_MASTER_KEY must be base64 of 32 bytes (rangka keygen prints one)")

// ParseKEK reads a base64 KEK.
func ParseKEK(text string) (*KEK, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil || len(b) != 32 {
		return nil, ErrBadKey
	}
	sum := sha256.Sum256(b)
	return &KEK{key: b, ID: hex.EncodeToString(sum[:8])}, nil
}

// LoadKEK reads RANGKA_MASTER_KEY, else a Vault transit key named by RANGKA_MASTER_KEY_VAULT, else the file named by
// RANGKA_MASTER_KEY_FILE. nil, nil when neither is set: the
// kernel starts, and db-driver writes answer secrets_key_missing.
func LoadKEK(getenv func(string) string) (*KEK, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if v := getenv("RANGKA_MASTER_KEY"); v != "" {
		return ParseKEK(v)
	}
	if name := getenv("RANGKA_MASTER_KEY_VAULT"); name != "" {
		v := VaultFromEnv(getenv)
		if v == nil || v.Token == "" {
			return nil, fmt.Errorf("RANGKA_MASTER_KEY_VAULT: %w", ErrVaultConfig)
		}
		return TransitKEK(v, name), nil
	}
	if p := getenv("RANGKA_MASTER_KEY_FILE"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("RANGKA_MASTER_KEY_FILE: %w", err)
		}
		return ParseKEK(string(b))
	}
	return nil, nil
}

// NewKEKText makes a new random KEK, base64 (rangka keygen).
func NewKEKText() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func seal(key, plain, aad []byte) (ct, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return gcm.Seal(nil, nonce, plain, aad), nonce, nil
}

// ErrDecrypt is a value or data key that does not open: wrong KEK, or a row copied to another org, connector or field.
var ErrDecrypt = errors.New("secret does not decrypt (wrong key, or copied from another org, connector or field)")

func open(key, ct, nonce, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, ErrDecrypt
	}
	out, err := gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return out, nil
}

func dekAAD(org, dekID string) []byte { return []byte("rk_org_keys|" + org + "|" + dekID) }

func (k *KEK) wrap(ctx context.Context, org, dekID string, dek []byte) (wrapped, nonce []byte, err error) {
	if k.transit != nil {
		w, err := k.transit.wrap(ctx, org, dekID, dek)
		return w, []byte{}, err
	}
	return seal(k.key, dek, dekAAD(org, dekID))
}

func (k *KEK) unwrap(ctx context.Context, org, dekID, kekID string, wrapped, nonce []byte) ([]byte, error) {
	if kekID != k.ID {
		return nil, fmt.Errorf("%w: data key of org %s is wrapped by KEK %s, this kernel has %s", ErrDecrypt, org, kekID, k.ID)
	}
	if k.transit != nil {
		return k.transit.unwrap(ctx, org, dekID, wrapped)
	}
	return open(k.key, wrapped, nonce, dekAAD(org, dekID))
}

// orgKey returns the org's data key, making one on first use.
func (s *Service) orgKey(ctx context.Context, q pg.Querier, org string) (string, []byte, error) {
	res, err := q.Query(ctx, "select dek_id, wrapped, nonce, kek_id from rk_org_keys where org_id = $1 order by created_at desc limit 1", org)
	if err != nil {
		return "", nil, err
	}
	if len(res.Rows) > 0 {
		r := res.Rows[0]
		dek, err := s.KEK.unwrap(ctx, org, r[0].(string), r[3].(string), r[1].([]byte), r[2].([]byte))
		return r[0].(string), dek, err
	}
	dek := make([]byte, 32)
	idb := make([]byte, 8)
	if _, err := rand.Read(dek); err != nil {
		return "", nil, err
	}
	if _, err := rand.Read(idb); err != nil {
		return "", nil, err
	}
	dekID := hex.EncodeToString(idb)
	wrapped, nonce, err := s.KEK.wrap(ctx, org, dekID, dek)
	if err != nil {
		return "", nil, err
	}
	if _, err := q.Exec(ctx, "insert into rk_org_keys (org_id, dek_id, wrapped, nonce, kek_id) values ($1, $2, $3, $4, $5)",
		org, dekID, wrapped, nonce, s.KEK.ID); err != nil {
		return "", nil, err
	}
	return dekID, dek, nil
}

// Rewrap seals every data key wrapped by old under next, without touching the values (rangka secrets rewrap).
// It returns how many data keys moved.
func Rewrap(ctx context.Context, db *pg.Pool, old, next *KEK) (int, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	res, err := tx.Query(ctx, "select org_id, dek_id, wrapped, nonce, kek_id from rk_org_keys for update")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range res.Rows {
		org, dekID, kekID := r[0].(string), r[1].(string), r[4].(string)
		if kekID == next.ID {
			continue
		}
		dek, err := old.unwrap(ctx, org, dekID, kekID, r[2].([]byte), r[3].([]byte))
		if err != nil {
			return 0, err
		}
		wrapped, nonce, err := next.wrap(ctx, org, dekID, dek)
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, "update rk_org_keys set wrapped = $3, nonce = $4, kek_id = $5 where org_id = $1 and dek_id = $2",
			org, dekID, wrapped, nonce, next.ID); err != nil {
			return 0, err
		}
		n++
	}
	return n, tx.Commit(ctx)
}
