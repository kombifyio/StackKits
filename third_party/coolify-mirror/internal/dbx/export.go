package dbx

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
	"github.com/mtalavi/coolify-mirror/internal/lcrypt"
)

// FormatVersion of the export document.
const FormatVersion = 1

// encKey marks a value that was encrypted with the source APP_KEY. The map
// {"__cm_enc__": base64(plaintext)} replaces the ciphertext inside exports.
const encKey = "__cm_enc__"

// Export is the database part of a selective backup.
type Export struct {
	Version        int                      `json:"version"`
	CoolifyVersion string                   `json:"coolify_version"`
	Roots          []coolify.Resource       `json:"roots"`
	Tables         map[string][]coolify.Row `json:"tables"`
	Warnings       []string                 `json:"warnings,omitempty"`
	// Drift: what this version could not carry from this Coolify (see drift.go).
	Drift []string `json:"drift,omitempty"`
	// InternalHosts: the private addresses this Coolify allows for outgoing
	// connections (see internalhosts.go).
	InternalHosts []string `json:"internal_hosts,omitempty"`
}

type collector struct {
	ctx  context.Context
	in   *coolify.Instance
	ex   *Export
	seen map[string]map[string]bool
}

// Collect reads every row belonging to the given resources (and the rows they
// reference: project, environment, git source, keys, tags, S3, shared vars).
// Encrypted values are replaced by their plaintext marker.
func Collect(ctx context.Context, in *coolify.Instance, roots []coolify.Resource) (*Export, error) {
	c := &collector{ctx: ctx, in: in, seen: map[string]map[string]bool{},
		ex: &Export{Version: FormatVersion, CoolifyVersion: in.Version, Roots: roots, Tables: map[string][]coolify.Row{}}}
	if err := c.collect(roots); err != nil {
		return nil, err
	}
	for _, t := range sortedKeys(c.ex.Tables) {
		for _, row := range c.ex.Tables[t] {
			for k, v := range row {
				row[k] = c.norm(t+"."+k, v)
			}
		}
	}
	if err := c.sharedVariableRefs(); err != nil {
		return nil, err
	}
	if d, err := c.ex.FindDrift(ctx, in); err == nil {
		c.ex.Drift = d
	}
	c.ex.InternalHosts = readInternalHosts(ctx, in)
	return c.ex, nil
}

func (c *collector) fetch(table, where string) ([]coolify.Row, error) {
	rows, err := c.in.Rows(c.ctx, fmt.Sprintf("SELECT * FROM %s t WHERE %s", coolify.SQLIdent(table), where))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", table, err)
	}
	for _, r := range rows {
		key := rowKey(table, r)
		if c.seen[table] == nil {
			c.seen[table] = map[string]bool{}
		}
		if c.seen[table][key] {
			continue
		}
		c.seen[table][key] = true
		c.ex.Tables[table] = append(c.ex.Tables[table], r)
	}
	return rows, nil
}

func rowKey(table string, r coolify.Row) string {
	if table == "taggables" {
		return fmt.Sprint(r["tag_id"], "/", r["taggable_type"], "/", r["taggable_id"])
	}
	return fmt.Sprint(r["id"])
}

func morphWhere(typeCol, idCol, morph string, ids []int64) string {
	return fmt.Sprintf("(t.%s = %s AND t.%s IN %s)", typeCol, coolify.SQLString(morph), idCol, coolify.SQLIntList(ids))
}

func (c *collector) collect(roots []coolify.Resource) error {
	byTable := map[string][]int64{}
	for _, r := range roots {
		byTable[r.Table] = append(byTable[r.Table], r.ID)
	}
	var resourceRows []coolify.Row
	var pvWhere, fvWhere, envWhere, tagWhere []string

	if ids := byTable["applications"]; len(ids) > 0 {
		rows, err := c.fetch("applications", "t.id IN "+coolify.SQLIntList(ids))
		if err != nil {
			return err
		}
		resourceRows = append(resourceRows, rows...)
		if _, err := c.fetch("application_settings", "t.application_id IN "+coolify.SQLIntList(ids)); err != nil {
			return err
		}
		if _, err := c.fetch("scheduled_tasks", "t.application_id IN "+coolify.SQLIntList(ids)); err != nil {
			return err
		}
		texts := make([]string, len(ids))
		for i, id := range ids {
			texts[i] = strconv.FormatInt(id, 10)
		}
		// Only the last successful production deployment: it carries the commit whose
		// image we ship, so the target can start without rebuilding.
		if _, err := c.fetch("application_deployment_queues", "t.id IN (SELECT DISTINCT ON (application_id) id FROM application_deployment_queues WHERE application_id IN "+
			coolify.SQLList(texts)+" AND status = 'finished' AND pull_request_id = 0 ORDER BY application_id, created_at DESC, id DESC)"); err != nil {
			return err
		}
		m := coolify.MorphApplication
		envWhere = append(envWhere, morphWhere("resourceable_type", "resourceable_id", m, ids))
		pvWhere = append(pvWhere, morphWhere("resource_type", "resource_id", m, ids))
		fvWhere = append(fvWhere, morphWhere("resource_type", "resource_id", m, ids))
		tagWhere = append(tagWhere, morphWhere("taggable_type", "taggable_id", m, ids))
	}

	if ids := byTable["services"]; len(ids) > 0 {
		rows, err := c.fetch("services", "t.id IN "+coolify.SQLIntList(ids))
		if err != nil {
			return err
		}
		resourceRows = append(resourceRows, rows...)
		sa, err := c.fetch("service_applications", "t.deleted_at IS NULL AND t.service_id IN "+coolify.SQLIntList(ids))
		if err != nil {
			return err
		}
		sd, err := c.fetch("service_databases", "t.deleted_at IS NULL AND t.service_id IN "+coolify.SQLIntList(ids))
		if err != nil {
			return err
		}
		if _, err := c.fetch("scheduled_tasks", "t.service_id IN "+coolify.SQLIntList(ids)); err != nil {
			return err
		}
		saIDs, sdIDs := idsOf(sa, "id"), idsOf(sd, "id")
		envWhere = append(envWhere, morphWhere("resourceable_type", "resourceable_id", coolify.MorphService, ids))
		tagWhere = append(tagWhere, morphWhere("taggable_type", "taggable_id", coolify.MorphService, ids))
		if len(saIDs) > 0 {
			envWhere = append(envWhere, morphWhere("resourceable_type", "resourceable_id", coolify.MorphServiceApplication, saIDs))
			pvWhere = append(pvWhere, morphWhere("resource_type", "resource_id", coolify.MorphServiceApplication, saIDs))
			fvWhere = append(fvWhere, morphWhere("resource_type", "resource_id", coolify.MorphServiceApplication, saIDs))
		}
		if len(sdIDs) > 0 {
			envWhere = append(envWhere, morphWhere("resourceable_type", "resourceable_id", coolify.MorphServiceDatabase, sdIDs))
			pvWhere = append(pvWhere, morphWhere("resource_type", "resource_id", coolify.MorphServiceDatabase, sdIDs))
			fvWhere = append(fvWhere, morphWhere("resource_type", "resource_id", coolify.MorphServiceDatabase, sdIDs))
			if _, err := c.fetch("scheduled_database_backups", morphWhere("database_type", "database_id", coolify.MorphServiceDatabase, sdIDs)); err != nil {
				return err
			}
		}
	}

	for _, d := range coolify.DatabaseKinds {
		ids := byTable[d.Table]
		if len(ids) == 0 {
			continue
		}
		rows, err := c.fetch(d.Table, "t.id IN "+coolify.SQLIntList(ids))
		if err != nil {
			return err
		}
		resourceRows = append(resourceRows, rows...)
		envWhere = append(envWhere, morphWhere("resourceable_type", "resourceable_id", d.Morph, ids))
		pvWhere = append(pvWhere, morphWhere("resource_type", "resource_id", d.Morph, ids))
		fvWhere = append(fvWhere, morphWhere("resource_type", "resource_id", d.Morph, ids))
		tagWhere = append(tagWhere, morphWhere("taggable_type", "taggable_id", d.Morph, ids))
		if _, err := c.fetch("scheduled_database_backups", morphWhere("database_type", "database_id", d.Morph, ids)); err != nil {
			return err
		}
	}

	if len(envWhere) > 0 {
		if _, err := c.fetch("environment_variables", strings.Join(envWhere, " OR ")); err != nil {
			return err
		}
		// A resource that reads {{vault.KEY}} values needs its secret manager link
		// and the team's token for that secret manager (Coolify 4.4+).
		if c.in.HasTable(c.ctx, "secret_manager_links") {
			links, err := c.fetch("secret_manager_links", strings.Join(envWhere, " OR "))
			if err != nil {
				return err
			}
			if _, err := c.fetch("integration_tokens", "t.id IN "+coolify.SQLIntList(idsOf(links, "integration_token_id"))); err != nil {
				return err
			}
		}
	}
	var pv, fv []coolify.Row
	var err error
	if len(pvWhere) > 0 {
		if pv, err = c.fetch("local_persistent_volumes", strings.Join(pvWhere, " OR ")); err != nil {
			return err
		}
	}
	if len(fvWhere) > 0 {
		if fv, err = c.fetch("local_file_volumes", strings.Join(fvWhere, " OR ")); err != nil {
			return err
		}
	}
	var volWhere []string
	if ids := idsOf(pv, "id"); len(ids) > 0 {
		volWhere = append(volWhere, morphWhere("backupable_type", "backupable_id", coolify.MorphPersistentVolume, ids))
	}
	if ids := idsOf(fv, "id"); len(ids) > 0 {
		volWhere = append(volWhere, morphWhere("backupable_type", "backupable_id", coolify.MorphFileVolume, ids))
	}
	if len(volWhere) > 0 {
		if _, err := c.fetch("scheduled_volume_backups", strings.Join(volWhere, " OR ")); err != nil {
			return err
		}
	}
	var taggables []coolify.Row
	if len(tagWhere) > 0 {
		if taggables, err = c.fetch("taggables", strings.Join(tagWhere, " OR ")); err != nil {
			return err
		}
	}
	if ids := idsOf(taggables, "tag_id"); len(ids) > 0 {
		if _, err := c.fetch("tags", "t.id IN "+coolify.SQLIntList(ids)); err != nil {
			return err
		}
	}

	// Context rows: environments, projects, destinations, git sources, keys, S3.
	envs, err := c.fetch("environments", "t.id IN "+coolify.SQLIntList(idsOf(resourceRows, "environment_id")))
	if err != nil {
		return err
	}
	projects, err := c.fetch("projects", "t.id IN "+coolify.SQLIntList(idsOf(envs, "project_id")))
	if err != nil {
		return err
	}
	projectIDs, envIDs := idsOf(projects, "id"), idsOf(envs, "id")
	if _, err := c.fetch("project_settings", "t.project_id IN "+coolify.SQLIntList(projectIDs)); err != nil {
		return err
	}
	if _, err := c.fetch("shared_environment_variables", fmt.Sprintf(
		"(t.type = 'project' AND t.project_id IN %s) OR (t.type = 'environment' AND t.environment_id IN %s)",
		coolify.SQLIntList(projectIDs), coolify.SQLIntList(envIDs))); err != nil {
		return err
	}
	if _, err := c.fetch("standalone_dockers", "t.id IN "+coolify.SQLIntList(morphIDs(resourceRows, "destination_type", "destination_id", coolify.MorphStandaloneDocker))); err != nil {
		return err
	}
	apps := c.ex.Tables["applications"]
	gh, err := c.fetch("github_apps", "t.id <> 0 AND t.id IN "+coolify.SQLIntList(morphIDs(apps, "source_type", "source_id", coolify.MorphGithubApp)))
	if err != nil {
		return err
	}
	gl, err := c.fetch("gitlab_apps", "t.id <> 0 AND t.id IN "+coolify.SQLIntList(morphIDs(apps, "source_type", "source_id", coolify.MorphGitlabApp)))
	if err != nil {
		return err
	}
	keyIDs := append(append(idsOf(apps, "private_key_id"), idsOf(gh, "private_key_id")...), idsOf(gl, "private_key_id")...)
	if _, err := c.fetch("private_keys", "t.id IN "+coolify.SQLIntList(keyIDs)); err != nil {
		return err
	}
	s3IDs := append(idsOf(c.ex.Tables["scheduled_database_backups"], "s3_storage_id"), idsOf(c.ex.Tables["scheduled_volume_backups"], "s3_storage_id")...)
	s3IDs = append(s3IDs, idsOf(projects, "icon_s3_storage_id")...)
	if _, err := c.fetch("s3_storages", "t.id IN "+coolify.SQLIntList(s3IDs)); err != nil {
		return err
	}
	return nil
}

var sharedRefRe = regexp.MustCompile(`\{\{\s*(team|server)\.([A-Za-z0-9_\-]+)\s*\}\}`)

// sharedVariableRefs adds team / server level shared variables that the copied
// environment variables reference ({{team.X}}, {{server.X}}).
func (c *collector) sharedVariableRefs() error {
	teamKeys, serverKeys := map[string]bool{}, map[string]bool{}
	for _, row := range c.ex.Tables["environment_variables"] {
		val, ok := PlainString(row["value"])
		if !ok {
			continue
		}
		for _, m := range sharedRefRe.FindAllStringSubmatch(val, -1) {
			if m[1] == "team" {
				teamKeys[m[2]] = true
			} else {
				serverKeys[m[2]] = true
			}
		}
	}
	var where []string
	if len(teamKeys) > 0 {
		teams := idsOf(c.ex.Tables["projects"], "team_id")
		where = append(where, fmt.Sprintf("(t.type = 'team' AND t.team_id IN %s AND t.key IN %s)", coolify.SQLIntList(teams), coolify.SQLList(sortedKeys(teamKeys))))
	}
	if len(serverKeys) > 0 {
		servers := []int64{}
		for _, r := range c.ex.Roots {
			servers = append(servers, r.ServerID)
		}
		where = append(where, fmt.Sprintf("(t.type = 'server' AND t.server_id IN %s AND t.key IN %s)", coolify.SQLIntList(servers), coolify.SQLList(sortedKeys(serverKeys))))
	}
	if len(where) == 0 {
		return nil
	}
	before := len(c.ex.Tables["shared_environment_variables"])
	if _, err := c.fetch("shared_environment_variables", strings.Join(where, " OR ")); err != nil {
		return err
	}
	for _, row := range c.ex.Tables["shared_environment_variables"][before:] {
		for k, v := range row {
			row[k] = c.norm("shared_environment_variables."+k, v)
		}
	}
	return nil
}

func (c *collector) norm(where string, v any) any {
	switch t := v.(type) {
	case string:
		if !lcrypt.LooksEncrypted(t) {
			return t
		}
		plain, err := c.in.Crypt.DecryptString(t)
		if err != nil {
			c.ex.Warnings = append(c.ex.Warnings, fmt.Sprintf("%s: %v (value copied unchanged)", where, err))
			return t
		}
		return map[string]any{encKey: base64.StdEncoding.EncodeToString(plain)}
	case map[string]any:
		for k, x := range t {
			t[k] = c.norm(where, x)
		}
	case []any:
		for i, x := range t {
			t[i] = c.norm(where, x)
		}
	}
	return v
}

// PlainBytes returns the plaintext of an encrypted marker.
func PlainBytes(v any) ([]byte, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	s, ok := m[encKey].(string)
	if !ok {
		return nil, false
	}
	b, err := base64.StdEncoding.DecodeString(s)
	return b, err == nil
}

// PlainString returns a readable value: plain strings as-is, encrypted markers
// decoded (and PHP-unserialized when they hold a serialized string).
func PlainString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case nil:
		return "", false
	}
	b, ok := PlainBytes(v)
	if !ok {
		return "", false
	}
	if s, ok := phpUnserializeString(b); ok {
		return s, true
	}
	return string(b), true
}

// DecodePlain turns decrypted bytes into text (PHP-unserializing s:N:"..."; values).
func DecodePlain(b []byte) string {
	if s, ok := phpUnserializeString(b); ok {
		return s
	}
	return string(b)
}

// phpUnserializeString decodes s:<len>:"<bytes>";
func phpUnserializeString(b []byte) (string, bool) {
	s := string(b)
	if !strings.HasPrefix(s, "s:") {
		return "", false
	}
	rest := s[2:]
	colon := strings.IndexByte(rest, ':')
	if colon < 0 {
		return "", false
	}
	n, err := strconv.Atoi(rest[:colon])
	if err != nil || n < 0 {
		return "", false
	}
	body := rest[colon+1:]
	if len(body) != n+3 || body[0] != '"' || !strings.HasSuffix(body, `";`) {
		return "", false
	}
	return body[1 : 1+n], true
}

// Int64 converts a decoded JSON number (or numeric string) to int64.
func Int64(v any) (int64, bool) {
	switch t := v.(type) {
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case float64:
		return int64(t), true
	case int64:
		return t, true
	case int:
		return int64(t), true
	case string:
		n, err := strconv.ParseInt(t, 10, 64)
		return n, err == nil
	}
	return 0, false
}

func idsOf(rows []coolify.Row, col string) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, r := range rows {
		if id, ok := Int64(r[col]); ok && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func morphIDs(rows []coolify.Row, typeCol, idCol, morph string) []int64 {
	var sel []coolify.Row
	for _, r := range rows {
		if s, _ := r[typeCol].(string); s == morph {
			sel = append(sel, r)
		}
	}
	return idsOf(sel, idCol)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
