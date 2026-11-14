package sandbox

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// GeneratedRows is how many demo rows each company model gets.
const GeneratedRows = 8

// Generate makes demo rows for the site's company models (x_*, contract 0016), which no demo pack can know
// (contract 0052 §4). It uses no LLM and no clock beyond now's date: the same org and fixtures give the same
// rows. Links point at records of fx or at rows generated here. A model with a required field it cannot fill is
// left out and named in skipped with the reason.
func Generate(reg *meta.Registry, org string, fx Fixtures, now time.Time) (Fixtures, map[string]string) {
	out := Fixtures{}
	skipped := map[string]string{}
	var models []*meta.Model
	for _, m := range reg.InstalledModels() {
		if strings.HasPrefix(m.Name, "x_") && !m.IsChild && len(fx[m.Name]) == 0 {
			models = append(models, m)
		}
	}
	// models linking to other company models go after them, so their links can be filled
	models = linkOrder(models)
	h := fnv.New64a()
	h.Write([]byte(org))
	rng := rand.New(rand.NewSource(int64(h.Sum64())))
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	for _, m := range models {
		rows := make([]map[string]any, 0, GeneratedRows)
		reason := ""
		for i := 1; i <= GeneratedRows && reason == ""; i++ {
			row := map[string]any{"name": fmt.Sprintf("%s-%03d", strings.ToUpper(strings.TrimPrefix(m.Name, "x_")), i)}
			for _, f := range m.Fields {
				if meta.SystemFields[f.Name] || f.Compute != "" || f.Name == "name" {
					continue
				}
				v, ok := value(f, i, rng, day, fx, out)
				if !ok {
					if f.Required && f.Default == "" {
						reason = fmt.Sprintf("field %s (%s) cannot be generated", f.Name, f.Type)
						break
					}
					continue
				}
				row[f.Name] = v
			}
			rows = append(rows, row)
		}
		if reason != "" {
			skipped[m.Name] = reason
			continue
		}
		out[m.Name] = rows
	}
	return out, skipped
}

// value is one field's demo value for row i; ok=false when the generator has nothing for the field.
func value(f *meta.Field, i int, rng *rand.Rand, day time.Time, fx, gen Fixtures) (any, bool) {
	label := f.Label.Text()
	if label == "" {
		label = strings.ReplaceAll(f.Name, "_", " ")
	}
	switch f.Type {
	case "data", "text":
		return fmt.Sprintf("Contoh %s %d", label, i), true
	case "phone":
		return fmt.Sprintf("0812000000%02d", i), true
	case "int":
		return bounded(f, int64(1+rng.Intn(20))), true
	case "float":
		return float64(bounded(f, int64(1+rng.Intn(100)))) / 2, true
	case "currency":
		return bounded(f, int64(1+rng.Intn(20))*50000), true // small round amounts
	case "percent":
		return bounded(f, int64(5*(1+rng.Intn(10)))), true
	case "rating":
		return int64(1 + rng.Intn(5)), true
	case "check":
		return i%2 == 0, true
	case "select":
		if len(f.Options) == 0 {
			return nil, false
		}
		return f.Options[(i-1)%len(f.Options)].Value, true
	case "multiselect":
		if len(f.Options) == 0 {
			return nil, false
		}
		return []any{f.Options[(i-1)%len(f.Options)].Value}, true
	case "date":
		return day.AddDate(0, 0, -rng.Intn(30)).Format("2006-01-02"), true
	case "datetime":
		return day.AddDate(0, 0, -rng.Intn(30)).Add(time.Duration(8+rng.Intn(10)) * time.Hour).Format(time.RFC3339), true
	case "link":
		names := recordNames(fx[f.To])
		if len(names) == 0 {
			names = recordNames(gen[f.To])
		}
		if len(names) == 0 {
			return nil, false
		}
		return names[(i-1)%len(names)], true
	}
	return nil, false
}

// bounded keeps v within the field's min and max when the model sets them.
func bounded(f *meta.Field, v int64) int64 {
	if lo, ok := num(f.Raw["min"]); ok && v < lo {
		v = lo
	}
	if hi, ok := num(f.Raw["max"]); ok && v > hi {
		v = hi
	}
	return v
}

func num(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	}
	return 0, false
}

// recordNames are the names of fixture rows, in order.
func recordNames(rows []map[string]any) []string {
	var out []string
	for _, r := range rows {
		if n, ok := r["name"].(string); ok && n != "" {
			out = append(out, n)
		}
	}
	return out
}

// linkOrder sorts company models so a model comes after the company models it links to (cycles keep name order).
func linkOrder(models []*meta.Model) []*meta.Model {
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	byName := map[string]*meta.Model{}
	for _, m := range models {
		byName[m.Name] = m
	}
	var out []*meta.Model
	state := map[string]int{} // 1 visiting, 2 done
	var visit func(m *meta.Model)
	visit = func(m *meta.Model) {
		if state[m.Name] != 0 {
			return
		}
		state[m.Name] = 1
		for _, f := range m.Fields {
			if f.Type == "link" && byName[f.To] != nil {
				visit(byName[f.To])
			}
		}
		state[m.Name] = 2
		out = append(out, m)
	}
	for _, m := range models {
		visit(m)
	}
	return out
}
