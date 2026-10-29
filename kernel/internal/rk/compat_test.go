package rk

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestPythonCompat loads every .rk file in the workspace (and testdata/yaml) with both this loader and
// rangka-os/tools/rkfile.py and compares data, node positions and error codes. Skips without python3+PyYAML.
func TestPythonCompat(t *testing.T) {
	tools, _ := filepath.Abs("../../../tools")
	workspace, _ := filepath.Abs("../../../..")
	if _, err := os.Stat(filepath.Join(tools, "rkfile.py")); err != nil {
		t.Skip("rkfile.py not found")
	}
	var files []string
	for _, root := range []string{filepath.Join(workspace, "rangka-apps"), filepath.Join(workspace, "rangka-os", "templates"),
		filepath.Join(workspace, "rangka-connectors"), "../../testdata"} {
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".rk") {
				files = append(files, p)
			}
			return nil
		})
	}
	if len(files) < 10 {
		t.Fatalf("expected the workspace .rk files, found %d", len(files))
	}
	args := append([]string{"../../scripts/rkdump.py", tools}, files...)
	out, err := exec.Command("python3", args...).Output()
	if err != nil {
		t.Skipf("python3/PyYAML unavailable: %v", err)
	}
	var want map[string]struct {
		Data  any               `json:"data"`
		Where map[string]string `json:"where"`
		Error [][3]any          `json:"error"`
	}
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		w := want[f]
		d, err := Load(f)
		if w.Error != nil {
			if err == nil {
				t.Errorf("%s: python refuses (%v), Go accepts", f, w.Error)
				continue
			}
			e := err.(*Error)
			var got [][3]any
			for _, x := range e.All {
				got = append(got, [3]any{float64(x.Line), float64(x.Col), x.Code})
			}
			if fmt.Sprint(got) != fmt.Sprint(w.Error) {
				t.Errorf("%s: errors\n got  %v\n want %v", f, got, w.Error)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: Go refuses: %v", f, err)
			continue
		}
		gotData := roundTrip(fixFloats(d.Data))
		if !reflect.DeepEqual(gotData, roundTrip(w.Data)) {
			gj, _ := json.Marshal(gotData)
			wj, _ := json.Marshal(w.Data)
			t.Errorf("%s: data differs\n got  %s\n want %s", f, gj, wj)
		}
		gotWhere := map[string]string{}
		if d.Root != nil {
			collectWhere(d.Root, nil, gotWhere)
		}
		for k, v := range w.Where {
			if gotWhere[k] != v {
				t.Errorf("%s: position of %q: got %s want %s", f, k, gotWhere[k], v)
			}
		}
	}
}

func collectWhere(n *Node, path []string, out map[string]string) {
	if n.Kind == ScalarNode && n.Tag == "null" && n.Value == "" && n.Style == 0 {
		// PyYAML places empty values at the next token; positions of empty values are not compared
		return
	}
	out[strings.Join(path, "/")] = fmt.Sprintf("%d:%d", n.Line, n.Col)
	for i, k := range n.Keys {
		collectWhere(n.Vals[i], append(append([]string{}, path...), k.Value), out)
	}
	for i, it := range n.Items {
		collectWhere(it, append(append([]string{}, path...), fmt.Sprint(i)), out)
	}
}

func fixFloats(v any) any {
	switch x := v.(type) {
	case float64:
		if math.IsInf(x, 1) {
			return "inf"
		}
		if math.IsInf(x, -1) {
			return "-inf"
		}
		if math.IsNaN(x) {
			return "nan"
		}
	case map[string]any:
		m := map[string]any{}
		for k, vv := range x {
			m[k] = fixFloats(vv)
		}
		return m
	case []any:
		l := make([]any, len(x))
		for i, vv := range x {
			l[i] = fixFloats(vv)
		}
		return l
	}
	return v
}

func roundTrip(v any) any {
	b, _ := json.Marshal(v)
	var out any
	json.Unmarshal(b, &out)
	return out
}
