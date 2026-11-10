// Package homegraph is the StackKits node side of the household home
// graph: it holds the Go types of the wire contract kombify.home_graph.v1
// (Cloud serves it at GET /api/v1/household/{space}/home), synchronizes Home
// Assistant (floors, areas, labels, device placement) with it over the Home
// Assistant WebSocket registry API, plans the adoption of an existing Home
// Assistant installation, and imports Loxone rooms and controls read-only from
// the Miniserver structure file.
//
// Target: HOME-GRAPH-TARGET.md in the kombify workspace, sections 3 to 8.
// Owner decision: D-2026-11-09-03.
//
// Home Assistant access tokens and Loxone credentials stay on the node. No
// type in this package serializes one, no error carries one and nothing logs
// one. kombify deletes nothing in Home Assistant: plans contain no delete
// operation, and removal of any object needs the person's confirmation outside
// this package. Nothing here writes to a Loxone Miniserver.
package homegraph
