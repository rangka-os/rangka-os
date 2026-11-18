// Package notices delivers kernel notices beyond the kernel's own inbox (contract 0066): the post to the host that
// runs the kernel (§3) and, on a kernel without a host, mail over SMTP (§4.2).
package notices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Environment variables of the host transport (§3). Both must be set; they come from the deployment, never from
// config.rk or system.rk.
const (
	EnvHostURL    = "RANGKA_HOST_NOTICES_URL"
	EnvHostSecret = "RANGKA_HOST_NOTICES_SECRET"
)

// MaxBody is the largest event the kernel posts (§3).
const MaxBody = 16 << 10

// Host posts events to the host's endpoint.
type Host struct {
	URL    string
	Secret string
	Client *http.Client
}

// lookupIP is net.DefaultResolver.LookupIP (a test swaps it).
var lookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// HostFromEnv reads the two variables. nil, nil when neither is set. An error names what is wrong, never the secret:
// one variable without the other, a URL that is not https (http only on a loopback or private address), or with
// credentials, a query or a fragment.
func HostFromEnv(getenv func(string) string) (*Host, error) {
	u, sec := strings.TrimSpace(getenv(EnvHostURL)), strings.TrimSpace(getenv(EnvHostSecret))
	if u == "" && sec == "" {
		return nil, nil
	}
	if u == "" || sec == "" {
		return nil, fmt.Errorf("%s and %s must both be set", EnvHostURL, EnvHostSecret)
	}
	if err := checkURL(u); err != nil {
		return nil, fmt.Errorf("%s: %w", EnvHostURL, err)
	}
	return &Host{URL: u, Secret: sec, Client: &http.Client{Timeout: 10 * time.Second,
		// the notice client calls this URL only: a redirect is an answer, not a new destination
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func checkURL(raw string) error {
	p, err := url.Parse(raw)
	if err != nil || p.Host == "" {
		return errors.New("not an absolute URL")
	}
	if p.User != nil || p.RawQuery != "" || p.Fragment != "" {
		return errors.New("must not carry credentials, a query or a fragment")
	}
	switch p.Scheme {
	case "https":
		return nil
	case "http":
		if localHost(p.Hostname()) {
			return nil
		}
		return errors.New("http is allowed only on a loopback or private address; use https")
	}
	return errors.New("must be https")
}

// localHost reports whether host is localhost or only resolves to loopback or private addresses.
func localHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ips := []net.IP{net.ParseIP(host)}
	if ips[0] == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var err error
		if ips, err = lookupIP(ctx, host); err != nil || len(ips) == 0 {
			return false
		}
	}
	for _, ip := range ips {
		if !(ip.IsLoopback() || ip.IsPrivate()) {
			return false
		}
	}
	return true
}

// Outcome is what one post came to.
type Outcome struct {
	Status     int           // HTTP status, 0 when no answer came
	Code       string        // the host's error code, the status word, or "network"
	Done       bool          // accepted or duplicate
	Duplicate  bool          // the host had the id already
	Retry      bool          // try again later
	RetryAfter time.Duration // 429's Retry-After (0 = use the backoff)
	Err        error         // the network error, when there was one
}

// Post sends one event (§3): POST <url>, Authorization: Bearer <secret>, the event as JSON.
func (h *Host) Post(ctx context.Context, event map[string]any) Outcome {
	body, err := json.Marshal(event)
	if err != nil {
		return Outcome{Code: "bad_notice", Err: err}
	}
	if len(body) > MaxBody {
		return Outcome{Code: "too_large", Err: fmt.Errorf("event is %d bytes, over %d", len(body), MaxBody)}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return Outcome{Code: "network", Err: err}
	}
	req.Header.Set("Authorization", "Bearer "+h.Secret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "rangka-kernel")
	res, err := h.Client.Do(req)
	if err != nil {
		return Outcome{Code: "network", Retry: true, Err: errors.New(scrub(err.Error(), h.Secret))}
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	var ans struct {
		Status string `json:"status"`
		Error  struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(b, &ans)
	out := Outcome{Status: res.StatusCode, Code: ans.Error.Code}
	if out.Code == "" {
		out.Code = strings.ReplaceAll(strings.ToLower(http.StatusText(res.StatusCode)), " ", "_")
	}
	switch {
	case res.StatusCode == http.StatusAccepted || res.StatusCode == http.StatusOK:
		out.Done, out.Duplicate, out.Code = true, ans.Status == "duplicate", ans.Status
	case res.StatusCode == http.StatusTooManyRequests:
		out.Retry = true
		if s, err := strconv.Atoi(strings.TrimSpace(res.Header.Get("Retry-After"))); err == nil && s >= 0 {
			out.RetryAfter = time.Duration(s) * time.Second
		} else if t, err := http.ParseTime(res.Header.Get("Retry-After")); err == nil {
			out.RetryAfter = time.Until(t)
		}
		if out.RetryAfter <= 0 {
			out.RetryAfter = time.Second
		}
		if out.RetryAfter > 6*time.Hour {
			out.RetryAfter = 6 * time.Hour
		}
	case res.StatusCode >= 500:
		out.Retry = true
	}
	return out
}

// scrub keeps the secret out of an error that echoes the request.
func scrub(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}

// Backoff is the wait before attempt n+1 after attempt n failed (§3): 30 s, 2 min, 10 min, 1 h, 6 h, then every 6 h.
func Backoff(attempt int) time.Duration {
	steps := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 6 * time.Hour}
	if attempt < 1 {
		attempt = 1
	}
	if attempt > len(steps) {
		return 6 * time.Hour
	}
	return steps[attempt-1]
}
