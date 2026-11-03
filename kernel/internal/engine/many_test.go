package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

func itemCount(t *testing.T, e *Engine, a Actor) int64 {
	t.Helper()
	n, err := store.Count(context.Background(), e.DB, a.Org, "item")
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCreateManyCommits(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	before := itemCount(t, e, a)
	res, err := e.CreateMany(ctx, a, "item", []map[string]any{{"item_name": "Many A"}, {"item_name": "Many B"}}, ManyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].Name == "" || res[1].Name == "" || res[0].Name == res[1].Name || res[0].Err != nil || res[1].Err != nil {
		t.Fatalf("results: %+v", res)
	}
	if got := itemCount(t, e, a); got != before+2 {
		t.Fatalf("count %d, want %d", got, before+2)
	}
	for _, r := range res {
		if _, err := e.Get(ctx, a, "item", r.Name); err != nil {
			t.Fatalf("%s: %v", r.Name, err)
		}
	}
}

func TestCreateManyDryRunLeavesNoTrace(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	before := itemCount(t, e, a)
	var wrote []string
	e.OnWrite = func(org, model string) { wrote = append(wrote, model) }
	res, err := e.CreateMany(ctx, a, "item", []map[string]any{{"item_name": "Dry A"}, {"item_name": "Dry B"}}, ManyOpts{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].Name == "" || res[1].Name == "" || res[0].Err != nil {
		t.Fatalf("results: %+v", res)
	}
	if got := itemCount(t, e, a); got != before {
		t.Fatalf("count %d, want %d", got, before)
	}
	if len(wrote) != 0 {
		t.Fatalf("OnWrite fired on dry run: %v", wrote)
	}
	// the name counter rolled back too: a real create gets the name the dry run showed
	real, err := e.CreateMany(ctx, a, "item", []map[string]any{{"item_name": "Dry A"}}, ManyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if real[0].Name != res[0].Name {
		t.Fatalf("dry run consumed a name: %s vs %s", real[0].Name, res[0].Name)
	}
}

func TestCreateManyValidationRollsBack(t *testing.T) {
	e, a := setup(t)
	before := itemCount(t, e, a)
	res, err := e.CreateMany(context.Background(), a, "item", []map[string]any{{"item_name": "Ok"}, {"uom": "Kg"}, {"item_name": "Ok 2"}}, ManyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var ve *ValidationError
	if len(res) != 3 || res[0].Err != nil || !errors.As(res[1].Err, &ve) || res[2].Err != nil {
		t.Fatalf("results: %+v", res)
	}
	if got := itemCount(t, e, a); got != before {
		t.Fatalf("count %d, want %d", got, before)
	}
}

func TestCreateManyBefore(t *testing.T) {
	e, a := setup(t)
	before := itemCount(t, e, a)
	boom := errors.New("boom")
	_, err := e.CreateMany(context.Background(), a, "item", []map[string]any{{"item_name": "Never"}},
		ManyOpts{Before: func(ctx context.Context, tx pg.Querier) error { return boom }})
	if !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
	if got := itemCount(t, e, a); got != before {
		t.Fatalf("count %d, want %d", got, before)
	}
	called := false
	if _, err := e.CreateMany(context.Background(), a, "item", []map[string]any{{"item_name": "Yes"}},
		ManyOpts{Before: func(ctx context.Context, tx pg.Querier) error { called = true; return nil }}); err != nil || !called {
		t.Fatalf("called=%v err=%v", called, err)
	}
}

func TestFindBy(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	m := e.Reg().Model("item")
	res, err := e.CreateMany(ctx, a, "item", []map[string]any{{"item_name": "Find Me"}, {"item_name": "Dup"}, {"item_name": "Dup"}}, ManyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.FindBy(ctx, e.DB, e.Reg(), a.Org, m, "item_name", "Find Me")
	if err != nil || len(got) != 1 || got[0] != res[0].Name {
		t.Fatalf("got %v err %v", got, err)
	}
	if got, _ = store.FindBy(ctx, e.DB, e.Reg(), a.Org, m, "item_name", "Dup"); len(got) != 2 {
		t.Fatalf("dup: %v", got)
	}
	if got, _ = store.FindBy(ctx, e.DB, e.Reg(), a.Org, m, "name", res[0].Name); len(got) != 1 {
		t.Fatalf("by name: %v", got)
	}
	if got, _ = store.FindBy(ctx, e.DB, e.Reg(), a.Org, m, "item_name", "nope"); len(got) != 0 {
		t.Fatalf("none: %v", got)
	}
	if got, _ = store.FindBy(ctx, e.DB, e.Reg(), "other-org", m, "item_name", "Find Me"); len(got) != 0 {
		t.Fatalf("other org: %v", got)
	}
	if _, err := store.FindBy(ctx, e.DB, e.Reg(), a.Org, m, "no_such; drop", "x"); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func leadInputs() []map[string]any {
	return []map[string]any{
		{"lead_name": "L1", "phone": "+62811000111"},
		{"lead_name": "L2", "phone": "+62811000111"},
		{"lead_name": "L3", "phone": "+62811000333"},
	}
}

func TestCreateManySavepointPerInput(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	count := func() int64 {
		n, err := store.Count(ctx, e.DB, a.Org, "lead")
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	res, err := e.CreateMany(ctx, a, "lead", leadInputs(), ManyOpts{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	var ve *ValidationError
	if res[0].Err != nil || !errors.As(res[1].Err, &ve) || res[2].Err != nil || res[0].Name == "" || res[2].Name == "" {
		t.Fatalf("dry run: %+v", res)
	}
	res, err = e.CreateMany(ctx, a, "lead", leadInputs(), ManyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.As(res[1].Err, &ve) || res[0].Name != "" || res[2].Name != "" {
		t.Fatalf("commit: %+v", res)
	}
	if got := count(); got != before {
		t.Fatalf("count %d, want %d", got, before)
	}
}

func TestCreateManyBeforeSameTransaction(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	before := itemCount(t, e, a)
	_, err := e.CreateMany(ctx, a, "item", []map[string]any{{"item_name": "B1"}}, ManyOpts{DryRun: true,
		Before: func(ctx context.Context, tx pg.Querier) error {
			_, err := tx.Exec(ctx, "insert into r_item (org_id, name, item_name) values ($1, 'ZZ-BEFORE', 'zz')", a.Org)
			return err
		}})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemCount(t, e, a); got != before {
		t.Fatalf("Before row survived dry run: %d vs %d", got, before)
	}
}
