package attackpath

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/accessgatetable"
	"github.com/kombifyio/stackkits/internal/hostsecurity"
)

// graph is the edge set and the section accounting one Evaluate builds.
type graph struct {
	now       time.Time
	edges     []Edge
	sections  []SectionReport
	services  []ServiceState
	unknown   []string
	truncated bool
}

func safe(name string) string {
	return strings.NewReplacer(">", "_", "\n", "_", "\r", "_").Replace(name)
}

func containerNode(name string) string { return containerNodePrefix + safe(name) }
func routeNode(id string) string       { return routeNodePrefix + safe(id) }
func backupNode(ref string) string     { return backupNodePrefix + safe(ref) }

func (g *graph) add(edge Edge) { g.edges = append(g.edges, edge) }

func (g *graph) section(name string, p Provenance, version string, usable bool, note string) {
	report := SectionReport{Name: name, Source: p.Source, Version: version, ObservedAt: p.ObservedAt, Usable: usable, Note: note}
	if usable {
		report.AgeSeconds = int64(g.now.Sub(p.ObservedAt).Seconds())
	} else {
		g.unknown = append(g.unknown, "section:"+name)
	}
	g.sections = append(g.sections, report)
}

func edgeFor(from, to, mechanism string, state EdgeState, basis Basis, source string, observed time.Time, detail string) Edge {
	edge := newEdge(from, to, mechanism)
	edge.State, edge.Basis, edge.Source, edge.ObservedAt, edge.Detail = state, basis, source, observed, detail
	return edge
}

func openOrClosed(open bool) EdgeState {
	if open {
		return EdgeOpen
	}
	return EdgeClosed
}

// Evaluate builds the attack-path graph and explains the consequential paths
// in it. It is a pure function of the snapshot and the clock.
func Evaluate(s Snapshot, now time.Time) Report {
	g := &graph{now: now}
	g.hostAccess(s.Evidence)
	g.routesAndServices(s.Routes, s.Containers)
	g.containers(s.Containers, s.Backups)
	g.backupReach(s.Backups)

	sort.Slice(g.edges, func(i, j int) bool { return g.edges[i].ID < g.edges[j].ID })
	for _, edge := range g.edges {
		if edge.State == EdgeUnknown {
			g.unknown = append(g.unknown, edge.ID)
		}
	}
	sort.Strings(g.unknown)
	g.unknown = slices.Compact(g.unknown)
	sort.Slice(g.services, func(i, j int) bool { return g.services[i].Route < g.services[j].Route })

	paths, cut := findPaths(g.edges)
	return Report{
		SchemaVersion: ReportSchemaVersion,
		Host:          s.Host,
		EvaluatedAt:   now.UTC(),
		Sections:      g.sections,
		Edges:         g.edges,
		Services:      g.services,
		Paths:         paths,
		Unknowns:      g.unknown,
		Truncated:     g.truncated || cut,
		Limits:        slices.Clone(reportLimits),
	}
}

// hostAccess derives the SSH edges from the host-security controls. A control
// that is missing, stale or unreadable leaves its edge unknown.
func (g *graph) hostAccess(evidence *hostsecurity.Evidence) {
	const source = "hostsecurity"
	root := edgeFor(NodeHostUser, NodeHostRoot, MechanismPrivilegeGain, EdgeUnknown, "", "none", time.Time{},
		"no source reads sudo or local privilege rules, so this step is not evaluated")
	password := newEdge(NodeNetwork, NodeHostUser, MechanismSSHPassword)
	rootLogin := newEdge(NodeNetwork, NodeHostRoot, MechanismSSHRootLogin)
	password.Source, rootLogin.Source = source, source
	password.Detail = "no fresh host-security evidence"
	rootLogin.Detail = password.Detail
	g.add(root)

	if evidence == nil {
		g.section("host-security", Provenance{}, "", false, "no host-security evidence")
		g.add(password)
		g.add(rootLogin)
		return
	}
	provenance := Provenance{Source: source + " " + evidence.SchemaVersion, ObservedAt: evidence.ObservedAt, ExpiresAt: evidence.ExpiresAt}
	usable := evidence.SchemaVersion == hostsecurity.EvidenceSchemaVersion && provenance.usable(g.now)
	note := ""
	if !usable {
		note = "evidence is missing, from another schema, stale or from the future"
	}
	g.section("host-security", provenance, evidence.BaselineVersion, usable, note)
	if !usable {
		g.add(password)
		g.add(rootLogin)
		return
	}
	pw := controlEdgeState(*evidence, hostsecurity.ControlSSHPassword)
	rt := controlEdgeState(*evidence, hostsecurity.ControlSSHRootLogin)
	password.State, password.Basis, password.ObservedAt = pw.state, BasisObserved, pw.observed
	password.Detail = "sshd accepts a non-key login: " + stateWord(pw.state)
	rootLogin.Basis, rootLogin.ObservedAt = BasisObserved, newer(pw.observed, rt.observed)
	switch {
	case pw.state == EdgeClosed || rt.state == EdgeClosed:
		rootLogin.State = EdgeClosed
	case pw.state == EdgeOpen && rt.state == EdgeOpen:
		rootLogin.State = EdgeOpen
	default:
		rootLogin.State = EdgeUnknown
	}
	rootLogin.Detail = "root login and password login together: " + stateWord(rootLogin.State)
	g.add(password)
	g.add(rootLogin)
}

type controlReading struct {
	state    EdgeState
	observed time.Time
}

// controlEdgeState reads a control as drift means open: drifted and
// exception (an approved deviation is still a deviation) are open, compliant
// is closed, anything else, including a missing control, is unknown.
func controlEdgeState(evidence hostsecurity.Evidence, id string) controlReading {
	index := slices.IndexFunc(evidence.Controls, func(control hostsecurity.Control) bool { return control.ID == id })
	if index < 0 {
		return controlReading{state: EdgeUnknown}
	}
	control := evidence.Controls[index]
	observed := control.ObservedAt
	if observed.IsZero() {
		observed = evidence.ObservedAt
	}
	switch control.State {
	case hostsecurity.StateDrifted, hostsecurity.StateException:
		return controlReading{EdgeOpen, observed}
	case hostsecurity.StateCompliant:
		return controlReading{EdgeClosed, observed}
	}
	return controlReading{EdgeUnknown, observed}
}

func stateWord(state EdgeState) string {
	switch state {
	case EdgeOpen:
		return "present"
	case EdgeClosed:
		return "absent"
	}
	return "unknown"
}

func newer(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// routesAndServices derives the public-route entry edges, the route backends
// and the intended-access state of every route.
func (g *graph) routesAndServices(routes *RouteFacts, containers *ContainerFacts) {
	if routes == nil {
		g.section("routes", Provenance{}, "", false, "no route table")
		return
	}
	usable := routes.usable(g.now)
	note := ""
	if !usable {
		note = "route table is missing, stale or from the future"
	}
	g.section("routes", routes.Provenance, routes.Table.Schema, usable, note)
	if !usable {
		return
	}
	table := routes.Table.Routes
	if len(table) > MaxRoutes {
		table = table[:MaxRoutes]
		g.truncated = true
	}
	source := routes.Source
	running, listed := containerIndex(containers, g.now)
	for _, route := range table {
		node := routeNode(route.ID)
		entry := route.Exposure == accessgatetable.ExposurePublic && route.Authentication == accessgatetable.AuthenticationNone
		detail := fmt.Sprintf("exposure %s, authentication %s", route.Exposure, route.Authentication)
		g.add(edgeFor(NodeNetwork, node, MechanismPublicRoute, openOrClosed(entry), BasisConfigured, source, routes.ObservedAt, detail))

		service := ServiceState{Route: route.ID, State: EdgeUnknown, Detail: "no backend recorded for this route"}
		backend, mapped := routes.Backends[route.ID]
		if mapped && backend != "" {
			service.Container = backend
			if entry {
				g.add(edgeFor(node, containerNode(backend), MechanismRouteBackend, EdgeOpen, BasisConfigured, source, routes.ObservedAt, "backend of the route"))
			}
			switch {
			case !listed:
				service.Detail = "no fresh container inspection"
			case running[backend]:
				service.State, service.Detail = EdgeOpen, "route defined and backend running"
			case containers.complete(backend):
				service.State, service.Detail = EdgeClosed, "backend container is missing or not running"
			default:
				service.Detail = "container list was cut, backend not found"
			}
		} else if entry {
			g.add(edgeFor(node, "container:unknown", MechanismRouteBackend, EdgeUnknown, "", source, routes.ObservedAt, "no backend recorded for this public route"))
		}
		g.services = append(g.services, service)
	}
}

// containerIndex maps running container names and reports whether a fresh
// inspection exists.
func containerIndex(containers *ContainerFacts, now time.Time) (running map[string]bool, fresh bool) {
	if containers == nil || !containers.usable(now) {
		return nil, false
	}
	running = map[string]bool{}
	for _, container := range containers.Containers {
		if container.Running {
			running[container.Name] = true
		}
	}
	return running, true
}

// complete reports whether the inspection lists every container, so that an
// absent name means the container does not exist.
func (c *ContainerFacts) complete(string) bool { return len(c.Containers) <= MaxContainers }

// containers derives the published-port and escape edges per container.
func (g *graph) containers(facts *ContainerFacts, backups *BackupFacts) {
	if facts == nil {
		g.section("containers", Provenance{}, "", false, "no container inspection")
		return
	}
	usable := facts.usable(g.now)
	note := ""
	if !usable {
		note = "container inspection is missing, stale or from the future"
	}
	g.section("containers", facts.Provenance, "", usable, note)
	if !usable {
		return
	}
	list := slices.Clone(facts.Containers)
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	if len(list) > MaxContainers {
		list = list[:MaxContainers]
		g.truncated = true
	}
	var credentials []BackupCredential
	if backups != nil && backups.usable(g.now) {
		credentials = backups.Credentials
	}
	for _, container := range list {
		g.containerEdges(facts, container, credentials)
	}
}

func (g *graph) containerEdges(facts *ContainerFacts, container Container, credentials []BackupCredential) {
	node := containerNode(container.Name)
	add := func(from, to, mechanism string, open bool, detail string) {
		state := openOrClosed(open && container.Running)
		if !container.Running {
			detail = "container is not running"
		}
		g.add(edgeFor(from, to, mechanism, state, BasisObserved, facts.Source, facts.ObservedAt, detail))
	}
	mounts := container.Mounts
	mountsCut := len(mounts) > MaxMounts
	if mountsCut {
		mounts = mounts[:MaxMounts]
		g.truncated = true
	}
	// A closed verdict from mounts is only as complete as the mounts read.
	mountEdge := func(from, to, mechanism string, open bool, detail string) {
		add(from, to, mechanism, open, detail)
		if !open && mountsCut && container.Running {
			last := &g.edges[len(g.edges)-1]
			last.State, last.Detail = EdgeUnknown, "mount list was cut at the bound"
		}
	}

	var hostIPs []string
	for _, port := range container.Ports {
		if beyondLoopback(port.HostIP) {
			hostIPs = append(hostIPs, fmt.Sprintf("%s/%s", port.Port, port.Transport))
		}
	}
	sort.Strings(hostIPs)
	add(NodeNetwork, node, MechanismPublishedPort, len(hostIPs) > 0, "published beyond loopback: "+strings.Join(limit(hostIPs, 8), ", "))

	socket := slices.ContainsFunc(mounts, isDockerSocket)
	mountEdge(node, NodeHostRoot, MechanismDockerSocket, socket, "container mounts the container-runtime socket")
	add(node, NodeHostRoot, MechanismPrivileged, container.Privileged, "container runs privileged")
	capability, dangerous := hasDangerousCapability(container.CapAdd)
	add(node, NodeHostRoot, MechanismCapability, dangerous, "container adds capability "+capability)
	mountEdge(node, NodeHostRoot, MechanismHostRootMount, slices.ContainsFunc(mounts, exposesHostRoot), "container writes to the host root directory")
	for _, credential := range credentials {
		reach := slices.ContainsFunc(mounts, func(mount Mount) bool { return reachesPath(mount.Source, credential.HostPath) })
		mountEdge(node, backupNode(credential.Ref), MechanismCredentialMount, reach, "container mount contains the credential file")
	}
}

func limit(values []string, n int) []string {
	if len(values) > n {
		return values[:n]
	}
	return values
}

// backupReach adds the root-reads-credential edges. Root reading a local file
// is an operating-system rule, so the basis is inferred; where credentials
// rest is configured custody.
func (g *graph) backupReach(backups *BackupFacts) {
	if backups == nil {
		g.section("backup-credentials", Provenance{}, "", false, "no backup credential record")
		g.add(unknownBackupReach())
		return
	}
	usable := backups.usable(g.now)
	note := ""
	if !usable {
		note = "backup credential record is missing, stale or from the future"
	}
	g.section("backup-credentials", backups.Provenance, "", usable, note)
	if !usable {
		g.add(unknownBackupReach())
		return
	}
	for _, credential := range backups.Credentials {
		g.add(edgeFor(NodeHostRoot, backupNode(credential.Ref), MechanismRootReadsBackup, EdgeOpen, BasisInferred, backups.Source, backups.ObservedAt,
			"root reads any local file; no file permission or access list was read"))
	}
}

func unknownBackupReach() Edge {
	return edgeFor(NodeHostRoot, backupNode("credentials"), MechanismRootReadsBackup, EdgeUnknown, "", "none", time.Time{},
		"where backup credentials rest is not known")
}
