package dbx

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
)

// Coolify changes its database between releases. The export copies every
// column of the tables it knows, so new plain columns travel automatically.
// Two kinds of change would be lost silently instead, and are reported:
//
//   - a table this version does not know that holds rows of the exported
//     resources (a new feature's settings, say),
//   - an id column of a known table that this version does not remap (its
//     value would point at the wrong row on the target).

// idColumnsNotReferences are *_id columns that hold outside ids (GitHub,
// processes, pull requests), not rows of this database.
var idColumnsNotReferences = map[string]bool{
	"repository_project_id": true, "current_process_id": true, "horizon_job_id": true,
	"pull_request_id": true, "app_id": true, "client_id": true, "installation_id": true,
	"deploy_key_id": true, "oauth_id": true, "container_id": true, "runner_group_id": true,
}

// Tables that only log or track activity: they are not part of a resource.
func driftIgnored(table string) bool {
	switch table {
	case "activity_log", "sessions", "personal_access_tokens", "jobs", "failed_jobs",
		"notifications", "scheduled_job_deliveries", "migrations", "cache", "cache_locks",
		"audit_events", "notification_throttles", "oauth_identities":
		return true
	}
	return strings.HasSuffix(table, "_executions") || strings.HasPrefix(table, "telescope_")
}

var driftReasons = map[string]string{
	"application_previews":    "preview deployments (pull requests) are not carried",
	"additional_destinations": "additional servers of an application are not carried",
	"managed_dns_record_references": "Cloudflare DNS records that Coolify manages are not carried (they point at this server) - " +
		"after the move, point the domains at the new server (Coolify can create the records again on the Domains page)",
}

// SchemaNeeds lists the tables and the reference columns the importer remaps
// (without the ones only newer Coolify versions have).
func SchemaNeeds() map[string][]string {
	out := map[string][]string{}
	for t, fks := range foreignKeys {
		if coolify.OptionalTables[t] {
			continue
		}
		cols := map[string]bool{}
		for _, f := range fks {
			if f.Optional {
				continue
			}
			cols[f.Col] = true
			if f.MorphCol != "" {
				cols[f.MorphCol] = true
			}
		}
		for c := range cols {
			out[t] = append(out[t], c)
		}
		sort.Strings(out[t])
	}
	return out
}

// SchemaColumns reads table -> columns of Coolify's database.
func SchemaColumns(ctx context.Context, in *coolify.Instance) (map[string]map[string]bool, error) {
	var rows []struct {
		T string `json:"table_name"`
		C string `json:"column_name"`
	}
	if err := in.Query(ctx, "SELECT table_name, column_name FROM information_schema.columns WHERE table_schema = 'public'", &rows); err != nil {
		return nil, err
	}
	out := map[string]map[string]bool{}
	for _, r := range rows {
		if out[r.T] == nil {
			out[r.T] = map[string]bool{}
		}
		out[r.T][r.C] = true
	}
	return out, nil
}

func plural(s string) string {
	if strings.HasSuffix(s, "y") {
		return strings.TrimSuffix(s, "y") + "ies"
	}
	return s + "s"
}

// FindDrift reports what of the exported resources this version would not carry
// correctly on this Coolify (see above).
func (ex *Export) FindDrift(ctx context.Context, in *coolify.Instance) ([]string, error) {
	schema, err := SchemaColumns(ctx, in)
	if err != nil {
		return nil, err
	}
	ids := map[string][]string{}
	for t, rows := range ex.Tables {
		for _, r := range rows {
			if id, ok := Int64(r["id"]); ok {
				ids[t] = append(ids[t], fmt.Sprint(id))
			}
		}
	}
	known := func(t, col string) bool {
		for _, f := range foreignKeys[t] {
			if f.Col == col {
				return true
			}
		}
		return false
	}
	var out []string

	// Unknown id columns of exported tables, with values.
	for _, t := range sortedKeys(ex.Tables) {
		for col := range schema[t] {
			if !strings.HasSuffix(col, "_id") || known(t, col) || idColumnsNotReferences[col] {
				continue
			}
			for _, r := range ex.Tables[t] {
				if v, ok := Int64(r[col]); ok && v != 0 {
					out = append(out, fmt.Sprintf("column %s.%s is new to this version of coolify-mirror: its value was copied unchanged and may point at the wrong row", t, col))
					break
				}
			}
		}
	}

	// Unknown tables holding rows of the exported resources.
	morphIDs := map[string][]string{}
	for morph, t := range coolify.MorphToTable {
		if len(ids[t]) > 0 {
			morphIDs[morph] = ids[t]
		}
	}
	tables := make([]string, 0, len(schema))
	for t := range schema {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for _, t := range tables {
		if _, ok := foreignKeys[t]; ok || driftIgnored(t) {
			continue
		}
		var conds []string
		for col := range schema[t] {
			switch {
			case strings.HasSuffix(col, "_type") && schema[t][strings.TrimSuffix(col, "_type")+"_id"]:
				idCol := strings.TrimSuffix(col, "_type") + "_id"
				for _, morph := range sortedKeys(morphIDs) {
					conds = append(conds, fmt.Sprintf("(%s = %s AND %s::text IN (%s))", coolify.SQLIdent(col), coolify.SQLString(morph),
						coolify.SQLIdent(idCol), quoteAll(morphIDs[morph])))
				}
			case strings.HasSuffix(col, "_id") && !schema[t][strings.TrimSuffix(col, "_id")+"_type"]:
				target := plural(strings.TrimSuffix(col, "_id"))
				if _, exported := foreignKeys[target]; exported && len(ids[target]) > 0 {
					conds = append(conds, fmt.Sprintf("%s::text IN (%s)", coolify.SQLIdent(col), quoteAll(ids[target])))
				}
			}
		}
		if len(conds) == 0 {
			continue
		}
		sort.Strings(conds)
		n, err := in.Scalar(ctx, fmt.Sprintf("SELECT count(*) FROM %s WHERE %s", coolify.SQLIdent(t), strings.Join(conds, " OR ")))
		if err != nil || n == "0" || n == "" {
			continue
		}
		why := driftReasons[t]
		if why == "" {
			why = "this version of coolify-mirror does not know this table (newer Coolify?) - update coolify-mirror, or set it up again in Coolify after the restore"
		}
		out = append(out, fmt.Sprintf("%s row(s) of table %s belong to these resources and are not in the backup: %s", n, t, why))
	}
	return out, nil
}

func quoteAll(vs []string) string {
	q := make([]string, len(vs))
	for i, v := range vs {
		q[i] = coolify.SQLString(v)
	}
	return strings.Join(q, ",")
}
