package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

func connectorCheckDB(t *testing.T) (context.Context, *pg.Pool) {
	t.Helper()
	ctx := context.Background()
	db, err := pg.NewPool(ctx, testdb.New(t), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, systemSQL); err != nil {
		t.Fatal(err)
	}
	return ctx, db
}

func TestGetConnectorCheckNoRow(t *testing.T) {
	ctx, db := connectorCheckDB(t)
	c, err := GetConnectorCheck(ctx, db, "o1", "x_none")
	if err != nil || c != nil {
		t.Fatalf("got %v, %v; want nil, nil", c, err)
	}
}

func TestSaveConnectorTestOK(t *testing.T) {
	ctx, db := connectorCheckDB(t)
	if err := SaveConnectorTest(ctx, db, "o1", "x_a", "h1", "owner@x.id", true, json.RawMessage(`{"rows":[1]}`)); err != nil {
		t.Fatal(err)
	}
	c, err := GetConnectorCheck(ctx, db, "o1", "x_a")
	if err != nil || c == nil {
		t.Fatalf("get: %v, %v", c, err)
	}
	if c.State != "tested" || c.Hash != "h1" || c.TestedBy != "owner@x.id" || c.TestedAt == nil || c.Org != "o1" || c.Connector != "x_a" {
		t.Errorf("bad check %+v", c)
	}
	if string(c.LastResult) != `{"rows": [1]}` && string(c.LastResult) != `{"rows":[1]}` {
		t.Errorf("last result %s", c.LastResult)
	}
}

func TestSaveConnectorTestFail(t *testing.T) {
	ctx, db := connectorCheckDB(t)
	res := json.RawMessage(`{"error":"connector_http","message":"gagal"}`)
	if err := SaveConnectorTest(ctx, db, "o1", "x_a", "h1", "owner@x.id", false, res); err != nil {
		t.Fatal(err)
	}
	c, _ := GetConnectorCheck(ctx, db, "o1", "x_a")
	if c == nil || c.State != "untested" || c.Hash != "h1" {
		t.Fatalf("bad check %+v", c)
	}
	var got map[string]string
	if err := json.Unmarshal(c.LastResult, &got); err != nil || got["error"] != "connector_http" {
		t.Errorf("result kept? %s", c.LastResult)
	}
}

func TestSaveConnectorTestResetsConfirmation(t *testing.T) {
	ctx, db := connectorCheckDB(t)
	_ = SaveConnectorTest(ctx, db, "o1", "x_a", "h1", "u", true, json.RawMessage(`{}`))
	if err := ConfirmConnectorWrites(ctx, db, "o1", "x_a", "h1", "owner"); err != nil {
		t.Fatal(err)
	}
	c, _ := GetConnectorCheck(ctx, db, "o1", "x_a")
	if c.WritesConfirmedAt == nil || c.WritesConfirmedBy != "owner" {
		t.Fatalf("not confirmed %+v", c)
	}
	// a new test (new content) clears the confirmation
	_ = SaveConnectorTest(ctx, db, "o1", "x_a", "h2", "u", true, json.RawMessage(`{}`))
	c, _ = GetConnectorCheck(ctx, db, "o1", "x_a")
	if c.WritesConfirmedAt != nil || c.WritesConfirmedBy != "" {
		t.Errorf("confirmation survived a new test: %+v", c)
	}
}

func TestConfirmWritesNeedsSameHash(t *testing.T) {
	ctx, db := connectorCheckDB(t)
	_ = SaveConnectorTest(ctx, db, "o1", "x_a", "h1", "u", true, json.RawMessage(`{}`))
	if err := ConfirmConnectorWrites(ctx, db, "o1", "x_a", "other", "owner"); err == nil {
		t.Fatal("confirm with a different hash must fail")
	}
	if err := ConfirmConnectorWrites(ctx, db, "o1", "x_missing", "h1", "owner"); err == nil {
		t.Fatal("confirm without a row must fail")
	}
	c, _ := GetConnectorCheck(ctx, db, "o1", "x_a")
	if c.WritesConfirmedAt != nil {
		t.Errorf("failed confirm left a mark: %+v", c)
	}
}

func TestConfirmWritesNeedsTestedState(t *testing.T) {
	ctx, db := connectorCheckDB(t)
	_ = SaveConnectorTest(ctx, db, "o1", "x_a", "h1", "u", false, json.RawMessage(`{"error":"e","message":"m"}`))
	if err := ConfirmConnectorWrites(ctx, db, "o1", "x_a", "h1", "owner"); err == nil {
		t.Fatal("confirm after a failed test must fail")
	}
}

func TestConfirmWritesWithin24Hours(t *testing.T) {
	ctx, db := connectorCheckDB(t)
	_ = SaveConnectorTest(ctx, db, "o1", "x_a", "h1", "u", true, json.RawMessage(`{}`))
	if _, err := db.Exec(ctx, `update rk_connector_checks set tested_at = now() - interval '25 hours'`); err != nil {
		t.Fatal(err)
	}
	if err := ConfirmConnectorWrites(ctx, db, "o1", "x_a", "h1", "owner"); err != ErrConnectorNotTested {
		t.Fatalf("got %v, want ErrConnectorNotTested", err)
	}
}
