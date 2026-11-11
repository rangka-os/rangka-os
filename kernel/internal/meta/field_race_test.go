package meta

import (
	"sync"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// Field is read by many goroutines at once (flow jobs, requests) on the one registry: it must not write, or the
// runtime aborts with concurrent map writes (audit poll-find-hooks F7, card t_63054d4b). Run with -race.
func TestModelFieldConcurrent(t *testing.T) {
	m := parseModel("app", &rk.Doc{Data: map[string]any{"name": "race_m", "fields": []any{
		map[string]any{"name": "a", "type": "data"}, map[string]any{"name": "b", "type": "int"}}}})
	lit := &Model{Name: "lit", Fields: []*Field{{Name: "x", Type: "data"}}}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if m.Field("a") == nil || m.Field("b") == nil || m.Field("zz") != nil {
				t.Error("parsed model fields")
			}
			if lit.Field("x") == nil || lit.Field("y") != nil {
				t.Error("literal model fields")
			}
		}()
	}
	wg.Wait()
}
