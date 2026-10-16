package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
	"github.com/mtalavi/coolify-mirror/internal/dbx"
)

// DomainField is one editable domain setting of a restored resource: an
// application's fqdn, one service of a compose application, or one
// application inside a Coolify service.
type DomainField struct {
	Resource string // resource uuid
	Label    string // "shop" or "wordpress · wordpress"
	Kind     string // application | compose | service_app
	Service  string // compose service name (Kind compose)
	ID       int64  // service_applications.id (Kind service_app)
	Value    string // comma separated URLs, as Coolify stores them
	Original string
}

// DomainFields lists the domain settings of the restored resources, so the
// user can change them as the last step before anything starts.
func DomainFields(ctx context.Context, in *coolify.Instance, res []dbx.PlannedResource) ([]DomainField, error) {
	var out []DomainField
	for _, r := range res {
		switch r.Table {
		case "applications":
			var rows []struct {
				Fqdn      *string `json:"fqdn"`
				BuildPack *string `json:"build_pack"`
				Compose   *string `json:"docker_compose_domains"`
			}
			if err := in.Query(ctx, "SELECT fqdn, build_pack, docker_compose_domains FROM applications WHERE uuid = "+coolify.SQLString(r.UUID), &rows); err != nil {
				return nil, err
			}
			if len(rows) == 0 {
				continue
			}
			row := rows[0]
			if deref(row.BuildPack) == "dockercompose" {
				var m map[string]any
				_ = json.Unmarshal([]byte(deref(row.Compose)), &m)
				type entry struct{ service, domain string }
				entries := make([]entry, 0, len(m))
				for k, raw := range m {
					v := ""
					switch x := raw.(type) {
					case string:
						v = x
					case map[string]any:
						v, _ = x["domain"].(string)
					}
					entries = append(entries, entry{k, strings.TrimSpace(v)})
				}
				// Services with a domain first; the ones without are marked,
				// so a web domain is not typed into a worker or a job by mistake.
				sort.Slice(entries, func(i, j int) bool {
					if a, b := entries[i].domain != "", entries[j].domain != ""; a != b {
						return a
					}
					return entries[i].service < entries[j].service
				})
				for _, e := range entries {
					label := r.Name + " · " + e.service
					if e.domain == "" {
						label += "  (had no domain)"
					}
					out = append(out, DomainField{Resource: r.UUID, Label: label, Kind: "compose", Service: e.service, Value: e.domain, Original: e.domain})
				}
				continue
			}
			v := deref(row.Fqdn)
			out = append(out, DomainField{Resource: r.UUID, Label: r.Name, Kind: "application", Value: v, Original: v})
		case "services":
			var rows []struct {
				ID   int64   `json:"id"`
				Name string  `json:"name"`
				Fqdn *string `json:"fqdn"`
			}
			if err := in.Query(ctx, `SELECT sa.id, sa.name, sa.fqdn FROM service_applications sa JOIN services s ON s.id = sa.service_id
WHERE s.uuid = `+coolify.SQLString(r.UUID)+` AND sa.deleted_at IS NULL AND COALESCE(sa.fqdn, '') <> '' ORDER BY sa.name`, &rows); err != nil {
				return nil, err
			}
			for _, row := range rows {
				v := deref(row.Fqdn)
				out = append(out, DomainField{Resource: r.UUID, Label: r.Name + " · " + row.Name, Kind: "service_app", ID: row.ID, Value: v, Original: v})
			}
		}
	}
	return out, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// NormalizeDomains cleans a user typed domain list: "a.com, http://b.com"
// becomes "https://a.com,http://b.com". It returns an error for a bad entry.
// RiskyComposeDomainMoves finds the likely slip in the domain step of a
// Docker Compose app: a service that had domains loses one while a service
// that had none (a worker, a migration or a backup job) gets one. Domains
// are compared one by one, so moving one of several domains counts too.
// Giving a domain to a service that had none is fine on its own.
func RiskyComposeDomainMoves(fields []DomainField) []string {
	type change struct{ lost, gained []string }
	byResource := map[string]*change{}
	var order []string
	for _, f := range fields {
		if f.Kind != "compose" {
			continue
		}
		before, after := hostSet(f.Original), hostSet(f.Value)
		c := byResource[f.Resource]
		if c == nil {
			c = &change{}
			byResource[f.Resource] = c
			order = append(order, f.Resource)
		}
		if len(before) == 0 {
			if len(after) > 0 {
				c.gained = append(c.gained, f.Service+" ("+strings.Join(sortedKeys(after), ", ")+")")
			}
			continue
		}
		var lost []string
		for h := range before {
			if !after[h] {
				lost = append(lost, h)
			}
		}
		if len(lost) > 0 {
			sort.Strings(lost)
			c.lost = append(c.lost, f.Service+" ("+strings.Join(lost, ", ")+")")
		}
	}
	var out []string
	for _, res := range order {
		c := byResource[res]
		if len(c.lost) == 0 || len(c.gained) == 0 {
			continue
		}
		sort.Strings(c.lost)
		sort.Strings(c.gained)
		out = append(out, fmt.Sprintf("taken off %s; given to %s, which had none",
			strings.Join(c.lost, ", "), strings.Join(c.gained, ", ")))
	}
	return out
}

// hostSet is the set of host names (lower case, no scheme) in a domain value.
func hostSet(v string) map[string]bool {
	out := map[string]bool{}
	for _, d := range coolify.SplitDomains(v) {
		out[strings.ToLower(strings.TrimSuffix(coolify.Host(d), "/"))] = true
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func NormalizeDomains(s string) (string, error) {
	var out []string
	for _, d := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if !strings.HasPrefix(d, "http://") && !strings.HasPrefix(d, "https://") {
			d = "https://" + d
		}
		host := coolify.Host(d)
		if i := strings.IndexAny(host, "/:"); i >= 0 {
			host = host[:i]
		}
		if host == "" || strings.ContainsAny(host, "\"'`$;|&<>{}") || !strings.Contains(host, ".") && host != "localhost" {
			return "", fmt.Errorf("%q is not a valid domain", d)
		}
		out = append(out, strings.TrimSuffix(d, "/"))
	}
	return strings.Join(out, ","), nil
}

// ApplyDomains saves the changed fields through Coolify itself (so services
// regenerate their compose file and SERVICE_URL_/SERVICE_FQDN_ variables),
// then refreshes each resource's Domains and Hold: a resource whose domain is
// still used by another resource on this server stays stopped.
func ApplyDomains(ctx context.Context, in *coolify.Instance, res []dbx.PlannedResource, fields []DomainField) error {
	var items []map[string]any
	for _, f := range fields {
		if f.Value == f.Original {
			continue
		}
		items = append(items, map[string]any{"kind": f.Kind, "uuid": f.Resource, "service": f.Service, "id": f.ID, "fqdn": f.Value})
	}
	if len(items) > 0 {
		var out struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if err := in.PHP(ctx, "set_domains", items, &out); err != nil {
			return err
		}
		if !out.OK {
			return fmt.Errorf("could not save the domains: %s", out.Error)
		}
	}
	return refreshHolds(ctx, in, res)
}

func refreshHolds(ctx context.Context, in *coolify.Instance, res []dbx.PlannedResource) error {
	all, err := in.ListResources(ctx)
	if err != nil {
		return err
	}
	owners := map[string][]coolify.Resource{}
	byUUID := map[string]coolify.Resource{}
	for _, r := range all {
		byUUID[r.UUID] = r
		for _, d := range r.Domains {
			owners[coolify.Host(d)] = append(owners[coolify.Host(d)], r)
		}
	}
	for i := range res {
		cur, ok := byUUID[res[i].UUID]
		if !ok {
			continue
		}
		res[i].Domains = cur.Domains
		var clashes []string
		for _, d := range cur.Domains {
			for _, o := range owners[coolify.Host(d)] {
				if o.UUID != cur.UUID {
					clashes = append(clashes, coolify.Host(d)+" (used by "+o.Name+")")
					break
				}
			}
		}
		res[i].Hold = ""
		if len(clashes) > 0 {
			res[i].Hold = "not started: " + strings.Join(clashes, ", ") + " - change the domain in Coolify, then deploy"
		}
	}
	return nil
}
