package coolify

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Resource is one deployable thing in Coolify: an application, a service
// (one-click / compose stack) or a standalone database.
type Resource struct {
	Kind            string   `json:"kind"` // application, service, postgresql, mysql, ...
	Table           string   `json:"table"`
	ID              int64    `json:"id"`
	UUID            string   `json:"uuid"`
	Name            string   `json:"name"`
	Domains         []string `json:"domains,omitempty"`
	Project         string   `json:"project"`
	ProjectUUID     string   `json:"project_uuid"`
	Environment     string   `json:"environment"`
	EnvironmentUUID string   `json:"environment_uuid"`
	ServerID        int64    `json:"server_id"`
	ServerName      string   `json:"server_name"`
	Status          string   `json:"status"`
	BuildPack       string   `json:"build_pack,omitempty"`
}

// appDomains returns the domains an application is served on. A Docker
// Compose application is routed only through its per-service domains; the
// fqdn column Coolify fills in at creation is not used for it.
func appDomains(buildPack, fqdn, compose string) []string {
	if buildPack == "dockercompose" {
		return composeDomains(compose)
	}
	return append(SplitDomains(fqdn), composeDomains(compose)...)
}

// Local reports whether the resource runs on the Coolify host itself.
func (r Resource) Local() bool { return r.ServerID == LocalServerID }

// Running reports whether Coolify last saw the resource running.
func (r Resource) Running() bool { return strings.HasPrefix(r.Status, "running") }

// Morph returns the model class of the resource.
func (r Resource) Morph() string { return TableToMorph[r.Table] }

// IsDatabase reports whether this is a standalone database.
func (r Resource) IsDatabase() bool { return strings.HasPrefix(r.Table, "standalone_") }

// Label is a short human description ("app", "service", "postgresql").
func (r Resource) Label() string {
	if r.Kind == "application" {
		return "app"
	}
	return r.Kind
}

type resRow struct {
	ID              int64            `json:"id"`
	UUID            string           `json:"uuid"`
	Name            string           `json:"name"`
	Fqdn            *string          `json:"fqdn"`
	ComposeDomains  *string          `json:"docker_compose_domains"`
	BuildPack       *string          `json:"build_pack"`
	Status          *string          `json:"status"`
	Project         string           `json:"project"`
	ProjectUUID     string           `json:"project_uuid"`
	Environment     string           `json:"environment"`
	EnvironmentUUID *string          `json:"environment_uuid"`
	ServerID        *int64           `json:"server_id"`
	ServerName      *string          `json:"server_name"`
	Extra           *json.RawMessage `json:"extra"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

const envJoin = `JOIN environments e ON e.id = t.environment_id
JOIN projects p ON p.id = e.project_id`

const destJoin = `LEFT JOIN standalone_dockers sd ON t.destination_type = 'App\Models\StandaloneDocker' AND sd.id = t.destination_id
LEFT JOIN swarm_dockers sw ON t.destination_type = 'App\Models\SwarmDocker' AND sw.id = t.destination_id
LEFT JOIN servers s ON s.id = COALESCE(sd.server_id, sw.server_id)`

// ListResources returns every non-deleted application, service and database.
func (in *Instance) ListResources(ctx context.Context) ([]Resource, error) {
	var out []Resource

	var apps []resRow
	err := in.Query(ctx, `SELECT t.id, t.uuid, t.name, t.fqdn, t.docker_compose_domains, t.build_pack, t.status,
  p.name AS project, p.uuid AS project_uuid, e.name AS environment, e.uuid AS environment_uuid,
  COALESCE(sd.server_id, sw.server_id) AS server_id, s.name AS server_name
FROM applications t `+envJoin+` `+destJoin+`
WHERE t.deleted_at IS NULL`, &apps)
	if err != nil {
		return nil, err
	}
	for _, r := range apps {
		res := base(r, "application", "applications")
		res.BuildPack = str(r.BuildPack)
		res.Domains = appDomains(res.BuildPack, str(r.Fqdn), str(r.ComposeDomains))
		out = append(out, res)
	}

	var svcs []resRow
	err = in.Query(ctx, `SELECT t.id, t.uuid, t.name, t.server_id, s.name AS server_name,
  p.name AS project, p.uuid AS project_uuid, e.name AS environment, e.uuid AS environment_uuid,
  (SELECT string_agg(sa.fqdn, ',') FROM service_applications sa
     WHERE sa.service_id = t.id AND sa.deleted_at IS NULL AND COALESCE(sa.fqdn, '') <> '') AS fqdn,
  (SELECT string_agg(x.status, ',') FROM (
     SELECT status FROM service_applications WHERE service_id = t.id AND deleted_at IS NULL AND NOT exclude_from_status
     UNION ALL
     SELECT status FROM service_databases WHERE service_id = t.id AND deleted_at IS NULL AND NOT exclude_from_status) x) AS status
FROM services t `+envJoin+`
LEFT JOIN servers s ON s.id = t.server_id
WHERE t.deleted_at IS NULL`, &svcs)
	if err != nil {
		return nil, err
	}
	for _, r := range svcs {
		res := base(r, "service", "services")
		res.Domains = SplitDomains(str(r.Fqdn))
		res.Status = serviceStatus(str(r.Status))
		out = append(out, res)
	}

	for _, d := range in.Kinds(ctx) {
		var dbs []resRow
		err = in.Query(ctx, fmt.Sprintf(`SELECT t.id, t.uuid, t.name, t.status,
  p.name AS project, p.uuid AS project_uuid, e.name AS environment, e.uuid AS environment_uuid,
  COALESCE(sd.server_id, sw.server_id) AS server_id, s.name AS server_name
FROM %s t `+envJoin+` `+destJoin+`
WHERE t.deleted_at IS NULL`, d.Table), &dbs)
		if err != nil {
			return nil, err
		}
		for _, r := range dbs {
			out = append(out, base(r, d.Kind, d.Table))
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (len(a.Domains) > 0) != (len(b.Domains) > 0) {
			return len(a.Domains) > 0
		}
		if a.Project != b.Project {
			return strings.ToLower(a.Project) < strings.ToLower(b.Project)
		}
		if a.Environment != b.Environment {
			return a.Environment < b.Environment
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return out, nil
}

func base(r resRow, kind, table string) Resource {
	res := Resource{
		Kind: kind, Table: table, ID: r.ID, UUID: r.UUID, Name: r.Name,
		Project: r.Project, ProjectUUID: r.ProjectUUID, Environment: r.Environment,
		EnvironmentUUID: str(r.EnvironmentUUID), ServerName: str(r.ServerName), Status: str(r.Status),
		ServerID: -1,
	}
	if r.ServerID != nil {
		res.ServerID = *r.ServerID
	}
	return res
}

// serviceStatus folds the statuses of a service's containers into one.
func serviceStatus(all string) string {
	if all == "" {
		return "exited"
	}
	running, other := 0, 0
	for _, s := range strings.Split(all, ",") {
		if strings.HasPrefix(s, "running") {
			running++
		} else {
			other++
		}
	}
	switch {
	case running > 0 && other == 0:
		return "running"
	case running > 0:
		return "degraded"
	default:
		return "exited"
	}
}

// SplitDomains turns Coolify's comma separated fqdn column into URLs.
func SplitDomains(fqdn string) []string {
	var out []string
	for _, d := range strings.Split(fqdn, ",") {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// composeDomains parses applications.docker_compose_domains, which is
// {"service": {"domain": "https://a,https://b"}} (older rows may hold plain strings).
func composeDomains(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		switch v := m[k].(type) {
		case string:
			out = append(out, SplitDomains(v)...)
		case map[string]any:
			if s, ok := v["domain"].(string); ok {
				out = append(out, SplitDomains(s)...)
			}
		}
	}
	return out
}

// Host strips the scheme from a Coolify domain URL for display.
func Host(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	return strings.TrimSuffix(u, "/")
}

// ShowHost is a domain for display: Host without the port. In Coolify the
// port of a domain (vemela.app:3000) is the container's port, not one that
// visitors use.
func ShowHost(u string) string {
	h := Host(u)
	host, path, hasPath := strings.Cut(h, "/")
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.Contains(host, "]") {
		if _, err := strconv.Atoi(host[i+1:]); err == nil {
			host = host[:i]
		}
	}
	if hasPath {
		return host + "/" + path
	}
	return host
}
