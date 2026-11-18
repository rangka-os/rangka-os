package meta

import (
	"fmt"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// ProvidedBy is who provides a connector's auth fields (contract 0063 §2): "host" when the platform that runs the
// kernel serves it, else "owner" (the default). It reads experimental.provided_by until the key is released
// (contract 0000 §4).
func ProvidedBy(c map[string]any) string {
	if v, _ := asMap(c["experimental"])["provided_by"].(string); v == "host" {
		return "host"
	}
	return "owner"
}

// hostConnector is contract 0063 §2 (check_host_connector in rangka_validate.py): a host connector is a catalogue
// connector, and every one of its auth fields comes from the host, so none has a default or is generated.
func (v *validator) hostConnector(cn string, d *rk.Doc, c map[string]any) {
	if ProvidedBy(c) != "host" {
		return
	}
	if IsCompanyConnector(cn) {
		v.rep.extra("error", d.Path, "experimental/provided_by", "host_connector_company", fmt.Sprintf(`company connector "%s" cannot be provided_by: host: only a catalogue connector is served by the host (contract 0063 §2)`, cn))
	}
	for i, x := range asList(asMap(c["auth"])["fields"]) {
		f := asMap(x)
		if _, ok := f["default"]; ok {
			v.rep.extra("error", d.Path, fmt.Sprintf("auth/fields/%d/default", i), "host_connector_fields", fmt.Sprintf(`auth field "%s" of host connector "%s" has a default: its value comes from the host only (contract 0063 §2)`, pyStr(f["name"]), cn))
		}
		if f["generate"] == true {
			v.rep.extra("error", d.Path, fmt.Sprintf("auth/fields/%d/generate", i), "host_connector_fields", fmt.Sprintf(`auth field "%s" of host connector "%s" is generated: its value comes from the host only (contract 0063 §2)`, pyStr(f["name"]), cn))
		}
	}
}
