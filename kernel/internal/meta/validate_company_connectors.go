package meta

import (
	"fmt"
	"net"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// HostPrivate reports whether a connector host must never be called: an IP literal, localhost or *.localhost,
// *.local, *.internal, or a name without a dot (contract 0033 §2). The kernel checks the resolved address again
// at send time (connectors/egress.go). One trailing dot is ignored.
func HostPrivate(host string) bool {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	if net.ParseIP(h) != nil {
		return true
	}
	switch {
	case h == "localhost", strings.HasSuffix(h, ".localhost"), strings.HasSuffix(h, ".local"), strings.HasSuffix(h, ".internal"):
		return true
	}
	return !strings.Contains(h, ".")
}

// companyConnector is contract 0033 §2 and contract 0062 §5 (check_company_connector in rangka_validate.py): where
// a connector may live, that a company connector declares public hosts, and where its draft came from.
func (v *validator) companyConnector(cn string, d *rk.Doc, c map[string]any, all map[string]vis) {
	company := IsCompanyConnector(cn)
	if v.reg.connectorSite[cn] && !company {
		v.rep.extra("error", d.Path, "name", "company_connector_name", fmt.Sprintf(`connector "%s" lives in the site, so its name must start with x_ (x_%s), contract 0033 §2`, cn, cn))
	}
	if v.reg.connectorShared[cn] && company && !v.reg.connectorDup[cn] {
		v.rep.extra("error", d.Path, "name", "company_connector_name", fmt.Sprintf(`connector "%s" starts with x_, so it belongs to the company and lives in the site, not in the shared connectors directory (contract 0033 §2)`, cn))
	}
	if v.reg.connectorDup[cn] {
		v.rep.extra("error", d.Path, "name", "duplicate_connector", fmt.Sprintf(`connector "%s" is in the shared connectors directory and in the site: keep one (contract 0033 §2)`, cn))
	}
	if !company {
		return
	}
	hosts, _ := c["hosts"].([]any)
	if len(hosts) == 0 {
		v.rep.extra("error", d.Path, "(root)", "hosts_required", fmt.Sprintf(`company connector "%s" must declare hosts: the domains it may call (contract 0033 §2)`, cn))
	}
	for i, h := range hosts {
		if s, ok := h.(string); ok && HostPrivate(s) {
			v.rep.extra("error", d.Path, fmt.Sprintf("hosts/%d", i), "connector_host_private", fmt.Sprintf(`host "%s" is an IP address, localhost or a private name: a connector may only call public domains (contract 0033 §2)`, s))
		}
	}
	if asMap(asMap(c["experimental"])["source"]) == nil {
		v.rep.extra("warn", d.Path, "(root)", "connector_source_missing", fmt.Sprintf(`company connector "%s" has no experimental.source: say where the draft came from (contract 0062 §5)`, cn))
	}
	// money_unflagged (contract 0033 §4): a flow ties the connector to a model with money or stock fields, but money: true is missing
	if c["money"] != true && MoneyByFlow(cn, v.reg.flowRaws(), func(m string) bool { x, ok := all[m]; return ok && SensitiveModel(x.m.Raw["fields"]) }) {
		v.rep.extra("warn", d.Path, "(root)", "money_unflagged", fmt.Sprintf(`company connector "%s" is called or triggered by a flow that creates or updates a model with money or stock fields: add money: true so writes need the owner's confirmation (contract 0033 §4)`, cn))
	}
}
