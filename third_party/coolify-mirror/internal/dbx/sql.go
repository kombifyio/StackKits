package dbx

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
)

// NextIDs allocates ids from the target sequences (outside the import
// transaction; unused ids only leave harmless gaps).
func NextIDs(ctx context.Context, in *coolify.Instance) Allocator {
	return func(table string, n int) ([]int64, error) {
		var rows []struct {
			ID int64 `json:"id"`
		}
		q := fmt.Sprintf("SELECT nextval(pg_get_serial_sequence(%s, 'id')) AS id FROM generate_series(1, %d)",
			coolify.SQLString("public."+table), n)
		if err := in.Query(ctx, q, &rows); err != nil {
			return nil, fmt.Errorf("allocate ids for %s: %w", table, err)
		}
		ids := make([]int64, len(rows))
		for i, r := range rows {
			ids[i] = r.ID
		}
		return ids, nil
	}
}

// SQL renders the import as INSERT statements. Run it in one transaction.
// It may be called once per plan.
func (p *Plan) SQL() (string, error) {
	var b strings.Builder
	b.WriteString("SET client_min_messages = warning;\n")
	for _, pr := range p.rows {
		if p.cols[pr.table] == nil {
			p.Warnings = append(p.Warnings, fmt.Sprintf("table %s does not exist on this Coolify version; %d row(s) skipped", pr.table, 1))
			continue
		}
		row, err := p.finalRow(pr)
		if err != nil {
			return "", fmt.Errorf("%s row %d: %w", pr.table, pr.oldID, err)
		}
		cols := make([]string, 0, len(row))
		for k := range row {
			cols = append(cols, k)
		}
		sort.Strings(cols)
		quoted := make([]string, len(cols))
		for i, c := range cols {
			quoted[i] = coolify.SQLIdent(c)
		}
		js, err := json.Marshal(row)
		if err != nil {
			return "", err
		}
		tag := dollarTag(string(js))
		list := strings.Join(quoted, ", ")
		fmt.Fprintf(&b, "INSERT INTO %s (%s) SELECT %s FROM json_populate_record(NULL::%s, %s%s%s::json)",
			coolify.SQLIdent(pr.table), list, list, coolify.SQLIdent(pr.table), tag, js, tag)
		if pr.table == "taggables" {
			b.WriteString(" ON CONFLICT DO NOTHING")
		}
		b.WriteString(";\n")
	}
	return b.String(), nil
}

func dollarTag(body string) string {
	tag := "$cm$"
	for i := 0; strings.Contains(body, tag); i++ {
		tag = fmt.Sprintf("$cm%d$", i)
	}
	return tag
}

// RowCount is the number of rows the plan inserts, per table.
func (p *Plan) RowCount() map[string]int {
	out := map[string]int{}
	for _, pr := range p.rows {
		out[pr.table]++
	}
	return out
}
