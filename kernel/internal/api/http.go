// Package api is the HTTP surface of the kernel: REST /api/v1, auth endpoints, the OIDC provider routes,
// SSE events and static files. Routing uses net/http patterns (Go 1.22+).
package api

import (
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

// fail maps engine errors to HTTP: 422 validation, StatusError codes, 404, else 500.
func fail(w http.ResponseWriter, err error) {
	var ve *engine.ValidationError
	var se *engine.StatusError
	switch {
	case errors.As(err, &ve):
		writeJSON(w, http.StatusUnprocessableEntity, ve)
	case errors.As(err, &se):
		writeError(w, se.Status, se.Code, se.Msg)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
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
