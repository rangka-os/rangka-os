package connectors

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// State is the effective state of a connector for one org (contract 0033 §3): needs_auth, untested or tested.
// hash is the sha256 of the connector file as it is on disk now; writesOK says an owner confirmed the writes of that
// exact hash. A catalogue connector is always tested with writes allowed; it has no state row.
func (rt *Runtime) State(ctx context.Context, org, connector string) (state, hash string, writesOK bool, err error) {
	if !meta.IsCompanyConnector(connector) {
		return "tested", "", true, nil
	}
	c, ok := rt.connector(connector)
	if !ok {
		return "", "", false, fmt.Errorf("connector %s is not loaded", connector)
	}
	hash, tested, writesOK, err := rt.checked(ctx, org, connector)
	if err != nil {
		return "", hash, false, err
	}
	if missing, err := rt.missingAuth(ctx, org, connector, c); err != nil {
		return "", hash, false, err
	} else if len(missing) > 0 {
		return "needs_auth", hash, false, nil
	}
	if !tested {
		return "untested", hash, false, nil
	}
	return "tested", hash, writesOK, nil
}

// checked says whether a company connector's file as it is on disk now (hash) passed a test in org, and whether the
// owner confirmed the writes of that content, whatever its keys.
func (rt *Runtime) checked(ctx context.Context, org, connector string) (hash string, tested, writesOK bool, err error) {
	path := rt.Reg().ConnectorPath(connector)
	if path == "" {
		return "", false, false, fmt.Errorf("connector %s has no file", connector)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false, false, err
	}
	sum := sha256.Sum256(raw)
	hash = hex.EncodeToString(sum[:])
	row, err := store.GetConnectorCheck(ctx, rt.DB, org, connector)
	if err != nil {
		return hash, false, false, err
	}
	if row == nil || row.Hash != hash || row.State != "tested" {
		return hash, false, false, nil
	}
	return hash, true, row.WritesConfirmedAt != nil, nil
}
