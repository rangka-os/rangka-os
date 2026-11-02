package imports

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

const mokaName = "moka_item_details"

// newService migrates a fresh database for the fnb fixture and seeds the five items of the Moka sample and
// outlet OUT-01.
func newService(t *testing.T) (*Service, engine.Actor) {
	t.Helper()
	url := testdb.New(t)
	ctx := context.Background()
	reg := meta.Load("testdata/apps", "", nil)
	db, err := pg.NewPool(ctx, url, 6)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := store.Migrate(ctx, db, reg, false, "test"); err != nil {
		t.Fatal(err)
	}
	e := engine.New(db, reg)
	e.Now = func() time.Time { return time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC) }
	a := engine.Actor{Org: "tahu-dago", ID: "ceo@saga.co.id", Roles: []string{"owner"}, Human: true}
	items := []map[string]any{
		{"item_name": "Tahu Original 10", "sku": "TH-ORI-10"},
		{"item_name": "Tahu Original 20", "sku": "TH-ORI-20"},
		{"item_name": "Tahu Isi 5", "sku": "TH-ISI-5"},
		{"item_name": "Es Teh Manis", "sku": "MN-ETM-R"},
		{"item_name": "Es Jeruk", "sku": "MN-EJR-R"},
	}
	if res, err := e.CreateMany(ctx, a, "item", items, engine.ManyOpts{}); err != nil || res[0].Err != nil {
		t.Fatalf("items: %v %+v", err, res)
	}
	res, err := e.CreateMany(ctx, a, "outlet", []map[string]any{{"outlet_name": "Outlet Dago"}}, engine.ManyOpts{})
	if err != nil || res[0].Name != "OUT-01" {
		t.Fatalf("outlet: %v %+v", err, res)
	}
	return &Service{DB: db, Engine: e}, a
}

func mokaBytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/moka-item-details.csv")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

const extraRows = "03/11/2026,08:01,Outlet Dago,DGO-0101,Tahu,Tahu Original,Isi 10,,TH-ORI-10,1,25000,0,0,25000,0,0,Dine In,Cash,Kasir 1,Kasir 1\n" +
	"03/11/2026,08:01,Outlet Dago,DGO-0101,Minuman,Es Jeruk,Regular,,MN-EJR-R,1,10000,0,0,10000,0,0,Dine In,Cash,Kasir 1,Kasir 1\n"

func withExtra(t *testing.T) []byte {
	data := mokaBytes(t)
	if !strings.HasSuffix(string(data), "\n") {
		data = append(data, '\n')
	}
	return append(data, extraRows...)
}

func salesCount(t *testing.T, s *Service, a engine.Actor) int64 {
	t.Helper()
	n, err := store.Count(context.Background(), s.DB, a.Org, "outlet_sales")
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func keyCount(t *testing.T, s *Service, a engine.Actor) int64 {
	t.Helper()
	res, err := s.DB.Query(context.Background(), "select count(*) from rk_import_keys where org_id = $1", a.Org)
	if err != nil {
		t.Fatal(err)
	}
	return res.Rows[0][0].(int64)
}

func wantStatus(t *testing.T, err error, status int, code string) {
	t.Helper()
	var se *engine.StatusError
	if !errors.As(err, &se) || se.Status != status || se.Code != code {
		t.Fatalf("err %v, want %d %s", err, status, code)
	}
}

func TestPreviewCommitAndDuplicates(t *testing.T) {
	s, a := newService(t)
	ctx := context.Background()

	p, err := s.Preview(ctx, a, mokaName, "moka.csv", mokaBytes(t), "OUT-01")
	if err != nil {
		t.Fatal(err)
	}
	if p.Rows != 8 || len(p.Errors) != 0 || p.Duplicate || p.Skipped != 0 || p.Status != "previewed" || p.Scope != "OUT-01" || p.Message != "" {
		t.Fatalf("preview: %+v", p)
	}
	if len(p.Preview) != 1 {
		t.Fatalf("preview records: %d", len(p.Preview))
	}
	if lines, _ := p.Preview[0]["lines"].([]any); len(lines) != 8 {
		t.Fatalf("preview lines: %#v", p.Preview[0]["lines"])
	}
	if n := salesCount(t, s, a); n != 0 {
		t.Fatalf("preview wrote %d sales", n)
	}
	// the stored plan keeps money as int64, never float64
	_, plan, err := s.load(ctx, a, mokaName, p.ID, true)
	if err != nil || len(plan) != 1 || len(plan[0].Keys) != 8 || len(plan[0].Rows) != 8 {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	if l := plan[0].Input["lines"].([]any)[2].(map[string]any); l["net"] != int64(43200) || l["gross"] != int64(48000) {
		t.Fatalf("plan line 3: %#v", l)
	}

	c, err := s.Commit(ctx, a, mokaName, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != "committed" || len(c.Records) != 1 || c.CommittedAt == nil {
		t.Fatalf("commit: %+v", c)
	}
	if n := salesCount(t, s, a); n != 1 {
		t.Fatalf("sales %d", n)
	}
	if n := keyCount(t, s, a); n != 8 {
		t.Fatalf("keys %d", n)
	}
	// money survives the stored plan as integers, and the record is a draft
	rec, err := s.Engine.Get(ctx, a, "outlet_sales", c.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	lines := rec["lines"].([]any)
	if len(lines) != 8 || rec["outlet"] != "OUT-01" || rec["date"] != "2026-11-02" {
		t.Fatalf("record: %#v", rec)
	}
	if l := lines[2].(store.Record); fmt.Sprint(l["net"]) != "43200" || fmt.Sprint(l["discount"]) != "4800" {
		t.Fatalf("line 3: %#v", l)
	}

	// the same file again
	d, err := s.Preview(ctx, a, mokaName, "moka.csv", mokaBytes(t), "OUT-01")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Duplicate || d.Message != "File ini sudah diimpor" || d.Skipped != 8 || len(d.Preview) != 0 {
		t.Fatalf("duplicate preview: %+v", d)
	}
	_, err = s.Commit(ctx, a, mokaName, d.ID)
	wantStatus(t, err, 409, "already_imported")
	if n := salesCount(t, s, a); n != 1 {
		t.Fatalf("sales %d after duplicate", n)
	}

	// an overlapping export: only the new day is created
	o, err := s.Preview(ctx, a, mokaName, "moka-2.csv", withExtra(t), "OUT-01")
	if err != nil {
		t.Fatal(err)
	}
	if o.Rows != 10 || o.Skipped != 8 || o.Duplicate || len(o.Errors) != 0 || len(o.Preview) != 1 {
		t.Fatalf("overlap preview: %+v", o)
	}
	oc, err := s.Commit(ctx, a, mokaName, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(oc.Records) != 1 {
		t.Fatalf("overlap records: %v", oc.Records)
	}
	rec, err = s.Engine.Get(ctx, a, "outlet_sales", oc.Records[0])
	if err != nil {
		t.Fatal(err)
	}
	if lines := rec["lines"].([]any); len(lines) != 2 || rec["date"] != "2026-11-03" {
		t.Fatalf("overlap record: %#v", rec)
	}
	if n := salesCount(t, s, a); n != 2 {
		t.Fatalf("sales %d", n)
	}

	// the same entry committed twice
	_, err = s.Commit(ctx, a, mokaName, o.ID)
	wantStatus(t, err, 409, "already_imported")
	if n := salesCount(t, s, a); n != 2 {
		t.Fatalf("sales %d after second commit", n)
	}

	// the same rows uploaded under another outlet are a different scope
	if _, err := s.Engine.CreateMany(ctx, a, "outlet", []map[string]any{{"outlet_name": "Outlet Dua"}}, engine.ManyOpts{}); err != nil {
		t.Fatal(err)
	}
	other, err := s.Preview(ctx, a, mokaName, "moka.csv", mokaBytes(t), "OUT-02")
	if err != nil {
		t.Fatal(err)
	}
	if other.Duplicate || other.Skipped != 0 {
		t.Fatalf("other outlet: %+v", other)
	}

	// Get and List
	g, err := s.Get(ctx, a, mokaName, p.ID)
	if err != nil || g.Status != "committed" || g.Duplicate || len(g.Records) != 1 || g.FileName != "moka.csv" || g.CreatedBy != a.ID {
		t.Fatalf("get: %+v %v", g, err)
	}
	list, err := s.List(ctx, a, mokaName)
	if err != nil || len(list) != 4 || list[0].ID != other.ID {
		t.Fatalf("list: %d %v", len(list), err)
	}
	_, err = s.Get(ctx, engine.Actor{Org: "other-org", ID: "x"}, mokaName, p.ID)
	wantStatus(t, err, 404, "not_found")
}

func TestCommitRefusesStalePreviews(t *testing.T) {
	s, a := newService(t)
	ctx := context.Background()
	// two previews of the same file before either is committed
	a1, err := s.Preview(ctx, a, mokaName, "moka.csv", mokaBytes(t), "OUT-01")
	if err != nil {
		t.Fatal(err)
	}
	a2, err := s.Preview(ctx, a, mokaName, "moka.csv", mokaBytes(t), "OUT-01")
	if err != nil {
		t.Fatal(err)
	}
	// and an overlapping file (other bytes, same keys plus two)
	b, err := s.Preview(ctx, a, mokaName, "moka-2.csv", withExtra(t), "OUT-01")
	if err != nil {
		t.Fatal(err)
	}
	if a2.Duplicate || b.Duplicate || b.Skipped != 0 {
		t.Fatalf("previews: %+v %+v", a2, b)
	}
	if _, err := s.Commit(ctx, a, mokaName, a1.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.Commit(ctx, a, mokaName, a2.ID)
	wantStatus(t, err, 409, "already_imported")
	// b's keys collide in rk_import_keys: nothing of b is written
	_, err = s.Commit(ctx, a, mokaName, b.ID)
	wantStatus(t, err, 409, "already_imported")
	if n := salesCount(t, s, a); n != 1 {
		t.Fatalf("sales %d", n)
	}
	if n := keyCount(t, s, a); n != 8 {
		t.Fatalf("keys %d", n)
	}
	if g, _ := s.Get(ctx, a, mokaName, b.ID); g.Status != "previewed" || len(g.Records) != 0 {
		t.Fatalf("b: %+v", g)
	}
}

func TestConcurrentCommitsOfOverlappingFiles(t *testing.T) {
	s, a := newService(t)
	ctx := context.Background()
	var ids []string
	for _, data := range [][]byte{mokaBytes(t), withExtra(t), mokaBytes(t)} {
		p, err := s.Preview(ctx, a, mokaName, "moka.csv", data, "OUT-01")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	errs := make([]error, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.Commit(ctx, a, mokaName, id)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
			continue
		}
		wantStatus(t, err, 409, "already_imported")
	}
	if ok != 1 {
		t.Fatalf("%d commits succeeded: %v", ok, errs)
	}
	if n := keyCount(t, s, a); n != 8 && n != 10 {
		t.Fatalf("keys %d", n)
	}
	if n := salesCount(t, s, a); n != 1 && n != 2 {
		t.Fatalf("sales %d", n)
	}
}

func TestPreviewRowErrors(t *testing.T) {
	s, a := newService(t)
	ctx := context.Background()
	data := strings.Replace(string(mokaBytes(t)), "TH-ISI-5", "TH-XX", 1)
	p, err := s.Preview(ctx, a, mokaName, "bad.csv", []byte(data), "OUT-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Errors) != 1 || p.Errors[0].Row != 5 || p.Errors[0].Column != "SKU" || !strings.Contains(p.Errors[0].Message, `"TH-XX" tidak ditemukan`) {
		t.Fatalf("errors: %+v", p.Errors)
	}
	_, err = s.Commit(ctx, a, mokaName, p.ID)
	wantStatus(t, err, 422, "import_errors")
	if n := salesCount(t, s, a); n != 0 {
		t.Fatalf("sales %d", n)
	}

	// an engine validation error (date required) comes back from the dry run as a row error
	data = strings.Replace(string(mokaBytes(t)), "02/11/2026", "", -1)
	p, err = s.Preview(ctx, a, mokaName, "nodate.csv", []byte(data), "OUT-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Errors) == 0 || p.Errors[0].Row != 2 || p.Errors[0].Column != "date" || p.Errors[0].Message == "" {
		t.Fatalf("dry run errors: %+v", p.Errors)
	}
	_, err = s.Commit(ctx, a, mokaName, p.ID)
	wantStatus(t, err, 422, "import_errors")
}

func TestPreviewRefusals(t *testing.T) {
	s, a := newService(t)
	ctx := context.Background()
	_, err := s.Preview(ctx, a, mokaName, "moka.csv", mokaBytes(t), "")
	wantStatus(t, err, 400, "outlet_required")
	_, err = s.Preview(ctx, a, mokaName, "moka.csv", mokaBytes(t), "OUT-99")
	wantStatus(t, err, 422, "unknown_outlet")
	if se := err.(*engine.StatusError); se.Msg != `Outlet "OUT-99" tidak ditemukan` {
		t.Fatalf("msg %q", se.Msg)
	}
	_, err = s.Preview(ctx, a, "nope", "moka.csv", mokaBytes(t), "OUT-01")
	wantStatus(t, err, 404, "not_found")
	if se := err.(*engine.StatusError); se.Msg != `Impor "nope" tidak ada` {
		t.Fatalf("msg %q", se.Msg)
	}
	_, err = s.Preview(ctx, a, mokaName, "x.xlsx", []byte("PK\x03\x04garbage"), "OUT-01")
	wantStatus(t, err, 400, "unreadable")
	_, err = s.Preview(ctx, a, mokaName, "big.csv", make([]byte, MaxBytes+1), "OUT-01")
	wantStatus(t, err, 413, "too_large")
	_, err = s.Commit(ctx, a, mokaName, "imp_missing")
	wantStatus(t, err, 404, "not_found")
}
