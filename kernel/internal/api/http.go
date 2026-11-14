// Package api is the HTTP surface of the kernel: REST /api/v1, auth endpoints, the OIDC provider routes,
// SSE events and static files. Routing uses net/http patterns (Go 1.22+).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// writeJSON writes v with status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

// errorBody is the JSON of a non-validation error.
type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	var b errorBody
	b.Error.Code, b.Error.Message = code, msg
	writeJSON(w, status, b)
}

// permissionDenied writes the 403 body: {"error": {"code": "permission_denied", "message", "model", "action"}}.
func permissionDenied(w http.ResponseWriter, pe *engine.PermissionError) {
	writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]any{
		"code": "permission_denied", "message": pe.Error(), "model": pe.Model, "action": pe.Action}})
}

// fail maps engine errors to HTTP: 422 validation, 403 permission, StatusError codes, 404, else 500.
func fail(w http.ResponseWriter, err error) {
	status, body := failBody(err)
	writeJSON(w, status, body)
}

// failBody is the status and body fail writes for err.
func failBody(err error) (int, any) {
	var ve *engine.ValidationError
	var se *engine.StatusError
	var pe *engine.PermissionError
	errOf := func(code, msg string) any {
		var b errorBody
		b.Error.Code, b.Error.Message = code, msg
		return b
	}
	switch {
	case errors.As(err, &pe):
		return http.StatusForbidden, map[string]any{"error": map[string]any{
			"code": "permission_denied", "message": pe.Error(), "model": pe.Model, "action": pe.Action}}
	case errors.As(err, &ve):
		return http.StatusUnprocessableEntity, ve
	case errors.As(err, &se):
		return se.Status, errOf(se.Code, se.Msg)
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound, errOf("not_found", err.Error())
	// the request ended while it waited (a metadata reload, a lock): not a server fault (card t_0214482d A4)
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusServiceUnavailable, errOf("timeout", "Sistem sedang sibuk, coba lagi sebentar lagi.")
	case errors.Is(err, context.Canceled):
		return 499, errOf("client_closed", "Permintaan dibatalkan.")
	default:
		return http.StatusInternalServerError, errOf("internal", err.Error())
	}
}

func readJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, v)
}

// readObject reads a JSON object body with numbers kept as float64 (as JSON clients send them).
func readObject(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	var m map[string]any
	if err := readJSON(r, &m); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "body must be a JSON object: "+err.Error())
		return nil, false
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, true
}

func intParam(r *http.Request, name string, def int) int {
	if v := r.URL.Query().Get(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// recordOut adds warnings to a record response.
func recordOut(res *engine.Result) map[string]any {
	out := map[string]any{}
	for k, v := range res.Record {
		out[k] = v
	}
	if len(res.Warnings) > 0 {
		out["_warnings"] = res.Warnings
	}
	return out
}
