package api

import (
	"net/http"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// openAPI is GET /api/v1/openapi.json (contract 0075 §6): an OpenAPI 3.1 document of the record API, generated from
// the loaded models and filtered by what the caller may do. The document differs per caller, so it is never cached.
func (s *Server) openAPI(w http.ResponseWriter, r *http.Request, c *caller) {
	doc := BuildOpenAPI(s.Engine, c.actor, func(action, model string) bool {
		return !c.p.IsIntegrationKey() || c.p.APIAllows(action, model)
	})
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, doc)
}

// BuildOpenAPI builds the document for actor a. scopeOK(action, model) says whether an integration key's scopes allow
// "read" or "write" on a model (always true for a person or a plain token). Encoding the map sorts its keys, so the output is stable.
func BuildOpenAPI(e *engine.Engine, a engine.Actor, scopeOK func(action, model string) bool) map[string]any {
	reg := e.Reg()
	paths := map[string]any{}
	schemas := map[string]any{
		"Error": obj(map[string]any{"error": obj(map[string]any{"code": typ("string"), "message": typ("string")}, nil)}, []string{"error"}),
	}
	errs := func(codes ...string) map[string]any {
		out := map[string]any{}
		for _, code := range codes {
			out[code] = map[string]any{"description": errText[code], "content": jsonBody(ref("Error"))}
		}
		return out
	}
	resp := func(code, desc string, schema any, more map[string]any) map[string]any {
		out := map[string]any{code: map[string]any{"description": desc, "content": jsonBody(schema)}}
		for k, v := range more {
			out[k] = v
		}
		return out
	}
	nameParam := map[string]any{"name": "name", "in": "path", "required": true, "schema": typ("string")}
	for _, m := range reg.InstalledModels() {
		if m.IsChild {
			continue // child rows travel inside their parent
		}
		can := e.Allowed(a, m.Name)
		read := can["read"] && scopeOK("read", m.Name)
		write := scopeOK("write", m.Name)
		if !read && !(write && (can["create"] || can["write"] || can["delete"])) {
			continue
		}
		base := "/api/v1/r/" + m.Name
		item := base + "/{name}"
		tag := []string{m.Name}
		if read {
			schemas[m.Name] = modelSchema(e, a, reg, m, false)
		}
		if write && (can["create"] || can["write"]) {
			schemas[m.Name+"Input"] = modelSchema(e, a, reg, m, true)
		}
		coll, one := map[string]any{}, map[string]any{}
		if read {
			coll["get"] = op(m, "list", "List "+m.Name+" records", tag, []any{
				query("limit", "integer"), query("offset", "integer"), query("q", "string"), query("filter", "string"), query("sort", "string")},
				nil, resp("200", "A page of records", obj(map[string]any{"data": arr(ref(m.Name)), "total": typ("integer")}, nil), errs("401", "403")))
			one["get"] = op(m, "get", "Get one "+m.Name+" record", tag, []any{nameParam}, nil, resp("200", "The record", ref(m.Name), errs("401", "403", "404")))
		}
		if write && can["create"] {
			coll["post"] = op(m, "create", "Create a "+m.Name+" record", tag, nil, ref(m.Name+"Input"), resp("201", "The new record", ref(m.Name), errs("401", "403", "422")))
		}
		if write && can["write"] {
			one["patch"] = op(m, "update", "Update a "+m.Name+" record", tag, []any{nameParam}, ref(m.Name+"Input"), resp("200", "The record", ref(m.Name), errs("401", "403", "404", "422")))
		}
		if write && can["delete"] {
			one["delete"] = op(m, "delete", "Delete a "+m.Name+" record", tag, []any{nameParam}, nil, map[string]any{"204": map[string]any{"description": "Deleted"}, "403": errs("403")["403"], "404": errs("404")["404"]})
		}
		if len(coll) > 0 {
			paths[base] = coll
		}
		if len(one) > 0 {
			paths[item] = one
		}
		if write && m.IsSubmittable {
			for _, verb := range []string{"submit", "cancel"} {
				if can[verb] {
					paths[item+"/"+verb] = map[string]any{"post": op(m, verb, verb+" a "+m.Name+" record", tag, []any{nameParam}, nil, resp("200", "The record", ref(m.Name), errs("401", "403", "404", "422")))}
				}
			}
		}
		if write && can["write"] {
			for _, act := range m.Actions {
				paths[item+"/actions/"+act.Name] = map[string]any{"post": op(m, "action."+act.Name, "Run the "+act.Name+" action on a "+m.Name+" record", tag, []any{nameParam}, nil, resp("200", "The result", obj(nil, nil), errs("401", "403", "404", "422")))}
			}
		}
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{"title": "Rangka API", "version": "1", "description": "Record API of this business, limited to what the caller's role and key scopes allow (contract 0075).",
			"x-rangka-contract": "0075"},
		"servers":    []any{map[string]any{"url": "/"}},
		"paths":      paths,
		"components": map[string]any{"schemas": schemas, "securitySchemes": map[string]any{"bearerAuth": map[string]any{"type": "http", "scheme": "bearer", "description": "An rk_ API token or integration key."}}},
		"security":   []any{map[string]any{"bearerAuth": []any{}}},
	}
}

var errText = map[string]string{"401": "Not signed in", "403": "Not allowed for this key or role", "404": "Not found", "422": "Validation failed"}

func typ(t string) map[string]any { return map[string]any{"type": t} }
func ref(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}
func arr(items any) map[string]any { return map[string]any{"type": "array", "items": items} }
func obj(props map[string]any, required []string) map[string]any {
	o := map[string]any{"type": "object"}
	if props != nil {
		o["properties"] = props
	}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}
func jsonBody(schema any) map[string]any {
	return map[string]any{"application/json": map[string]any{"schema": schema}}
}
func query(name, t string) map[string]any {
	return map[string]any{"name": name, "in": "query", "schema": typ(t)}
}

func op(m *meta.Model, id, summary string, tags []string, params []any, body any, responses map[string]any) map[string]any {
	o := map[string]any{"operationId": m.Name + "." + id, "summary": summary, "tags": tags, "responses": responses}
	if params != nil {
		o["parameters"] = params
	}
	if body != nil {
		o["requestBody"] = map[string]any{"required": true, "content": jsonBody(body)}
	}
	return o
}

// modelSchema describes a model's record for actor a: fields hidden from a are left out (contract 0027), and a read-only
// field is marked readOnly. input leaves out what a client cannot set: computed, read-only and read-only-for fields.
func modelSchema(e *engine.Engine, a engine.Actor, reg *meta.Registry, m *meta.Model, input bool) map[string]any {
	props := map[string]any{}
	var required []string
	if !input {
		props["name"] = map[string]any{"type": "string", "readOnly": true}
	}
	for _, f := range m.Fields {
		if e.FieldHidden(a, m, f) {
			continue
		}
		ro := f.ReadOnly || f.Compute != "" || e.FieldReadOnlyFor(a, m, f)
		if input && ro {
			continue
		}
		p := fieldSchema(reg, f, func(cm *meta.Model, cf *meta.Field) bool { return e.FieldHidden(a, cm, cf) })
		if ro && !input {
			p["readOnly"] = true
		}
		if l := f.Label.Text(); l != "" {
			p["title"] = l
		}
		props[f.Name] = p
		if input && f.Required && f.Default == "" {
			required = append(required, f.Name)
		}
	}
	return obj(props, required)
}

// hide, when set, tells which child fields of a table are hidden from the caller (contract 0027).
func fieldSchema(reg *meta.Registry, f *meta.Field, hide func(*meta.Model, *meta.Field) bool) map[string]any {
	switch f.Type {
	case "int", "rating":
		return typ("integer")
	case "float", "percent", "currency", "money":
		return typ("number")
	case "check":
		return typ("boolean")
	case "date":
		return map[string]any{"type": "string", "format": "date"}
	case "datetime":
		return map[string]any{"type": "string", "format": "date-time"}
	case "json", "geo", "daterange":
		return map[string]any{}
	case "multiselect":
		return arr(typ("string"))
	case "select":
		p := typ("string")
		if len(f.Options) > 0 {
			vals := make([]any, 0, len(f.Options))
			for _, o := range f.Options {
				vals = append(vals, o.Value)
			}
			p["enum"] = vals
		}
		return p
	case "table":
		if f.Of != "" && reg.Model(f.Of) != nil {
			items := obj(map[string]any{}, nil)
			props := items["properties"].(map[string]any)
			child := reg.Model(f.Of)
			for _, cf := range child.Fields {
				if hide != nil && hide(child, cf) {
					continue
				}
				props[cf.Name] = fieldSchema(reg, cf, hide)
			}
			return arr(items)
		}
		return arr(obj(nil, nil))
	case "link":
		p := typ("string")
		if f.To != "" {
			p["description"] = "Name of a " + f.To + " record"
		}
		return p
	}
	return typ("string")
}
