package mail

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// NotifyDriver is the driver a system.rk notify.email block selects: "smtp" when driver is smtp, enabled is not false
// and host is set; else "log" (also when the block is missing). problem says why an smtp block falls back to log (""
// when it does not).
func NotifyDriver(cfg map[string]any) (driver, problem string) {
	if cfg == nil || cfg["driver"] != "smtp" {
		return "log", ""
	}
	if cfg["enabled"] == false {
		return "log", "notify.email.enabled is false"
	}
	if h, _ := cfg["host"].(string); strings.TrimSpace(h) == "" {
		return "log", "notify.email.driver is smtp but host is not set"
	}
	return "smtp", ""
}

// NotifyConfig reads a notify.email block whose NotifyDriver is smtp into what Send takes: the server (port 587 when
// unset; port 465 is implicit TLS, any other port requires STARTTLS), the credentials and the From address (the
// username when from is unset and the username is an address). resolve turns a "${VAR}" value into the secret it names
// (ok false: unset); a literal comes back as it is. Errors wrap ErrConfig.
func NotifyConfig(cfg map[string]any, resolve func(string) (string, bool)) (Server, Credentials, Address, error) {
	var (
		srv  Server
		cred Credentials
		from Address
	)
	if d, problem := NotifyDriver(cfg); d != "smtp" {
		if problem == "" {
			problem = "notify.email.driver is not smtp"
		}
		return srv, cred, from, fmt.Errorf("%w: %s", ErrConfig, problem)
	}
	get := func(k string) (string, error) {
		raw, ok := cfg[k]
		if !ok || raw == nil {
			return "", nil
		}
		s := fmt.Sprint(raw)
		v, ok := resolve(s)
		if !ok {
			return "", fmt.Errorf("%w: notify.email.%s %s is not set", ErrConfig, k, s)
		}
		return v, nil
	}
	var err error
	if srv.Host, err = get("host"); err != nil {
		return srv, cred, from, err
	}
	if cred.Username, err = get("username"); err != nil {
		return srv, cred, from, err
	}
	if cred.Password, err = get("password"); err != nil {
		return srv, cred, from, err
	}
	if cred.Username != "" && cred.Password == "" {
		return srv, cred, from, fmt.Errorf("%w: notify.email.username is set without a password", ErrConfig)
	}
	switch p := cfg["port"].(type) {
	case nil:
		srv.Port = 587
	case int:
		srv.Port = p
	case int64:
		srv.Port = int(p)
	case float64:
		srv.Port = int(p)
	case string:
		if srv.Port, err = strconv.Atoi(p); err != nil {
			return srv, cred, from, fmt.Errorf("%w: notify.email.port %q is not a number", ErrConfig, p)
		}
	}
	if srv.Port <= 0 || srv.Port > 65535 {
		return srv, cred, from, fmt.Errorf("%w: notify.email.port %d is out of range", ErrConfig, srv.Port)
	}
	srv.Security = "starttls"
	if srv.Port == 465 {
		srv.Security = "tls"
	}
	f, err := get("from")
	if err != nil {
		return srv, cred, from, err
	}
	if f == "" {
		f = cred.Username
	}
	list, err := ParseAddresses(f)
	if err != nil || len(list) != 1 {
		if f == "" {
			return srv, cred, from, fmt.Errorf("%w: notify.email.from is not set", ErrConfig)
		}
		return srv, cred, from, fmt.Errorf("%w: notify.email.from %q is not one e-mail address", ErrConfig, f)
	}
	return srv, cred, list[0], nil
}

// Permanent reports whether a Send error will happen again on a retry: ErrConfig, ErrAuth, ErrTLSRequired, or a
// 5xx reply (a refused recipient). Network errors and 4xx replies are worth retrying.
func Permanent(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrConfig) || errors.Is(err, ErrAuth) || errors.Is(err, ErrTLSRequired) {
		return true
	}
	var re *ReplyError
	return errors.As(err, &re) && re.Code >= 500
}
