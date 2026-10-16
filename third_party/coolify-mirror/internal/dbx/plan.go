package dbx

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
)

// Decision is what to do with a resource whose uuid already exists on the target.
type Decision int

const (
	// KeepUUID restores the resource with its original uuid (the normal case).
	KeepUUID Decision = iota
	// NewCopy restores it as an independent copy with fresh uuids.
	NewCopy
	// Skip leaves it out.
	Skip
)

// TargetState is what the planner needs to know about the target instance.
type TargetState struct {
	Columns map[string]map[string]string
	TeamID  int64
	// Existing uuids per table (only those that also occur in the export).
	Existing          map[string]map[string]int64
	EnvsByProjectName map[int64]map[string]int64 // target project id -> env name -> env id
	EnvUUID           map[int64]string           // target env id -> uuid
	Destinations      map[string]int64           // network -> standalone_dockers.id on the local server
	TagsByName        map[string]int64
	SharedKeys        map[string]bool // type|scope id|key
	InternalHosts     []string        // Settings > Advanced: allowed internal hosts
}

// PlannedResource is a resource as it will exist on the target.
type PlannedResource struct {
	coolify.Resource
	SourceUUID string `json:"source_uuid"`
	Copy       bool   `json:"copy"`
	// Commit is the git commit of the last successful deployment (image tag).
	Commit string `json:"commit,omitempty"`
	// DeploymentUUID is the restored "last deployment" row on the target.
	DeploymentUUID string `json:"deployment_uuid,omitempty"`
	WasRunning     bool   `json:"was_running"`
	// Expect lists the compose services that were up on the source.
	Expect []string `json:"expect,omitempty"`
	// Hold, when set, keeps the resource stopped and explains why.
	Hold string `json:"hold,omitempty"`
}

// Plan is a fully resolved import.
type Plan struct {
	TeamID    int64
	Renames   map[string]string // source uuid -> target uuid
	Resources []PlannedResource
	Notes     []string
	Warnings  []string
	// Networks that must exist on the target (docker network create).
	Networks []string

	rows  []*planRow
	idMap map[string]map[int64]int64
	enc   func([]byte) (string, error)
	cols  map[string]map[string]string
}

type planRow struct {
	table string
	oldID int64
	newID int64
	row   coolify.Row
}

// Allocator hands out n fresh ids for a table (nextval of its sequence).
type Allocator func(table string, n int) ([]int64, error)

// LoadTarget queries the target instance for everything BuildPlan needs.
func LoadTarget(ctx context.Context, in *coolify.Instance, ex *Export, teamID int64) (*TargetState, error) {
	cols, err := in.Columns(ctx)
	if err != nil {
		return nil, err
	}
	ts := &TargetState{Columns: cols, TeamID: teamID, Existing: map[string]map[string]int64{},
		EnvsByProjectName: map[int64]map[string]int64{}, EnvUUID: map[int64]string{},
		Destinations: map[string]int64{}, TagsByName: map[string]int64{}, SharedKeys: map[string]bool{},
		InternalHosts: readInternalHosts(ctx, in)}

	for table, rows := range ex.Tables {
		col := uuidColumn(table)
		if col == "" || cols[table] == nil {
			continue
		}
		var uuids []string
		for _, r := range rows {
			if s, ok := r[col].(string); ok && s != "" {
				uuids = append(uuids, s)
			}
		}
		if len(uuids) == 0 {
			continue
		}
		var found []struct {
			ID   int64  `json:"id"`
			UUID string `json:"u"`
		}
		q := fmt.Sprintf("SELECT id, %s AS u FROM %s WHERE %s IN %s", coolify.SQLIdent(col), coolify.SQLIdent(table), coolify.SQLIdent(col), coolify.SQLList(uuids))
		if err := in.Query(ctx, q, &found); err != nil {
			return nil, err
		}
		ts.Existing[table] = map[string]int64{}
		for _, f := range found {
			ts.Existing[table][f.UUID] = f.ID
		}
	}

	var envs []struct {
		ID        int64   `json:"id"`
		ProjectID int64   `json:"project_id"`
		Name      string  `json:"name"`
		UUID      *string `json:"uuid"`
	}
	if err := in.Query(ctx, "SELECT id, project_id, name, uuid FROM environments", &envs); err != nil {
		return nil, err
	}
	for _, e := range envs {
		if ts.EnvsByProjectName[e.ProjectID] == nil {
			ts.EnvsByProjectName[e.ProjectID] = map[string]int64{}
		}
		ts.EnvsByProjectName[e.ProjectID][e.Name] = e.ID
		if e.UUID != nil {
			ts.EnvUUID[e.ID] = *e.UUID
		}
	}

	var dests []struct {
		ID      int64  `json:"id"`
		Network string `json:"network"`
	}
	if err := in.Query(ctx, fmt.Sprintf("SELECT id, network FROM standalone_dockers WHERE server_id = %d", coolify.LocalServerID), &dests); err != nil {
		return nil, err
	}
	for _, d := range dests {
		ts.Destinations[d.Network] = d.ID
	}

	var tags []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := in.Query(ctx, fmt.Sprintf("SELECT id, name FROM tags WHERE team_id = %d", teamID), &tags); err != nil {
		return nil, err
	}
	for _, t := range tags {
		ts.TagsByName[t.Name] = t.ID
	}

	var shared []struct {
		Type    string `json:"type"`
		Key     string `json:"key"`
		Team    *int64 `json:"team_id"`
		Project *int64 `json:"project_id"`
		Env     *int64 `json:"environment_id"`
		Server  *int64 `json:"server_id"`
	}
	if err := in.Query(ctx, "SELECT type, key, team_id, project_id, environment_id, server_id FROM shared_environment_variables", &shared); err != nil {
		return nil, err
	}
	for _, s := range shared {
		ts.SharedKeys[sharedScopeKey(s.Type, s.Team, s.Project, s.Env, s.Server, s.Key)] = true
	}
	return ts, nil
}

func sharedScopeKey(typ string, team, project, env, server *int64, key string) string {
	v := func(p *int64) string {
		if p == nil {
			return "-"
		}
		return strconv.FormatInt(*p, 10)
	}
	switch typ {
	case "project":
		return "project|" + v(project) + "|" + key
	case "environment":
		return "environment|" + v(env) + "|" + key
	case "server":
		return "server|" + v(server) + "|" + key
	}
	return "team|" + v(team) + "|" + key
}

// BuildPlan decides, for every exported row, whether it is inserted, mapped to an
// existing target row or skipped, and computes the id and uuid mappings.
func BuildPlan(ex *Export, ts *TargetState, onConflict func(coolify.Resource) Decision,
	alloc Allocator, encrypt func([]byte) (string, error)) (*Plan, error) {

	p := &Plan{TeamID: ts.TeamID, Renames: map[string]string{}, idMap: map[string]map[int64]int64{},
		enc: encrypt, cols: ts.Columns}
	owners := buildOwners(ex)

	// 1. Decide per root resource.
	decision := map[string]Decision{}
	for _, r := range ex.Roots {
		d := KeepUUID
		if _, exists := ts.Existing[r.Table][r.UUID]; exists {
			d = onConflict(r)
		}
		decision[r.UUID] = d
	}
	included := func(table string, row coolify.Row) bool {
		root := owners.rootOf(table, row)
		return root == "" || decision[root] != Skip
	}
	copyMode := func(table string, row coolify.Row) bool {
		root := owners.rootOf(table, row)
		return root != "" && decision[root] == NewCopy
	}

	// 2. Which context rows are needed by the included resource rows.
	need := neededContext(ex, included)

	// 3. Walk tables in insert order.
	counts := map[string]int{}
	existing := func(table string, row coolify.Row) (int64, bool) {
		col := uuidColumn(table)
		if col == "" {
			return 0, false
		}
		u, _ := row[col].(string)
		id, ok := ts.Existing[table][u]
		return id, ok
	}
	mapID := func(table string, oldID, newID int64) {
		if p.idMap[table] == nil {
			p.idMap[table] = map[int64]int64{}
		}
		p.idMap[table][oldID] = newID
	}
	add := func(table string, row coolify.Row) *planRow {
		pr := &planRow{table: table, row: cloneRow(row)}
		pr.oldID, _ = Int64(row["id"])
		p.rows = append(p.rows, pr)
		counts[table]++
		return pr
	}

	for _, table := range insertOrder {
		for _, row := range ex.Tables[table] {
			oldID, _ := Int64(row["id"])
			switch table {
			case "s3_storages", "integration_tokens", "private_keys", "github_apps", "gitlab_apps":
				if !need[table][oldID] {
					continue
				}
				if id, ok := existing(table, row); ok {
					mapID(table, oldID, id)
					p.Notes = append(p.Notes, fmt.Sprintf("uses existing %s %q", humanTable(table), row["name"]))
					continue
				}
				add(table, row)
			case "projects":
				if !need[table][oldID] {
					continue
				}
				if id, ok := existing(table, row); ok {
					mapID(table, oldID, id)
					p.Notes = append(p.Notes, fmt.Sprintf("project %q already exists - resources are added to it", row["name"]))
					continue
				}
				add(table, row)
			case "project_settings":
				pid, _ := Int64(row["project_id"])
				if !need["projects"][pid] || isReused(p, "projects", pid) {
					continue
				}
				add(table, row)
			case "environments":
				if !need[table][oldID] {
					continue
				}
				if id, ok := existing(table, row); ok {
					mapID(table, oldID, id)
					continue
				}
				pid, _ := Int64(row["project_id"])
				if isReused(p, "projects", pid) {
					name, _ := row["name"].(string)
					if id, ok := ts.EnvsByProjectName[p.idMap["projects"][pid]][name]; ok {
						mapID(table, oldID, id)
						// Same environment, other uuid: keep links (deployment URLs) valid.
						if old, _ := row["uuid"].(string); old != "" && ts.EnvUUID[id] != "" {
							p.Renames[old] = ts.EnvUUID[id]
						}
						continue
					}
				}
				add(table, row)
			case "shared_environment_variables":
				typ, _ := row["type"].(string)
				scopeOK := true
				switch typ {
				case "project":
					pid, _ := Int64(row["project_id"])
					scopeOK = need["projects"][pid]
				case "environment":
					eid, _ := Int64(row["environment_id"])
					scopeOK = need["environments"][eid]
				}
				if !scopeOK {
					continue
				}
				add(table, row)
			case "standalone_dockers":
				if !need[table][oldID] {
					continue
				}
				network, _ := row["network"].(string)
				if id, ok := ts.Destinations[network]; ok {
					mapID(table, oldID, id)
					continue
				}
				pr := add(table, row)
				if _, clash := existing(table, row); clash {
					pr.row["uuid"] = newUUIDLike(row["uuid"])
				}
				p.Networks = append(p.Networks, network)
			case "tags":
				if !need[table][oldID] {
					continue
				}
				name, _ := row["name"].(string)
				if id, ok := ts.TagsByName[name]; ok {
					mapID(table, oldID, id)
					continue
				}
				pr := add(table, row)
				if _, clash := existing(table, row); clash {
					pr.row["uuid"] = newUUIDLike(row["uuid"])
				}
			default:
				if !included(table, row) {
					continue
				}
				pr := add(table, row)
				col := uuidColumn(table)
				if col == "" {
					continue
				}
				old, _ := row[col].(string)
				_, clash := existing(table, row)
				if copyMode(table, row) || clash {
					nu := newUUIDLike(old)
					pr.row[col] = nu
					if isResourceTable(table) && old != "" {
						p.Renames[old] = nu
					}
					// Make copies recognizable in the dashboard.
					if name, ok := pr.row["name"].(string); ok && copyMode(table, row) &&
						owners.root[key(table, pr.oldID)] == old && table != "service_applications" && table != "service_databases" {
						pr.row["name"] = name + " (copy)"
					}
				}
			}
		}
	}

	// 4. Allocate ids.
	for _, table := range insertOrder {
		n := counts[table]
		if n == 0 || table == "taggables" {
			continue
		}
		ids, err := alloc(table, n)
		if err != nil {
			return nil, err
		}
		if len(ids) != n {
			return nil, fmt.Errorf("allocator returned %d ids for %s, want %d", len(ids), table, n)
		}
		i := 0
		for _, pr := range p.rows {
			if pr.table == table {
				pr.newID = ids[i]
				mapID(table, pr.oldID, pr.newID)
				i++
			}
		}
	}

	// 5. Shared variables: never overwrite a key that already exists in the same scope.
	var kept []*planRow
	for _, pr := range p.rows {
		if pr.table == "shared_environment_variables" {
			typ, _ := pr.row["type"].(string)
			key, _ := pr.row["key"].(string)
			scope := p.scopeKey(typ, pr.row)
			if ts.SharedKeys[scope] {
				p.Notes = append(p.Notes, fmt.Sprintf("shared variable %s.%s already exists on this server - kept the existing value", typ, key))
				continue
			}
		}
		kept = append(kept, pr)
	}
	p.rows = kept

	// 6. Resources as they will exist on the target.
	lastDeploy := map[string]coolify.Row{}
	for _, pr := range p.rows {
		if pr.table == "application_deployment_queues" {
			appID, _ := Int64(pr.row["application_id"])
			lastDeploy[strconv.FormatInt(appID, 10)] = pr.row
		}
	}
	for _, r := range ex.Roots {
		if decision[r.UUID] == Skip {
			continue
		}
		pl := PlannedResource{Resource: r, SourceUUID: r.UUID, Copy: decision[r.UUID] == NewCopy, WasRunning: r.Running()}
		if nu, ok := p.Renames[r.UUID]; ok {
			pl.UUID = nu
		}
		if pl.Copy {
			pl.Name += " (copy)"
		}
		pl.ID = p.idMap[r.Table][r.ID]
		pl.ServerID = coolify.LocalServerID
		if r.Table == "applications" {
			if d, ok := lastDeploy[strconv.FormatInt(r.ID, 10)]; ok {
				pl.Commit, _ = d["commit"].(string)
				pl.DeploymentUUID, _ = d["deployment_uuid"].(string)
			}
		}
		p.Resources = append(p.Resources, pl)
	}
	sort.Strings(p.Networks)
	return p, nil
}

func (p *Plan) scopeKey(typ string, row coolify.Row) string {
	get := func(col string) *int64 {
		if v, ok := Int64(row[col]); ok {
			return &v
		}
		return nil
	}
	team := p.TeamID
	server := int64(coolify.LocalServerID)
	var project, env *int64
	if v := get("project_id"); v != nil {
		m := p.idMap["projects"][*v]
		project = &m
	}
	if v := get("environment_id"); v != nil {
		m := p.idMap["environments"][*v]
		env = &m
	}
	return sharedScopeKey(typ, &team, project, env, &server, fmt.Sprint(row["key"]))
}

func isReused(p *Plan, table string, oldID int64) bool {
	if _, ok := p.idMap[table][oldID]; !ok {
		return false
	}
	for _, pr := range p.rows {
		if pr.table == table && pr.oldID == oldID {
			return false
		}
	}
	return true
}

func humanTable(t string) string {
	switch t {
	case "s3_storages":
		return "S3 storage"
	case "private_keys":
		return "private key"
	case "github_apps":
		return "GitHub App"
	case "gitlab_apps":
		return "GitLab App"
	case "integration_tokens":
		return "secret manager"
	}
	return t
}

func cloneRow(r coolify.Row) coolify.Row {
	out := make(coolify.Row, len(r))
	for k, v := range r {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, x := range t {
			m[k] = cloneValue(x)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, x := range t {
			s[i] = cloneValue(x)
		}
		return s
	}
	return v
}

// newUUIDLike returns a random lowercase alphanumeric id with the same length
// as old (Coolify's new_public_id format), so serialized lengths stay valid.
func newUUIDLike(old any) string {
	n := 24
	if s, ok := old.(string); ok && len(s) >= 7 {
		n = len(s)
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	for i := range b {
		b[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(b)
}

// --- ownership ---------------------------------------------------------------

type ownerIndex struct {
	root map[string]string // "table#id" -> root uuid
	ex   *Export
}

func key(table string, id int64) string { return table + "#" + strconv.FormatInt(id, 10) }

func buildOwners(ex *Export) *ownerIndex {
	o := &ownerIndex{root: map[string]string{}, ex: ex}
	for _, r := range ex.Roots {
		o.root[key(r.Table, r.ID)] = r.UUID
	}
	for _, row := range ex.Tables["service_applications"] {
		o.link("service_applications", row, "services", row["service_id"])
	}
	for _, row := range ex.Tables["service_databases"] {
		o.link("service_databases", row, "services", row["service_id"])
	}
	return o
}

func (o *ownerIndex) link(table string, row coolify.Row, parentTable string, parentID any) {
	id, ok1 := Int64(row["id"])
	pid, ok2 := Int64(parentID)
	if ok1 && ok2 {
		if root, ok := o.root[key(parentTable, pid)]; ok {
			o.root[key(table, id)] = root
		}
	}
}

// rootOf finds the root resource uuid owning a row ("" for context rows).
func (o *ownerIndex) rootOf(table string, row coolify.Row) string {
	id, _ := Int64(row["id"])
	if r, ok := o.root[key(table, id)]; ok {
		return r
	}
	parent := func(t string, v any) string {
		pid, ok := Int64(v)
		if !ok {
			return ""
		}
		return o.root[key(t, pid)]
	}
	morph := func(typeCol, idCol string) string {
		t, _ := row[typeCol].(string)
		pt, ok := coolify.MorphToTable[t]
		if !ok {
			return ""
		}
		if pt == "local_persistent_volumes" || pt == "local_file_volumes" {
			pid, _ := Int64(row[idCol])
			for _, vr := range o.ex.Tables[pt] {
				if vid, _ := Int64(vr["id"]); vid == pid {
					return o.rootOf(pt, vr)
				}
			}
			return ""
		}
		return parent(pt, row[idCol])
	}
	switch table {
	case "application_settings":
		return parent("applications", row["application_id"])
	case "application_deployment_queues":
		return parent("applications", row["application_id"])
	case "scheduled_tasks":
		if r := parent("applications", row["application_id"]); r != "" {
			return r
		}
		return parent("services", row["service_id"])
	case "environment_variables", "secret_manager_links":
		return morph("resourceable_type", "resourceable_id")
	case "local_persistent_volumes", "local_file_volumes":
		return morph("resource_type", "resource_id")
	case "scheduled_database_backups":
		return morph("database_type", "database_id")
	case "scheduled_volume_backups":
		return morph("backupable_type", "backupable_id")
	case "taggables":
		return morph("taggable_type", "taggable_id")
	}
	return ""
}

// neededContext returns, per context table, the source ids referenced by the
// resource rows that are going to be imported.
func neededContext(ex *Export, included func(string, coolify.Row) bool) map[string]map[int64]bool {
	need := map[string]map[int64]bool{}
	mark := func(t string, v any) {
		if id, ok := Int64(v); ok {
			if need[t] == nil {
				need[t] = map[int64]bool{}
			}
			need[t][id] = true
		}
	}
	for table, rows := range ex.Tables {
		if !isResourceTable(table) && table != "scheduled_database_backups" && table != "scheduled_volume_backups" && table != "taggables" {
			continue
		}
		for _, row := range rows {
			if !included(table, row) {
				continue
			}
			mark("environments", row["environment_id"])
			if dt, _ := row["destination_type"].(string); dt == coolify.MorphStandaloneDocker {
				mark("standalone_dockers", row["destination_id"])
			}
			switch st, _ := row["source_type"].(string); st {
			case coolify.MorphGithubApp:
				mark("github_apps", row["source_id"])
			case coolify.MorphGitlabApp:
				mark("gitlab_apps", row["source_id"])
			}
			mark("private_keys", row["private_key_id"])
			mark("s3_storages", row["s3_storage_id"])
			mark("tags", row["tag_id"])
		}
	}
	for _, row := range ex.Tables["secret_manager_links"] {
		if included("secret_manager_links", row) {
			mark("integration_tokens", row["integration_token_id"])
		}
	}
	for _, row := range ex.Tables["environments"] {
		if id, _ := Int64(row["id"]); need["environments"][id] {
			mark("projects", row["project_id"])
		}
	}
	for _, t := range []string{"github_apps", "gitlab_apps"} {
		for _, row := range ex.Tables[t] {
			if id, _ := Int64(row["id"]); need[t][id] {
				mark("private_keys", row["private_key_id"])
			}
		}
	}
	for _, row := range ex.Tables["projects"] {
		if id, _ := Int64(row["id"]); need["projects"][id] {
			mark("s3_storages", row["icon_s3_storage_id"])
		}
	}
	return need
}

// --- row transformation --------------------------------------------------------

var appIDLabelRe = regexp.MustCompile(`coolify\.applicationId=\d+`)

// Rename applies the uuid renames of this plan to s.
func (p *Plan) Rename(s string) string {
	if len(p.Renames) == 0 || s == "" {
		return s
	}
	for old, nu := range p.Renames {
		if strings.Contains(s, old) {
			s = strings.ReplaceAll(s, old, nu)
		}
	}
	return s
}

func (p *Plan) renameValue(v any) any {
	switch t := v.(type) {
	case string:
		return p.Rename(t)
	case map[string]any:
		if b, ok := PlainBytes(t); ok {
			if s, ok := phpUnserializeString(b); ok {
				r := p.Rename(s)
				b = []byte(fmt.Sprintf("s:%d:\"%s\";", len(r), r))
			} else {
				b = []byte(p.Rename(string(b)))
			}
			return map[string]any{encKey: base64.StdEncoding.EncodeToString(b)}
		}
		for k, x := range t {
			t[k] = p.renameValue(x)
		}
	case []any:
		for i, x := range t {
			t[i] = p.renameValue(x)
		}
	}
	return v
}

func (p *Plan) encryptValue(v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		if b, ok := PlainBytes(t); ok {
			return p.enc(b)
		}
		for k, x := range t {
			nv, err := p.encryptValue(x)
			if err != nil {
				return nil, err
			}
			t[k] = nv
		}
	case []any:
		for i, x := range t {
			nv, err := p.encryptValue(x)
			if err != nil {
				return nil, err
			}
			t[i] = nv
		}
	}
	return v, nil
}

// finalRow produces the row exactly as it will be inserted.
func (p *Plan) finalRow(pr *planRow) (coolify.Row, error) {
	row := pr.row
	for k, v := range row {
		row[k] = p.renameValue(v)
	}
	if pr.table != "taggables" {
		row["id"] = pr.newID
	}
	for _, f := range foreignKeys[pr.table] {
		v, present := row[f.Col]
		if !present || v == nil {
			continue
		}
		old, ok := Int64(v)
		if !ok {
			continue
		}
		target := f.Table
		if f.MorphCol != "" {
			mt, _ := row[f.MorphCol].(string)
			target = coolify.MorphToTable[mt]
		}
		var nv any
		switch target {
		case refTeam:
			nv = p.TeamID
		case refServer:
			nv = int64(coolify.LocalServerID)
		case refNull, "":
			nv = nil
		default:
			if id, ok := p.idMap[target][old]; ok {
				nv = id
			} else if old == 0 && (target == "github_apps" || target == "gitlab_apps") {
				nv = int64(0) // the built-in public GitHub / GitLab source
			} else {
				nv = nil
				if pr.table != "projects" { // icons are optional
					p.Warnings = append(p.Warnings, fmt.Sprintf("%s.%s=%d has no counterpart on this server; cleared", pr.table, f.Col, old))
				}
			}
		}
		if f.AsText && nv != nil {
			nv = fmt.Sprint(nv)
		}
		row[f.Col] = nv
	}
	switch pr.table {
	case "applications":
		row["status"] = "exited"
		if _, ok := row["container_present"]; ok {
			row["container_present"] = false
		}
		row["restart_count"] = 0
		row["restart_limit_reached"] = false
		if s, ok := row["custom_labels"].(string); ok && s != "" {
			row["custom_labels"] = rewriteLabels(s, pr.newID, p.Rename)
		}
	case "service_applications", "service_databases":
		row["status"] = "exited"
	case "services":
		row["server_id"] = int64(coolify.LocalServerID)
	}
	if _, ok := coolify.DatabaseKindByTable(pr.table); ok {
		row["status"] = "exited"
		row["started_at"] = nil
	}
	for k, v := range row {
		nv, err := p.encryptValue(v)
		if err != nil {
			return nil, err
		}
		row[k] = nv
	}
	// Only columns that exist on the target (it may run another Coolify version).
	cols := p.cols[pr.table]
	for k := range row {
		if _, ok := cols[k]; !ok {
			delete(row, k)
		}
	}
	return row, nil
}

func rewriteLabels(b64 string, appID int64, rename func(string) string) string {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return rename(b64)
	}
	s := appIDLabelRe.ReplaceAllString(string(raw), fmt.Sprintf("coolify.applicationId=%d", appID))
	return base64.StdEncoding.EncodeToString([]byte(rename(s)))
}
