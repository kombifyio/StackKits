// Package attackpath explains consequential attack paths over facts StackKits
// already holds and verifies that a repair broke one without breaking the
// access the owner intends (Guard B, ADR 0043 in homelab/docs/ADR).
//
// It is deterministic and bounded. It reads only a Snapshot handed to it: the
// host-security evidence, the access-gate route table, the container
// inspection and the backup credential locations. It probes nothing, scans
// nothing and exploits nothing. Every edge of the graph states whether it is
// open, closed or unknown, carries its basis (observed, configured or
// inferred), its source and its observation time, and an edge whose evidence
// is missing, stale or unreadable stays unknown: it is never read as closed
// and never completes a path. A path is reported as confirmed only when every
// edge is open, and as unresolved when exactly one edge is unknown and all
// others are open.
//
// Closure compares a report taken before a repair with one taken after it.
// A path counts as repaired only when one of its edges was observed closed by
// evidence produced after the before report and every intended access is
// still observed to work. The package never claims that a host is secure,
// free of other paths, or free of malware.
package attackpath
