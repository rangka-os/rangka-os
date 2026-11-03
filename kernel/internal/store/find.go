package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// FindBy returns the names of records whose field equals value, at most two (enough to tell none, one
// or ambiguous). "name" matches the record name; x_ fields are read from the x JSONB column. The field
// must exist on the model, and the value is always a parameter.
func FindBy(ctx context.Context, q pg.Querier, reg *meta.Registry, org string, m *meta.Model, field, value string) ([]string, error) {
	var expr string
	switch {
	case field == "name":
		expr = "name"
	case m.Field(field) == nil:
		return nil, fmt.Errorf("model %s has no field %s", m.Name, field)
	case strings.HasPrefix(field, "x_"):
		expr = "x->>$3"
	case IsColumn(m.Field(field)):
		expr = pg.QuoteIdent(field)
	default:
		return nil, fmt.Errorf("field %s of %s cannot be searched", field, m.Name)
	}
	args := []any{org, value}
	if strings.HasPrefix(field, "x_") {
		args = append(args, field)
	}
	sql := fmt.Sprintf("select name from %s where org_id = $1 and %s::text = $2 order by name limit 2", pg.QuoteIdent(TableName(m.Name)), expr)
	res, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(res.Rows))
	for _, row := range res.Rows {
		out = append(out, fmt.Sprint(row[0]))
	}
	return out, nil
}
