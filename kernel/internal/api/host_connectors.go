package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/secrets"
)

// Host connectors (contract 0063 §2): a catalogue connector with provided_by: host gets every auth field from the
// platform that runs the kernel. Whoever runs the kernel names the ones it serves in RANGKA_HOST_CONNECTORS; any
// other host connector is host_unavailable: the catalogue says so and a change set cannot install it.

// HostConnectorsEnv is the variable that names the host connectors a kernel serves, comma separated.
const HostConnectorsEnv = "RANGKA_HOST_CONNECTORS"

// ParseHostConnectors reads RANGKA_HOST_CONNECTORS: connector names separated by commas, spaces ignored. A name
// must look like a connector name (lowercase snake_case); a company connector (x_) is never a host connector.
func ParseHostConnectors(v string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, n := range strings.Split(v, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !connectorName(n) {
			return nil, fmt.Errorf("%s: %q is not a connector name", HostConnectorsEnv, n)
		}
		if meta.IsCompanyConnector(n) {
			return nil, fmt.Errorf("%s: %q is a company connector (x_), which is never provided by the host (contract 0063 §2)", HostConnectorsEnv, n)
		}
		out[n] = true
	}
	return out, nil
}

func connectorName(n string) bool {
	for i, r := range n {
		if !(r >= 'a' && r <= 'z' || r == '_' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return n != ""
}

// HostConnectorList is the sorted list of served host connectors, for logs.
func (s *Server) HostConnectorList() []string {
	out := make([]string, 0, len(s.HostConnectors))
	for n := range s.HostConnectors {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// isHostConnector reports whether the live registry's connector name is provided_by: host.
func (s *Server) isHostConnector(name string) bool {
	return secrets.HostProvided(s.Engine.Reg(), name)
}

// hostUnavailable reports whether name is a host connector this kernel does not serve.
func (s *Server) hostUnavailable(reg *meta.Registry, name string) bool {
	return secrets.HostProvided(reg, name) && !s.HostConnectors[name]
}

// HostUnavailable is the change-set hook (changeset.Service.HostUnavailable): the host connectors installed in after
// and not in before that this kernel does not serve, sorted.
func (s *Server) HostUnavailable(before, after *meta.Registry) []string {
	was := map[string]bool{}
	for _, n := range InstalledConnectors(before) {
		was[n] = true
	}
	var out []string
	for _, n := range InstalledConnectors(after) {
		if !was[n] && s.hostUnavailable(after, n) {
			out = append(out, n)
		}
	}
	return out
}
