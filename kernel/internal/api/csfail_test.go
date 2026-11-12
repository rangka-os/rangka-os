package api

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/changeset"
)

// A 403 change-set error with a detail map carries the detail inside error (contract 0045 §2).
func TestCsFailForbiddenPaths(t *testing.T) {
	w := httptest.NewRecorder()
	csFail(w, &changeset.Error{Status: 403, Code: "ai_forbidden_path", Msg: "tidak bisa", Detail: map[string]any{"paths": []string{"apps/crm/app.rk"}}})
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"error": map[string]any{"code": "ai_forbidden_path", "message": "tidak bisa", "paths": []any{"apps/crm/app.rk"}}}
	if w.Code != 403 || !reflect.DeepEqual(body, want) {
		t.Fatalf("%d %v", w.Code, body)
	}
}
