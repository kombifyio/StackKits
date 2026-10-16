package coolify

import (
	"fmt"
	"sort"
	"strings"
)

// Project is one environment of a Coolify project with the resources in it:
// what the dashboard shows as a project card. A project with resources in
// several environments (production, staging, ...) becomes one Project per
// environment, marked MultiEnv.
type Project struct {
	Name            string
	UUID            string
	Environment     string
	EnvironmentUUID string
	MultiEnv        bool
	Resources       []Resource
}

// GroupProjects groups resources by project environment. Projects with every
// resource running come first, then partly running, then stopped ones; each
// group by name, like the dashboard.
func GroupProjects(rs []Resource) []Project {
	idx := map[string]int{}
	var out []Project
	envs := map[string]map[string]bool{}
	for _, r := range rs {
		k := r.ProjectUUID + "/" + r.EnvironmentUUID
		if r.EnvironmentUUID == "" {
			k = r.ProjectUUID + "/" + r.Environment
		}
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, Project{Name: r.Project, UUID: r.ProjectUUID, Environment: r.Environment, EnvironmentUUID: r.EnvironmentUUID})
		}
		out[i].Resources = append(out[i].Resources, r)
		if envs[r.ProjectUUID] == nil {
			envs[r.ProjectUUID] = map[string]bool{}
		}
		envs[r.ProjectUUID][r.Environment] = true
	}
	for i := range out {
		out[i].MultiEnv = len(envs[out[i].UUID]) > 1
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := a.runRank(), b.runRank(); ra != rb {
			return ra < rb
		}
		if na, nb := strings.ToLower(a.Name), strings.ToLower(b.Name); na != nb {
			return na < nb
		}
		return a.Environment < b.Environment
	})
	return out
}

// Title is the project name, with the environment when the project has more
// than one.
func (p Project) Title() string {
	if p.MultiEnv {
		return p.Name + " · " + p.Environment
	}
	return p.Name
}

// Domains of every resource in the project, the main one first: the
// shortest host (shop.com before api.shop.com and generated sslip.io names),
// applications before services.
func (p Project) Domains() []string {
	out := p.allDomains()
	SortMainFirst(out)
	return out
}

// SortMainFirst orders domains main domain first: fewest dots, then shortest
// (vemela.app before api.vemela.app).
func SortMainFirst(ds []string) {
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := hostOf(ds[i]), hostOf(ds[j])
		if da, db := strings.Count(a, "."), strings.Count(b, "."); da != db {
			return da < db
		}
		return len(a) < len(b)
	})
}

// hostOf is the host name of a domain, without path or port.
func hostOf(d string) string {
	h := strings.Split(Host(d), "/")[0]
	return strings.Split(h, ":")[0]
}

func (p Project) allDomains() []string {
	var out []string
	seen := map[string]bool{}
	add := func(r Resource) {
		for _, d := range r.Domains {
			if h := Host(d); !seen[h] {
				seen[h] = true
				out = append(out, h)
			}
		}
	}
	for _, r := range p.Resources {
		if r.Kind == "application" {
			add(r)
		}
	}
	for _, r := range p.Resources {
		if r.Kind != "application" {
			add(r)
		}
	}
	return out
}

// Running counts the resources Coolify last saw running.
func (p Project) Running() int {
	n := 0
	for _, r := range p.Resources {
		if r.Running() {
			n++
		}
	}
	return n
}

// Degraded counts services with some of their containers running.
func (p Project) Degraded() int {
	n := 0
	for _, r := range p.Resources {
		if r.Status == "degraded" {
			n++
		}
	}
	return n
}

// RunState is "running" (everything runs), "partly" (something runs) or
// "stopped" (nothing runs).
func (p Project) RunState() string {
	switch n := p.Running(); {
	case n == len(p.Resources):
		return "running"
	case n > 0 || p.Degraded() > 0:
		return "partly"
	}
	return "stopped"
}

func (p Project) runRank() int {
	switch p.RunState() {
	case "running":
		return 0
	case "partly":
		return 1
	}
	return 2
}

// Local returns the resources on the Coolify host itself; the others run on
// remote servers.
func (p Project) Local() (local, remote []Resource) {
	for _, r := range p.Resources {
		if r.Local() {
			local = append(local, r)
		} else {
			remote = append(remote, r)
		}
	}
	return local, remote
}

// Kinds summarises what is in the project: "app", "2 apps, postgresql".
func (p Project) Kinds() string {
	count := map[string]int{}
	var order []string
	for _, r := range p.Resources {
		l := r.Label()
		if count[l] == 0 {
			order = append(order, l)
		}
		count[l]++
	}
	sort.SliceStable(order, func(i, j int) bool { return kindRank(order[i]) < kindRank(order[j]) })
	parts := make([]string, len(order))
	for i, l := range order {
		parts[i] = l
		if n := count[l]; n > 1 {
			parts[i] = fmt.Sprintf("%d %ss", n, l)
		}
	}
	return strings.Join(parts, ", ")
}

func kindRank(label string) int {
	switch label {
	case "app":
		return 0
	case "service":
		return 1
	}
	return 2
}

// MatchProject reports whether name picks project p: its name, its uuid,
// "name/environment", or a domain of one of its resources.
func MatchProject(p Project, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if pn, env, ok := strings.Cut(name, "/"); ok && strings.EqualFold(pn, p.Name) && strings.EqualFold(env, p.Environment) {
		return true
	}
	if strings.EqualFold(name, p.Name) || name == p.UUID {
		return true
	}
	h := strings.ToLower(Host(name))
	for _, d := range p.Domains() {
		d = strings.ToLower(d)
		bare := strings.Split(d, "/")[0]
		if d == h || bare == h || strings.Split(bare, ":")[0] == h {
			return true
		}
	}
	return false
}
