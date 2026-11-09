package api

import (
	"errors"
	"net/http"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/secrets"
)

// Connector secrets, write-only (contract 0017 §3). GET reports per field whether a value is set, never the value;
// PUT and DELETE change one field for the caller's org. Only a person who is the org's owner, or holds a role named in
// config.rk connector_auth:, reaches these routes. The assistant and OIDC helpers never do, and API tokens may read
// but not write (no token carries the secrets:write scope yet).

const msgConnectorAuthForbidden = "Kunci penghubung hanya bisa diatur oleh pemilik atau peran yang diberi izin connector_auth"

// mayManageSecrets is the role rule: owner, or a role listed in config.rk connector_auth.
func (s *Server) mayManageSecrets(c *caller) bool {
	if c.actor.Org == "" {
		return false
	}
	allowed := map[string]bool{"owner": true}
	for _, r := range meta.StrList(s.Engine.Reg().SiteConfig("connector_auth")) {
		allowed[r] = true
	}
	for _, r := range c.actor.Roles {
		if allowed[r] {
			return true
		}
	}
	return false
}

// connectorAuthGuard checks the caller and the connector, and answers when either is refused.
func (s *Server) connectorAuthGuard(w http.ResponseWriter, c *caller, write bool) bool {
	switch c.p.Via {
	case "ai", "oidc": // agents and helpers never reach connector secrets (§3, §4)
		writeError(w, 403, "ai_forbidden", "Raka dan helper tidak bisa membaca atau mengatur kunci penghubung; minta pemilik usaha.")
		return false
	}
	if write && !c.p.Human() { // an API token needs secrets:write, which no token carries yet
		writeError(w, 403, "forbidden", msgConnectorAuthForbidden)
		return false
	}
	if !s.mayManageSecrets(c) {
		writeError(w, 403, "forbidden", msgConnectorAuthForbidden)
		return false
	}
	if s.Secrets == nil {
		writeError(w, 501, "secrets_disabled", "secrets are not configured on this kernel")
		return false
	}
	return true
}

func (s *Server) connectorExists(w http.ResponseWriter, name string) bool {
	if s.Engine.Reg().Connectors[name] == nil {
		writeError(w, 404, "not_found", "no connector "+name)
		return false
	}
	return true
}

func (s *Server) getConnectorAuth(w http.ResponseWriter, r *http.Request, c *caller) {
	if !s.connectorAuthGuard(w, c, false) {
		return
	}
	name := r.PathValue("connector")
	if !s.connectorExists(w, name) {
		return
	}
	list, err := s.Secrets.Status(r.Context(), c.actor.Org, name)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"connector": name, "org": c.actor.Org, "driver": s.Secrets.Driver(),
		"writable": s.Secrets.Writable(), "fields": list})
}

func (s *Server) putConnectorAuth(w http.ResponseWriter, r *http.Request, c *caller) {
	if !s.connectorAuthGuard(w, c, true) {
		return
	}
	name, field := r.PathValue("connector"), r.PathValue("field")
	if !s.connectorExists(w, name) {
		return
	}
	var body struct {
		Value *string `json:"value"`
	}
	if err := readJSON(r, &body); err != nil || body.Value == nil {
		writeError(w, 400, "bad_json", `send {"value": "…"}`)
		return
	}
	_, err := s.Secrets.Set(r.Context(), c.actor.Org, name, field, *body.Value, c.actor.ID)
	if secretsFail(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteConnectorAuth(w http.ResponseWriter, r *http.Request, c *caller) {
	if !s.connectorAuthGuard(w, c, true) {
		return
	}
	name, field := r.PathValue("connector"), r.PathValue("field")
	if !s.connectorExists(w, name) {
		return
	}
	if secretsFail(w, s.Secrets.Delete(r.Context(), c.actor.Org, name, field, c.actor.ID)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// secretsFail answers a secrets error; the message never repeats the value.
func secretsFail(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, secrets.ErrReadOnlyDriver):
		writeError(w, 409, "secrets_read_only_driver", "Kunci penghubung di situs ini diatur lewat system.rk connector_auth (driver env, file atau vault), bukan dari sini.")
	case errors.Is(err, secrets.ErrKeyMissing):
		writeError(w, 409, "secrets_key_missing", "Kernel belum punya kunci enkripsi (RANGKA_MASTER_KEY); jalankan rangka keygen lalu mulai ulang.")
	case errors.Is(err, secrets.ErrUnknownField):
		writeError(w, 404, "unknown_field", "Penghubung ini tidak punya field itu.")
	case errors.Is(err, secrets.ErrEmptyValue):
		writeError(w, 400, "empty_value", "Nilai tidak boleh kosong; untuk menghapus pakai DELETE.")
	default:
		writeError(w, 500, "internal", "could not store the secret")
	}
	return true
}
