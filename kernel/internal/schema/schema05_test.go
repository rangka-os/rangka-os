package schema

import (
	"strings"
	"testing"
)

func TestSchema05Kinds(t *testing.T) {
	s := Embedded()
	for _, k := range []string{"print", "letterhead", "model"} {
		if !s.Has(k) {
			t.Errorf("Has(%q) = false", k)
		}
	}
	if s.Raw("print") == nil || s.Raw("letterhead") == nil {
		t.Error("Raw returns nil for a 0.5 kind")
	}

	ok := map[string]any{"name": "sales_invoice", "model": "sales_invoice", "template": "x.html", "paper": "A4"}
	if errs := s.Validate("print", FromData(ok)); len(errs) != 0 {
		t.Errorf("valid print: %v", errs)
	}

	bad := map[string]any{"name": "sales_invoice", "model": "sales_invoice", "template": "x.html", "paper": "Letter"}
	errs := s.Validate("print", FromData(bad))
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "Letter") {
		t.Errorf("paper Letter: %v", errs)
	}

	noTpl := map[string]any{"name": "sales_invoice", "model": "sales_invoice"}
	errs = s.Validate("print", FromData(noTpl))
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "'template' is a required property") {
		t.Errorf("missing template: %v", errs)
	}

	lh := map[string]any{"bank": []any{map[string]any{"bank": "B"}}}
	errs = s.Validate("letterhead", FromData(lh))
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "'account' is a required property") {
		t.Errorf("bank without account: %v", errs)
	}
}

// The contract's example documents (0008 §1, §2) must validate.
func TestSchema05ContractExamples(t *testing.T) {
	s := Embedded()
	pr := map[string]any{
		"name": "sales_invoice", "model": "sales_invoice",
		"label": map[string]any{"id": "Faktur", "en": "Invoice"},
		"paper": "A4", "template": "sales_invoice.html", "default": true,
		"related": map[string]any{"invoices": map[string]any{
			"model": "sales_invoice", "match": map[string]any{"customer": "name"},
			"filter": "docstatus == 1", "sort": "posting_date", "limit": int64(500),
		}},
	}
	if errs := s.Validate("print", FromData(pr)); len(errs) != 0 {
		t.Errorf("print example: %v", errs)
	}
	lh := map[string]any{
		"legal_name": "PT Contoh Jasa Teknik", "tagline": "Inspeksi dan pengujian", "logo": "brand/logo.png",
		"address": "Jl. Contoh No. 1\nJakarta 10110", "phone": "+62 21 000 0000", "email": "halo@contoh.test",
		"website": "contoh.test", "npwp": "00.000.000.0-000.000",
		"bank":      []any{map[string]any{"bank": "Bank Contoh", "account": "000 000 0000", "holder": "PT Contoh Jasa Teknik", "branch": "Jakarta"}},
		"signatory": map[string]any{"name": "Nama Penandatangan", "title": "Direktur"},
		"footer":    map[string]any{"id": "Terima kasih atas kepercayaan Anda"},
	}
	if errs := s.Validate("letterhead", FromData(lh)); len(errs) != 0 {
		t.Errorf("letterhead example: %v", errs)
	}
	// strictness
	pr["extra"] = true
	if errs := s.Validate("print", FromData(pr)); len(errs) != 1 {
		t.Errorf("additional property: %v", errs)
	}
}

func TestSchema05LoadDir(t *testing.T) {
	s, err := LoadDir("../../../schema/0.4")
	if err != nil {
		t.Skip("schema source dir not found")
	}
	if !s.Has("print") || !s.Has("letterhead") || !s.Has("model") {
		t.Error("LoadDir(0.4) should also load the sibling 0.5")
	}
}
