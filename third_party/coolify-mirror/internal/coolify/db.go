package coolify

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mtalavi/coolify-mirror/internal/run"
)

// Row is one database row as decoded from row_to_json (numbers stay json.Number).
type Row = map[string]any

func (in *Instance) psql(extra ...string) []string {
	args := []string{"exec", "-i", DBContainer, "psql", "-X", "-q", "-v", "ON_ERROR_STOP=1",
		"-U", in.DBUser, "-d", in.DBName}
	return append(args, extra...)
}

// Query runs a SELECT and decodes the rows (as a JSON array) into dst.
func (in *Instance) Query(ctx context.Context, sql string, dst any) error {
	wrapped := "SELECT coalesce(json_agg(q), '[]'::json) FROM (" + sql + ") q;\n"
	out, err := run.Do(ctx, run.Spec{Name: "docker", Args: in.psql("-At", "-f", "-"), Stdin: strings.NewReader(wrapped)})
	if err != nil {
		return fmt.Errorf("database query failed: %w", err)
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		out = []byte("[]")
	}
	if err := jsonUnmarshalNumber(out, dst); err != nil {
		return fmt.Errorf("decode query result: %w", err)
	}
	return nil
}

// HasTable reports whether Coolify's database has this table (newer Coolify
// versions add tables). The list is read once; a read error answers false.
func (in *Instance) HasTable(ctx context.Context, table string) bool {
	in.tablesMu.Lock()
	defer in.tablesMu.Unlock()
	if in.tables == nil {
		var rows []struct {
			T string `json:"table_name"`
		}
		if err := in.Query(ctx, "SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'", &rows); err != nil {
			return false
		}
		in.tables = map[string]bool{}
		for _, r := range rows {
			in.tables[r.T] = true
		}
	}
	return in.tables[table]
}

// Kinds returns the standalone database types this Coolify has.
func (in *Instance) Kinds(ctx context.Context) []DatabaseKind {
	var out []DatabaseKind
	for _, d := range DatabaseKinds {
		if d.Since == "" || in.HasTable(ctx, d.Table) {
			out = append(out, d)
		}
	}
	return out
}

// Rows is Query into []Row.
func (in *Instance) Rows(ctx context.Context, sql string) ([]Row, error) {
	var rows []Row
	err := in.Query(ctx, sql, &rows)
	return rows, err
}

// Scalar returns the first column of the first row as text ("" when no rows).
func (in *Instance) Scalar(ctx context.Context, sql string) (string, error) {
	out, err := run.Do(ctx, run.Spec{Name: "docker", Args: in.psql("-At", "-f", "-"), Stdin: strings.NewReader(sql + ";\n")})
	if err != nil {
		return "", fmt.Errorf("database query failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ExecSQL runs a script. With tx=true the whole script is one transaction.
// The transaction is written into the script (not psql --single-transaction):
// if the input is cut off, psql never sees COMMIT and the server rolls back.
func (in *Instance) ExecSQL(ctx context.Context, script string, tx bool) error {
	if tx {
		script = "BEGIN;\n" + script + "\nCOMMIT;\n"
	}
	_, err := run.Do(ctx, run.Spec{Name: "docker", Args: in.psql("-f", "-"), Stdin: strings.NewReader(script)})
	return err
}

// Dump streams a custom-format pg_dump of the Coolify database to w.
func (in *Instance) Dump(ctx context.Context, w io.Writer) error {
	_, err := run.Do(ctx, run.Spec{
		Name:   "docker",
		Args:   []string{"exec", "-i", DBContainer, "pg_dump", "-U", in.DBUser, "-d", in.DBName, "-Fc", "--no-owner", "--no-acl"},
		Stdout: w,
	})
	return err
}

// RestoreDump replaces the whole Coolify database with a custom-format dump read
// from r. Dropping the old schema and loading the dump happen in ONE transaction,
// so a failure leaves the current database untouched.
func (in *Instance) RestoreDump(ctx context.Context, r io.Reader) error {
	script := `set -e
{
  echo 'BEGIN;'
  echo 'DROP SCHEMA IF EXISTS public CASCADE;'
  echo 'CREATE SCHEMA public;'
  pg_restore --no-owner --no-acl -f - || echo 'SELECT coolify_mirror_pg_restore_failed();'
  echo 'COMMIT;'
} | psql -X -q -v ON_ERROR_STOP=1 -U "$1" -d "$2" >/dev/null`
	_, err := run.Do(ctx, run.Spec{
		Name:  "docker",
		Args:  []string{"exec", "-i", DBContainer, "sh", "-c", script, "sh", in.DBUser, in.DBName},
		Stdin: r,
	})
	return err
}

// Columns returns column name -> data type for every table in the public schema.
func (in *Instance) Columns(ctx context.Context) (map[string]map[string]string, error) {
	var rows []struct {
		Table  string `json:"table_name"`
		Column string `json:"column_name"`
		Type   string `json:"data_type"`
	}
	err := in.Query(ctx, `SELECT table_name, column_name, data_type FROM information_schema.columns WHERE table_schema = 'public'`, &rows)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]string{}
	for _, r := range rows {
		if out[r.Table] == nil {
			out[r.Table] = map[string]string{}
		}
		out[r.Table][r.Column] = r.Type
	}
	return out, nil
}

// SQLString quotes a string literal for PostgreSQL (standard_conforming_strings on).
func SQLString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// SQLIdent quotes an identifier.
func SQLIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// SQLList renders a list of string literals for IN (...).
func SQLList(vals []string) string {
	if len(vals) == 0 {
		return "(NULL)"
	}
	q := make([]string, len(vals))
	for i, v := range vals {
		q[i] = SQLString(v)
	}
	return "(" + strings.Join(q, ",") + ")"
}

// SQLIntList renders a list of integers for IN (...).
func SQLIntList(vals []int64) string {
	if len(vals) == 0 {
		return "(NULL)"
	}
	q := make([]string, len(vals))
	for i, v := range vals {
		q[i] = fmt.Sprint(v)
	}
	return "(" + strings.Join(q, ",") + ")"
}
