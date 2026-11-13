// Package mcp serves the Model Context Protocol endpoint (POST /mcp, JSON-RPC 2.0, Streamable HTTP with
// JSON responses) with the tools fixed in CONTRACTS.md. There are no apply tools: a person approves change sets
// in the Console or the app. Agents can list document approvals, but rangka_approve refuses non-person callers.
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
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/skills"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// ProtocolVersion is the newest MCP revision this server speaks.
const ProtocolVersion = "2025-06-18"

var supported = map[string]bool{"2025-06-18": true, "2025-03-26": true, "2024-11-05": true}

// Caller is an authenticated MCP client.
type Caller struct {
	Actor  engine.Actor
	Change changeset.Actor
	// Principal is auth.Principal.Kind: chat for the token the chat relay minted (contract 0020 §2). It is
	// reported as _meta "rangka/principal" on initialize and tools/list, so the assistant can refuse a token
	// that is not a chat the kernel admitted and counted.
	Principal string
}

// Handler serves /mcp.
type Handler struct {
	Version string
	Resolve func(r *http.Request) (*Caller, error)
	Engine  func() *engine.Engine
	Changes *changeset.Service
	Meta    func(ctx context.Context, a engine.Actor) (any, error)
	File    func(ctx context.Context, path string) (any, error)
	Status  func(ctx context.Context, org string) any
	// Catalogue and Skills serve contract 0035: what exists for this site, and the skills agents load.
	Catalogue func() any
	Skills    func() *skills.Library
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
	// queries made while serving this request run with rangka.org set (row-level security backstop)
	r = r.WithContext(pg.WithOrg(r.Context(), caller.Actor.Org))
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
			"_meta":           map[string]any{"rangka/principal": c.Principal},
			"instructions": "Rangka OS: read metadata (rangka_meta, rangka_get_file), query records (rangka_query, rangka_get_record), " +
				"read dashboards and system status, and propose metadata changes as change sets (propose → validate → preview). " +
				"A person approves and applies change sets in the Console; there is no tool for that. " +
				"Agents can list document approvals (rangka_approvals_list); approving or rejecting one needs a person.",
		}
	case "notifications/initialized", "notifications/cancelled":
		return nil
	case "ping":
		res.Result = map[string]any{}
	case "tools/list":
		list := tools()
		if c.Change.Assistant {
			kept := []any{}
			for _, t := range list {
				if m, ok := t.(map[string]any); ok {
					if name, ok := m["name"].(string); ok && assistantTools[name] {
						kept = append(kept, t)
					}
				}
			}
			list = kept
		}
		res.Result = map[string]any{"tools": list, "_meta": map[string]any{"rangka/principal": c.Principal}}
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
		if c.Change.Assistant && !assistantTools[p.Name] {
			res.Error = &rpcError{-32001, auth.AIForbiddenMessage, map[string]any{"code": "ai_forbidden", "tool": p.Name}}
			break
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
// errors set isError and carry {error: {code, message}}; a refused permission adds model and action, as the
// REST 403 body does.
func toolResult(out any, err error) map[string]any {
	if err != nil {
		code, msg := "internal", err.Error()
		body := map[string]any{}
		var ve *engine.ValidationError
		var ce *changeset.Error
		var se *engine.StatusError
		var pe *engine.PermissionError
		errBody := map[string]any{}
		switch {
		case errors.As(err, &pe):
			code, msg = "permission_denied", pe.Error()
			errBody["model"], errBody["action"] = pe.Model, pe.Action
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
		errBody["code"], errBody["message"] = code, msg
		body["error"] = errBody
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

// assistantTools are the tools the embedded assistant (auth Via ai) may call: read, and propose change sets
// up to preview. Anything else, rangka_approve included, is a JSON-RPC error with data.code ai_forbidden.
var assistantTools = map[string]bool{
	"rangka_meta": true, "rangka_get_file": true, "rangka_query": true, "rangka_get_record": true, "rangka_dashboard": true,
	"rangka_system_status": true, "rangka_changeset_propose": true, "rangka_changeset_validate": true,
	"rangka_changeset_preview": true, "rangka_changeset_status": true, "rangka_approvals_list": true,
	"rangka_catalogue": true, "rangka_skills": true, "rangka_skill": true, "rangka_skill_file": true,
}

// ToolNames returns the names of every tool this server lists (rangka skills check rejects others).
func ToolNames() map[string]bool {
	out := map[string]bool{}
	for _, t := range tools() {
		out[t.(map[string]any)["name"].(string)] = true
	}
	return out
}

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
		map[string]any{"name": "rangka_changeset_propose", "title": "Propose a change set", "description": "Propose metadata changes. ops: [{op: create|replace|patch|delete, path: modules/<module>/models/<m>.rk | apps/<app>/app.rk | system.rk | config.rk | themes/<t>.rk | …, content: full .rk text (create/replace), patch: RFC 6902 list (patch)}]. Returns the change set with layers and risk; a person must approve it.",
			"inputSchema": obj(map[string]any{"title": strp("short title"), "reason": strp("why"), "ops": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "object",
				"properties": map[string]any{"op": map[string]any{"enum": []string{"create", "replace", "patch", "delete"}}, "path": strp("change-set path"), "content": strp(".rk text"), "patch": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}},
				"required":   []string{"op", "path"}}}}, "title", "ops"), "annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "openWorldHint": false}},
		map[string]any{"name": "rangka_changeset_validate", "title": "Validate a change set", "description": "Run the validator on a copy of the metadata with the change applied.", "inputSchema": obj(map[string]any{"id": strp("change set id (cs_…)")}, "id")},
		map[string]any{"name": "rangka_changeset_preview", "title": "Preview a change set", "description": "Dry run: the SQL migrations that would run and how many records the touched models hold.", "inputSchema": obj(map[string]any{"id": strp("change set id")}, "id")},
		map[string]any{"name": "rangka_changeset_status", "title": "Change set status", "description": "The change set with its status and history.", "inputSchema": obj(map[string]any{"id": strp("change set id")}, "id"), "annotations": ro},
		map[string]any{"name": "rangka_approvals_list", "title": "List approvals", "description": "Document approval requests of this organisation, newest first, each with can_decide for the caller. Filter by state (pending, approved, rejected, void), model and record name.",
			"inputSchema": obj(map[string]any{"state": map[string]any{"enum": []string{"pending", "approved", "rejected", "void"}}, "model": strp("model name"), "name": strp("record name")}), "annotations": ro},
		map[string]any{"name": "rangka_catalogue", "title": "Catalogue", "description": "Every app, module and connector this site can use, with state (installed, available, not_in_plan); per app the installed modules it shares and the modules it adds. Read it before naming an app (contract 0035).", "inputSchema": obj(map[string]any{}), "annotations": ro},
		map[string]any{"name": "rangka_skills", "title": "List skills", "description": "Rangka skills (Agent Skills): name, description and source of each. Load one with rangka_skill before working in its area.", "inputSchema": obj(map[string]any{}), "annotations": ro},
		map[string]any{"name": "rangka_skill", "title": "Load a skill", "description": "One skill's SKILL.md instructions and the list of its other files.", "inputSchema": obj(map[string]any{"name": strp("skill name, e.g. rangka-compose")}, "name"), "annotations": ro},
		map[string]any{"name": "rangka_skill_file", "title": "Read a skill file", "description": "One file of a skill (a reference), by its path inside the skill.", "inputSchema": obj(map[string]any{"name": strp("skill name"), "path": strp("path inside the skill, e.g. references/apps.md")}, "name", "path"), "annotations": ro},
		map[string]any{"name": "rangka_approve", "title": "Approve or reject", "description": "Decide an approval request. Needs a person (browser session): agent and API-token callers get human_required. A reject needs a reason.",
			"inputSchema": obj(map[string]any{"id": strp("approval id (apr_…)"), "decision": map[string]any{"enum": []string{"approve", "reject"}}, "reason": strp("why (required for reject)")}, "id", "decision"),
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "openWorldHint": false}},
	}
}

// approvalItem is a request as MCP shows it: the stored fields plus whether the caller may decide it now.
func approvalItem(a engine.Actor, ap *store.Approval) map[string]any {
	b, _ := json.Marshal(ap)
	out := map[string]any{}
	json.Unmarshal(b, &out)
	if ap.Label == nil {
		out["label"] = map[string]any{}
	}
	out["can_decide"] = engine.CanDecide(a, ap)
	return out
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
		return h.Status(ctx, c.Actor.Org), nil
	case "rangka_catalogue":
		if h.Catalogue == nil {
			return nil, errors.New("no catalogue on this server")
		}
		return h.Catalogue(), nil
	case "rangka_skills":
		list := []any{}
		for _, sk := range h.library().Skills {
			list = append(list, map[string]any{"name": sk.Name, "description": sk.Description, "source": sk.Source})
		}
		return map[string]any{"skills": list}, nil
	case "rangka_skill":
		sk := h.library().Get(sarg(args, "name"))
		if sk == nil {
			return nil, fmt.Errorf("no skill %q (rangka_skills lists them)", sarg(args, "name"))
		}
		files := sk.Files
		if files == nil {
			files = []string{}
		}
		return map[string]any{"name": sk.Name, "description": sk.Description, "metadata": sk.Metadata, "source": sk.Source, "instructions": sk.Body, "files": files}, nil
	case "rangka_skill_file":
		sk := h.library().Get(sarg(args, "name"))
		if sk == nil {
			return nil, fmt.Errorf("no skill %q (rangka_skills lists them)", sarg(args, "name"))
		}
		text, err := sk.ReadFile(sarg(args, "path"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"name": sk.Name, "path": sarg(args, "path"), "text": text}, nil
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
		if org, ok := args["org"]; ok {
			body["org"] = org // checked against the caller's organisation (org_mismatch)
		}
		return h.Changes.Propose(ctx, a, body)
	case "rangka_changeset_validate":
		return h.Changes.Validate(ctx, c.Change, sarg(args, "id"))
	case "rangka_changeset_preview":
		return h.Changes.Preview(ctx, c.Change, sarg(args, "id"))
	case "rangka_changeset_status":
		return h.Changes.Get(ctx, c.Change.Org, sarg(args, "id"))
	case "rangka_approvals_list":
		rows, total, err := e.ListApprovals(ctx, c.Actor, store.ApprovalFilter{State: sarg(args, "state"), Model: sarg(args, "model"), Name: sarg(args, "name"), Limit: 50})
		if err != nil {
			return nil, err
		}
		items := make([]map[string]any, 0, len(rows))
		for i := range rows {
			items = append(items, approvalItem(c.Actor, &rows[i]))
		}
		return map[string]any{"data": items, "total": total}, nil
	case "rangka_approve":
		var d *engine.Decision
		var err error
		switch sarg(args, "decision") {
		case "approve":
			d, err = e.Approve(ctx, c.Actor, sarg(args, "id"))
		case "reject":
			d, err = e.Reject(ctx, c.Actor, sarg(args, "id"), sarg(args, "reason"))
		default:
			return nil, &engine.StatusError{Status: 400, Code: "bad_request", Msg: "decision must be approve or reject"}
		}
		if err != nil {
			return nil, err
		}
		out := map[string]any{"approval": approvalItem(c.Actor, &d.Approval)}
		if d.Record != nil {
			out["record"] = d.Record
		}
		if d.Action != nil {
			out["action"] = d.Action
		}
		if d.Flow != nil {
			out["flow"] = d.Flow
		}
		return out, nil
	}
	return nil, fmt.Errorf("%w %s", errUnknownTool, name)
}

// RequireScope is used by the API to accept OIDC and assistant tokens on /mcp only with the rangka.mcp scope. An
// assistant token (Via "ai") may instead carry the assistant scope, which is its MCP credential; that scope opens
// /mcp for no other principal, so an OIDC client given it still needs rangka.mcp (contract 0045 §3).
func RequireScope(p *auth.Principal) bool {
	if p == nil {
		return false
	}
	if p.Via != "oidc" && p.Via != "ai" {
		return true
	}
	for _, s := range splitFields(p.Scope) {
		if s == "rangka.mcp" || (s == auth.ScopeAssistant && p.Via == "ai") {
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

// library returns the served skills, the embedded platform ones when the server was given none.
func (h *Handler) library() *skills.Library {
	if h.Skills != nil {
		if l := h.Skills(); l != nil {
			return l
		}
	}
	return skills.Load("", "")
}
