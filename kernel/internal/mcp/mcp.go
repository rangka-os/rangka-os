// Package mcp serves the Model Context Protocol endpoint (POST /mcp, JSON-RPC 2.0, Streamable HTTP with
// JSON responses) with the tools fixed in CONTRACTS.md. There are no approve/apply tools: a person approves
// change sets in the Console or the app.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/changeset"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
)

// ProtocolVersion is the newest MCP revision this server speaks.
const ProtocolVersion = "2025-06-18"

var supported = map[string]bool{"2025-06-18": true, "2025-03-26": true, "2024-11-05": true}

// Caller is an authenticated MCP client.
type Caller struct {
	Actor  engine.Actor
	Change changeset.Actor
}

// Handler serves /mcp.
type Handler struct {
	Version string
	Resolve func(r *http.Request) (*Caller, error)
	Engine  func() *engine.Engine
	Changes *changeset.Service
	Meta    func(ctx context.Context, a engine.Actor) (any, error)
	File    func(ctx context.Context, path string) (any, error)
	Status  func(ctx context.Context) any
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Allow", "POST")
		http.Error(w, "this MCP server answers POST requests with JSON (no server-initiated stream)", http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	caller, err := h.Resolve(r)
	if err != nil || caller == nil {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="/.well-known/openid-configuration"`)
		http.Error(w, `{"error":"unauthorized","message":"send Authorization: Bearer <rk_ token or OIDC access token with scope rangka.mcp>"}`, http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var reqs []request
	batch := len(body) > 0 && body[0] == '['
	if batch {
		err = json.Unmarshal(body, &reqs)
	} else {
		var one request
		err = json.Unmarshal(body, &one)
		reqs = []request{one}
	}
	if err != nil {
		writeJSON(w, response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error: " + err.Error(), nil}})
		return
	}
	var out []response
	for _, req := range reqs {
		res := h.handle(r.Context(), caller, req)
		if res != nil {
			out = append(out, *res)
		}
	}
	if len(out) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if batch {
		writeJSON(w, out)
		return
	}
	writeJSON(w, out[0])
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func (h *Handler) handle(ctx context.Context, c *Caller, req request) *response {
	notification := len(req.ID) == 0 || string(req.ID) == "null"
	res := &response{JSONRPC: "2.0", ID: req.ID}
	if req.JSONRPC != "2.0" {
		res.Error = &rpcError{-32600, "jsonrpc must be \"2.0\"", nil}
		return res
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		v := ProtocolVersion
		if supported[p.ProtocolVersion] {
			v = p.ProtocolVersion
		}
		res.Result = map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "rangka-os", "title": "Rangka OS kernel", "version": h.Version},
			"instructions": "Rangka OS: read metadata (rangka_meta, rangka_get_file), query records (rangka_query, rangka_get_record), " +
				"read dashboards and system status, and propose metadata changes as change sets (propose → validate → preview). " +
				"A person approves and applies change sets in the Console; there is no tool for that.",
		}
	case "notifications/initialized", "notifications/cancelled":
		return nil
	case "ping":
		res.Result = map[string]any{}
	case "tools/list":
		res.Result = map[string]any{"tools": tools()}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			res.Error = &rpcError{-32602, "invalid params: " + err.Error(), nil}
			break
		}
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		out, err := h.call(ctx, c, p.Name, p.Arguments)
		if errors.Is(err, errUnknownTool) {
			res.Error = &rpcError{-32602, "unknown tool " + p.Name, nil}
			break
		}
		res.Result = toolResult(out, err)
	default:
		if notification {
			return nil
		}
		res.Error = &rpcError{-32601, "method not found: " + req.Method, nil}
	}
	if notification {
		return nil
	}
	return res
}

// toolResult follows CONTRACTS v0.1 decision 9: content [{type: text, text: <json>}] plus structuredContent;
// errors set isError and carry {error: {code, message}}.
func toolResult(out any, err error) map[string]any {
	if err != nil {
		code, msg := "internal", err.Error()
		body := map[string]any{}
		var ve *engine.ValidationError
		var ce *changeset.Error
		var se *engine.StatusError
		switch {
		case errors.As(err, &ve):
			code = "invalid"
			body["errors"], body["warnings"] = ve.Errors, ve.Warnings
		case errors.As(err, &ce):
			code = ce.Code
			if ce.Detail != nil {
				body["detail"] = ce.Detail
			}
		case errors.As(err, &se):
			code = se.Code
		}
		body["error"] = map[string]any{"code": code, "message": msg}
		b, _ := json.Marshal(body)
		var sc map[string]any
		json.Unmarshal(b, &sc)
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}, "structuredContent": sc, "isError": true}
	}
	b, _ := json.Marshal(out)
	var sc map[string]any
	if json.Unmarshal(b, &sc) != nil {
		sc = map[string]any{"result": out}
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}, "structuredContent": sc, "isError": false}
}

var errUnknownTool = errors.New("unknown tool")

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func tools() []any {
	strp := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	ro := map[string]any{"readOnlyHint": true, "openWorldHint": false}
	return []any{
		map[string]any{"name": "rangka_meta", "title": "Installation metadata", "description": "Apps, models (fields, rules, views), dashboards, theme and layout of this installation (GET /api/v1/meta).", "inputSchema": obj(map[string]any{}), "annotations": ro},
		map[string]any{"name": "rangka_get_file", "title": "Read a .rk file", "description": "Raw .rk text and parsed JSON of a metadata file by change-set path, e.g. apps/crm/models/deal.rk, system.rk, themes/kopi_senja.rk.", "inputSchema": obj(map[string]any{"path": strp("change-set path")}, "path"), "annotations": ro},
		map[string]any{"name": "rangka_query", "title": "Query records", "description": "List records of a model with an optional CEL filter (stage != 'won'), sort (-field) and limit.", "inputSchema": obj(map[string]any{
			"model": strp("model name"), "filter": strp("CEL expression"), "sort": strp("field or -field"), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 500}}, "model"), "annotations": ro},
		map[string]any{"name": "rangka_get_record", "title": "Read a record", "description": "One record with its child rows.", "inputSchema": obj(map[string]any{"model": strp("model name"), "name": strp("record name")}, "model", "name"), "annotations": ro},
		map[string]any{"name": "rangka_dashboard", "title": "Dashboard numbers", "description": "Computed widgets of a dashboard over the last period days.", "inputSchema": obj(map[string]any{"name": strp("dashboard name"), "period": map[string]any{"type": "integer", "minimum": 1}}, "name"), "annotations": ro},
		map[string]any{"name": "rangka_system_status", "title": "System status", "description": "Kernel version, capabilities (db, cache, queue, scheduler, realtime …), apps and counts.", "inputSchema": obj(map[string]any{}), "annotations": ro},
		map[string]any{"name": "rangka_changeset_propose", "title": "Propose a change set", "description": "Propose metadata changes. ops: [{op: create|replace|patch|delete, path: apps/<app>/models/<m>.rk | system.rk | config.rk | themes/<t>.rk | …, content: full .rk text (create/replace), patch: RFC 6902 list (patch)}]. Returns the change set with layers and risk; a person must approve it.",
			"inputSchema": obj(map[string]any{"title": strp("short title"), "reason": strp("why"), "ops": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "object",
				"properties": map[string]any{"op": map[string]any{"enum": []string{"create", "replace", "patch", "delete"}}, "path": strp("change-set path"), "content": strp(".rk text"), "patch": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}},
				"required":   []string{"op", "path"}}}}, "title", "ops"), "annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "openWorldHint": false}},
		map[string]any{"name": "rangka_changeset_validate", "title": "Validate a change set", "description": "Run the validator on a copy of the metadata with the change applied.", "inputSchema": obj(map[string]any{"id": strp("change set id (cs_…)")}, "id")},
		map[string]any{"name": "rangka_changeset_preview", "title": "Preview a change set", "description": "Dry run: the SQL migrations that would run and how many records the touched models hold.", "inputSchema": obj(map[string]any{"id": strp("change set id")}, "id")},
		map[string]any{"name": "rangka_changeset_status", "title": "Change set status", "description": "The change set with its status and history.", "inputSchema": obj(map[string]any{"id": strp("change set id")}, "id"), "annotations": ro},
	}
}

func sarg(args map[string]any, k string) string {
	if v, ok := args[k].(string); ok {
		return v
	}
	return ""
}

func iarg(args map[string]any, k string) int {
	if v, ok := args[k].(float64); ok {
		return int(v)
	}
	return 0
}

func (h *Handler) call(ctx context.Context, c *Caller, name string, args map[string]any) (any, error) {
	e := h.Engine()
	switch name {
	case "rangka_meta":
		return h.Meta(ctx, c.Actor)
	case "rangka_get_file":
		return h.File(ctx, sarg(args, "path"))
	case "rangka_query":
		limit := iarg(args, "limit")
		if limit == 0 {
			limit = 50
		}
		rows, total, err := e.List(ctx, c.Actor, sarg(args, "model"), engine.ListOpts{Filter: sarg(args, "filter"), Sort: sarg(args, "sort"), Limit: limit})
		if err != nil {
			return nil, err
		}
		return map[string]any{"data": rows, "total": total}, nil
	case "rangka_get_record":
		return e.Get(ctx, c.Actor, sarg(args, "model"), sarg(args, "name"))
	case "rangka_dashboard":
		d, _, err := e.Dashboard(ctx, c.Actor, sarg(args, "name"), iarg(args, "period"))
		return d, err
	case "rangka_system_status":
		return h.Status(ctx), nil
	case "rangka_changeset_propose":
		a := c.Change
		a.Kind = "ai" // proposals through MCP come from an agent unless the arguments name another author
		body := map[string]any{"title": args["title"], "ops": args["ops"]}
		if r, ok := args["reason"]; ok {
			body["reason"] = r
		}
		if au, ok := args["author"]; ok {
			body["author"] = au
		}
		return h.Changes.Propose(ctx, a, body)
	case "rangka_changeset_validate":
		return h.Changes.Validate(ctx, c.Change, sarg(args, "id"))
	case "rangka_changeset_preview":
		return h.Changes.Preview(ctx, c.Change, sarg(args, "id"))
	case "rangka_changeset_status":
		return h.Changes.Get(ctx, c.Change.Org, sarg(args, "id"))
	}
	return nil, fmt.Errorf("%w %s", errUnknownTool, name)
}

// RequireScope is used by the API to accept OIDC tokens on /mcp only with the rangka.mcp scope.
func RequireScope(p *auth.Principal) bool {
	if p == nil {
		return false
	}
	if p.Via != "oidc" {
		return true
	}
	for _, s := range splitFields(p.Scope) {
		if s == "rangka.mcp" {
			return true
		}
	}
	return false
}

func splitFields(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
