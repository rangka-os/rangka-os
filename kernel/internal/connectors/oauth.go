package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// OAuth2 (contract 0017 §8): for auth.type oauth2 the kernel keeps access_token, refresh_token and expires_at as
// secrets (db driver) and refreshes the access token at auth.token_url with client_id and client_secret before a call
// when it expires within a minute. One refresh at a time per (org, connector), under an advisory lock. When refresh
// fails the access token is removed, so the connector shows needs_auth, and the failure is audited for the owner.

var errNoOAuth = errors.New("not an oauth2 connector")

const refreshMargin = time.Minute

func (rt *Runtime) refreshIfDue(ctx context.Context, org, connector string) error {
	c, ok := rt.connector(connector)
	auth := meta.AsMap(c["auth"])
	if !ok || meta.Str(auth, "type") != "oauth2" || rt.Secrets == nil {
		return errNoOAuth
	}
	due := func() (bool, error) {
		_, haveTok, err := rt.Secrets.Value(ctx, org, connector, "access_token")
		if err != nil {
			return false, err
		}
		exp, haveExp, err := rt.Secrets.Value(ctx, org, connector, "expires_at")
		if err != nil {
			return false, err
		}
		if !haveTok {
			return true, nil
		}
		if !haveExp {
			return false, nil // no expiry known: use the token until the vendor refuses it
		}
		t, ok := parseExpiry(exp)
		return ok && rt.now().Add(refreshMargin).After(t), nil
	}
	if d, err := due(); err != nil || !d {
		return err
	}
	tx, err := rt.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock(hashtextextended($1, 2))", "oauth2|"+org+"|"+connector); err != nil {
		return err
	}
	rt.Secrets.Forget(org, connector) // another worker may have refreshed while we waited
	if d, err := due(); err != nil || !d {
		return err
	}
	if err := rt.refresh(ctx, org, connector, auth); err != nil {
		// the owner reconnects: drop the stale access token so the connector shows needs_auth
		rt.Secrets.Delete(context.WithoutCancel(ctx), org, connector, "access_token", "system:oauth2")
		store.Audit(context.WithoutCancel(ctx), rt.DB, org, "connector:"+connector, "connector.needs_auth", "connector/"+connector,
			"OAuth2 refresh failed; the owner must connect again", map[string]any{"code": "oauth2_refresh_failed"})
		return fmt.Errorf("%w: OAuth2 refresh failed: %v", ErrNeedsAuth, err)
	}
	return tx.Commit(ctx)
}

func parseExpiry(s string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(n, 0), true
	}
	return time.Time{}, false
}

func (rt *Runtime) refresh(ctx context.Context, org, connector string, auth map[string]any) error {
	tokenURL := meta.Str(auth, "token_url")
	u, err := url.Parse(tokenURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("auth.token_url must be an https URL")
	}
	get := func(f string) (string, error) {
		v, ok, err := rt.Secrets.Value(ctx, org, connector, f)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("%s is not set", f)
		}
		return v, nil
	}
	refreshTok, err := get("refresh_token")
	if err != nil {
		return err
	}
	clientID, err := get("client_id")
	if err != nil {
		return err
	}
	clientSecret, _, _ := rt.Secrets.Value(ctx, org, connector, "client_secret")
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshTok}, "client_id": {clientID}}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := rt.client(true, strings.ToLower(u.Hostname())).Do(req)
	scrub, _ := rt.Scrubber(ctx, org, connector)
	if err != nil {
		return errors.New(scrub.String(err.Error()))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, cut(scrub.String(string(raw))))
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil || tok.AccessToken == "" {
		return errors.New("the token answer has no access_token")
	}
	const by = "system:oauth2"
	if _, err := rt.Secrets.Set(ctx, org, connector, "access_token", tok.AccessToken, by); err != nil {
		return err
	}
	if tok.RefreshToken != "" && tok.RefreshToken != refreshTok {
		if _, err := rt.Secrets.Set(ctx, org, connector, "refresh_token", tok.RefreshToken, by); err != nil {
			return err
		}
	}
	if tok.ExpiresIn > 0 {
		exp := rt.now().Add(time.Duration(tok.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
		if _, err := rt.Secrets.Set(ctx, org, connector, "expires_at", exp, by); err != nil {
			return err
		}
	}
	return nil
}
