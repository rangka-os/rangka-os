package changeset

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// ApplyPatch applies an RFC 6902 JSON Patch to plain data (maps, lists, scalars).
func ApplyPatch(doc any, patch []map[string]any) (any, error) {
	doc = deepCopy(doc)
	for i, op := range patch {
		path, _ := op["path"].(string)
		var err error
		switch op["op"] {
		case "add":
			doc, err = setAt(doc, path, deepCopy(op["value"]), "add")
		case "replace":
			if _, gerr := getAt(doc, path); gerr != nil {
				return nil, fmt.Errorf("patch %d: %w", i, gerr)
			}
			doc, err = setAt(doc, path, deepCopy(op["value"]), "replace")
		case "remove":
			doc, _, err = removeAt(doc, path)
		case "move", "copy":
			from, _ := op["from"].(string)
			var v any
			if op["op"] == "move" {
				doc, v, err = removeAt(doc, from)
			} else {
				v, err = getAt(doc, from)
				v = deepCopy(v)
			}
			if err == nil {
				doc, err = setAt(doc, path, v, "add")
			}
		case "test":
			var v any
			v, err = getAt(doc, path)
			if err == nil && !reflect.DeepEqual(normalize(v), normalize(op["value"])) {
				err = fmt.Errorf("test failed at %s", path)
			}
		default:
			err = fmt.Errorf("unknown op %v", op["op"])
		}
		if err != nil {
			return nil, fmt.Errorf("patch %d (%v %s): %w", i, op["op"], path, err)
		}
	}
	return doc, nil
}

func normalize(v any) any {
	b, _ := json.Marshal(v)
	var out any
	json.Unmarshal(b, &out)
	return out
}

func deepCopy(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = deepCopy(e)
		}
		return m
	case []any:
		l := make([]any, len(x))
		for i, e := range x {
			l[i] = deepCopy(e)
		}
		return l
	case float64:
		if x == float64(int64(x)) && x < 1e15 && x > -1e15 {
			return int64(x)
		}
	}
	return v
}

func tokens(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("JSON pointer must start with /")
	}
	parts := strings.Split(path[1:], "/")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
	}
	return parts, nil
}

func getAt(doc any, path string) (any, error) {
	toks, err := tokens(path)
	if err != nil {
		return nil, err
	}
	cur := doc
	for _, t := range toks {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[t]
			if !ok {
				return nil, fmt.Errorf("%s: no key %q", path, t)
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(t)
			if err != nil || i < 0 || i >= len(c) {
				return nil, fmt.Errorf("%s: no index %q", path, t)
			}
			cur = c[i]
		default:
			return nil, fmt.Errorf("%s: cannot descend into a value", path)
		}
	}
	return cur, nil
}

// setAt sets (add/replace) the value at path and returns the new document.
func setAt(doc any, path string, v any, mode string) (any, error) {
	toks, err := tokens(path)
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 {
		return v, nil
	}
	parent, err := getAt(doc, "/"+strings.Join(escape(toks[:len(toks)-1]), "/"))
	if len(toks) == 1 {
		parent, err = doc, nil
	}
	if err != nil {
		return nil, err
	}
	last := toks[len(toks)-1]
	switch p := parent.(type) {
	case map[string]any:
		p[last] = v
		return doc, nil
	case []any:
		var i int
		if last == "-" {
			i = len(p)
		} else if i, err = strconv.Atoi(last); err != nil || i < 0 || i > len(p) {
			return nil, fmt.Errorf("bad index %q", last)
		}
		var nl []any
		if mode == "replace" {
			if i >= len(p) {
				return nil, fmt.Errorf("bad index %q", last)
			}
			p[i] = v
			return doc, nil
		}
		nl = append(append(append([]any{}, p[:i]...), v), p[i:]...)
		return replaceParent(doc, toks[:len(toks)-1], nl)
	}
	return nil, fmt.Errorf("cannot set %s", path)
}

func removeAt(doc any, path string) (any, any, error) {
	toks, err := tokens(path)
	if err != nil || len(toks) == 0 {
		return nil, nil, fmt.Errorf("cannot remove the whole document")
	}
	var parent any = doc
	if len(toks) > 1 {
		parent, err = getAt(doc, "/"+strings.Join(escape(toks[:len(toks)-1]), "/"))
		if err != nil {
			return nil, nil, err
		}
	}
	last := toks[len(toks)-1]
	switch p := parent.(type) {
	case map[string]any:
		v, ok := p[last]
		if !ok {
			return nil, nil, fmt.Errorf("no key %q", last)
		}
		delete(p, last)
		return doc, v, nil
	case []any:
		i, err := strconv.Atoi(last)
		if err != nil || i < 0 || i >= len(p) {
			return nil, nil, fmt.Errorf("bad index %q", last)
		}
		v := p[i]
		nl := append(append([]any{}, p[:i]...), p[i+1:]...)
		d, err := replaceParent(doc, toks[:len(toks)-1], nl)
		return d, v, err
	}
	return nil, nil, fmt.Errorf("cannot remove %s", path)
}

func replaceParent(doc any, toks []string, nl []any) (any, error) {
	if len(toks) == 0 {
		return nl, nil
	}
	return setAt(doc, "/"+strings.Join(escape(toks), "/"), nl, "replace")
}

func escape(toks []string) []string {
	out := make([]string, len(toks))
	for i, t := range toks {
		out[i] = strings.ReplaceAll(strings.ReplaceAll(t, "~", "~0"), "/", "~1")
	}
	return out
}
