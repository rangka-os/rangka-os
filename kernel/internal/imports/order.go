package imports

import (
	"bytes"
	"encoding/json"
	"sort"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// previewOrder is the key order of preview records in JSON: the model's field order, and the child model's
// for the lines table. rk_imports.preview is jsonb, which keeps no key order, so it is applied on output.
type previewOrder struct {
	fields     []string
	lineField  string
	lineFields []string
}

func fieldNames(m *meta.Model) []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.Fields))
	for _, f := range m.Fields {
		out = append(out, f.Name)
	}
	return out
}

func orderFor(reg *meta.Registry, im *meta.Import) *previewOrder {
	m := reg.Model(im.Model)
	o := &previewOrder{fields: fieldNames(m)}
	if im.Lines != nil && m != nil {
		if f := m.Field(im.Lines.Field); f != nil {
			o.lineField, o.lineFields = f.Name, fieldNames(reg.Model(f.Of))
		}
	}
	return o
}

// orderedRecord marshals a map with keys in the given order; keys not in it follow, sorted.
type orderedRecord struct {
	m     map[string]any
	keys  []string
	order *previewOrder
	line  bool
}

func (r orderedRecord) MarshalJSON() ([]byte, error) {
	seen := map[string]bool{}
	keys := make([]string, 0, len(r.m))
	for _, k := range r.keys {
		if _, ok := r.m[k]; ok && !seen[k] {
			keys, seen[k] = append(keys, k), true
		}
	}
	var rest []string
	for k := range r.m {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	keys = append(keys, rest...)

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		v := r.m[k]
		if !r.line && r.order.lineField != "" && k == r.order.lineField {
			v = r.order.lines(v)
		}
		vb, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (o *previewOrder) lines(v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]any, len(list))
	for i, l := range list {
		if m, ok := l.(map[string]any); ok {
			out[i] = orderedRecord{m: m, keys: o.lineFields, order: o, line: true}
		} else {
			out[i] = l
		}
	}
	return out
}

// MarshalJSON writes the entry with its preview records in field order (when the service set one), or without
// a preview for List.
func (e *Entry) MarshalJSON() ([]byte, error) {
	type plain Entry
	if e.listing {
		return json.Marshal(struct {
			*plain
			Preview *struct{} `json:"preview,omitempty"`
		}{(*plain)(e), nil})
	}
	if e.order == nil {
		return json.Marshal((*plain)(e))
	}
	recs := make([]orderedRecord, len(e.Preview))
	for i, m := range e.Preview {
		recs[i] = orderedRecord{m: m, keys: e.order.fields, order: e.order}
	}
	return json.Marshal(struct {
		*plain
		Preview []orderedRecord `json:"preview"`
	}{(*plain)(e), recs})
}
