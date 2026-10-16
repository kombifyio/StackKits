package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
	"github.com/mtalavi/coolify-mirror/internal/dbx"
)

// Dependency explains why a resource was added automatically.
type Dependency struct {
	Resource coolify.Resource
	Reason   string
}

// ResolveDependencies finds resources that the selected ones reference by uuid
// in their environment variables or compose files (typically databases:
// a postgres URL whose host is <db-uuid>) and returns the ones not selected yet.
func ResolveDependencies(ctx context.Context, in *coolify.Instance, selected, all []coolify.Resource) ([]Dependency, error) {
	chosen := map[string]bool{}
	for _, r := range selected {
		chosen[r.UUID] = true
	}
	var deps []Dependency
	queue := append([]coolify.Resource(nil), selected...)
	for len(queue) > 0 {
		texts, err := referenceTexts(ctx, in, queue)
		if err != nil {
			return nil, err
		}
		queue = nil
		for _, cand := range all {
			if chosen[cand.UUID] || len(cand.UUID) < 7 {
				continue
			}
			for _, t := range texts {
				if strings.Contains(t.text, cand.UUID) {
					chosen[cand.UUID] = true
					deps = append(deps, Dependency{Resource: cand, Reason: fmt.Sprintf("used by %s (%s)", t.owner, t.where)})
					queue = append(queue, cand)
					break
				}
			}
		}
	}
	return deps, nil
}

type refText struct {
	owner, where, text string
}

func referenceTexts(ctx context.Context, in *coolify.Instance, rs []coolify.Resource) ([]refText, error) {
	var out []refText
	byMorph := map[string][]int64{}
	names := map[string]string{}
	for _, r := range rs {
		byMorph[r.Morph()] = append(byMorph[r.Morph()], r.ID)
		names[r.Morph()+"#"+fmt.Sprint(r.ID)] = r.Name
	}
	var where []string
	for m, ids := range byMorph {
		where = append(where, fmt.Sprintf("(resourceable_type = %s AND resourceable_id IN %s)", coolify.SQLString(m), coolify.SQLIntList(ids)))
	}
	var envs []struct {
		Type  string  `json:"resourceable_type"`
		ID    int64   `json:"resourceable_id"`
		Key   string  `json:"key"`
		Value *string `json:"value"`
	}
	if len(where) > 0 {
		if err := in.Query(ctx, "SELECT resourceable_type, resourceable_id, key, value FROM environment_variables WHERE "+strings.Join(where, " OR "), &envs); err != nil {
			return nil, err
		}
	}
	for _, e := range envs {
		if e.Value == nil {
			continue
		}
		val := *e.Value
		if plain, err := in.Crypt.DecryptString(val); err == nil {
			val = dbx.DecodePlain(plain)
		}
		out = append(out, refText{owner: names[e.Type+"#"+fmt.Sprint(e.ID)], where: e.Key, text: val})
	}
	// A SQLite database connected to an application (Coolify 4.4+) is mounted
	// into it as a volume that points at the database.
	if in.HasTable(ctx, "standalone_sqlites") {
		var vwhere []string
		for m, ids := range byMorph {
			vwhere = append(vwhere, fmt.Sprintf("(v.resource_type = %s AND v.resource_id IN %s)", coolify.SQLString(m), coolify.SQLIntList(ids)))
		}
		var vols []struct {
			Type string `json:"resource_type"`
			ID   int64  `json:"resource_id"`
			Name string `json:"name"`
			UUID string `json:"uuid"`
		}
		if len(vwhere) > 0 {
			if err := in.Query(ctx, "SELECT v.resource_type, v.resource_id, v.name, s.uuid FROM local_persistent_volumes v JOIN standalone_sqlites s ON s.id = v.standalone_sqlite_id WHERE "+
				strings.Join(vwhere, " OR "), &vols); err != nil {
				return nil, err
			}
		}
		for _, v := range vols {
			out = append(out, refText{owner: names[v.Type+"#"+fmt.Sprint(v.ID)], where: "connected SQLite volume " + v.Name, text: v.UUID})
		}
	}
	for _, t := range []string{"applications", "services"} {
		var ids []int64
		for _, r := range rs {
			if r.Table == t {
				ids = append(ids, r.ID)
			}
		}
		if len(ids) == 0 {
			continue
		}
		var rows []struct {
			ID      int64   `json:"id"`
			Name    string  `json:"name"`
			Compose *string `json:"docker_compose_raw"`
		}
		if err := in.Query(ctx, fmt.Sprintf("SELECT id, name, docker_compose_raw FROM %s WHERE id IN %s", t, coolify.SQLIntList(ids)), &rows); err != nil {
			return nil, err
		}
		for _, r := range rows {
			if r.Compose != nil {
				out = append(out, refText{owner: r.Name, where: "docker compose", text: *r.Compose})
			}
		}
	}
	return out, nil
}
