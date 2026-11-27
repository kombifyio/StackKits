package attackpath

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
)

func isConsequence(node string) bool {
	return node == NodeHostRoot || strings.HasPrefix(node, backupNodePrefix)
}

// basisRank orders bases from weakest to strongest. A path is only as strong
// as its weakest edge.
func basisRank(basis Basis) int {
	switch basis {
	case BasisObserved:
		return 3
	case BasisConfigured:
		return 2
	case BasisInferred:
		return 1
	}
	return 0
}

// PathID is the stable identity of a chain of edges. It does not depend on
// edge state, so the same path keeps its identity before and after a repair.
func PathID(edgeIDs []string) string {
	sum := sha256.Sum256([]byte(strings.Join(edgeIDs, "\n")))
	return "ap-" + hex.EncodeToString(sum[:6])
}

// findPaths searches simple chains from the network to a consequence over the
// edges that are not closed. It is exhaustive within MaxDepth, ordered
// deterministically and cut at MaxPaths.
func findPaths(edges []Edge) (paths []Path, truncated bool) {
	outgoing := map[string][]Edge{}
	for _, edge := range edges {
		if edge.State != EdgeClosed {
			outgoing[edge.From] = append(outgoing[edge.From], edge)
		}
	}
	for _, list := range outgoing {
		sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	}

	var walk func(node string, chain []Edge, visited map[string]bool, unknown int)
	walk = func(node string, chain []Edge, visited map[string]bool, unknown int) {
		if len(chain) >= MaxDepth {
			return
		}
		for _, edge := range outgoing[node] {
			if visited[edge.To] {
				continue
			}
			count := unknown
			if edge.State == EdgeUnknown {
				count++
			}
			if count > 1 {
				continue // two unknown steps assert nothing; they stay in Unknowns
			}
			next := append(slices.Clone(chain), edge)
			if isConsequence(edge.To) {
				paths = append(paths, describe(next, count))
			}
			visited[edge.To] = true
			walk(edge.To, next, visited, count)
			delete(visited, edge.To)
		}
	}
	walk(NodeNetwork, nil, map[string]bool{NodeNetwork: true}, 0)

	sort.Slice(paths, func(i, j int) bool {
		a, b := paths[i], paths[j]
		if a.Status != b.Status {
			return a.Status == PathConfirmed
		}
		if len(a.EdgeIDs) != len(b.EdgeIDs) {
			return len(a.EdgeIDs) < len(b.EdgeIDs)
		}
		return a.ID < b.ID
	})
	if len(paths) > MaxPaths {
		paths, truncated = paths[:MaxPaths], true
	}
	return paths, truncated
}

func describe(chain []Edge, unknown int) Path {
	ids := make([]string, len(chain))
	for i, edge := range chain {
		ids[i] = edge.ID
	}
	path := Path{ID: PathID(ids), Status: PathConfirmed, Target: chain[len(chain)-1].To, EdgeIDs: ids}
	weakest := BasisObserved
	for _, edge := range chain {
		if edge.State == EdgeUnknown {
			path.Status, path.UnknownEdge = PathUnresolved, edge.ID
			continue
		}
		if basisRank(edge.Basis) < basisRank(weakest) {
			weakest = edge.Basis
		}
	}
	path.Basis = weakest
	headline := fmt.Sprintf("%s can reach %s in %d steps", NodeNetwork, path.Target, len(chain))
	if unknown > 0 {
		headline += "; one step is not evaluated, so this is a question, not a finding"
	}
	path.Explanation = append(path.Explanation, headline)
	for i, edge := range chain {
		path.Explanation = append(path.Explanation, fmt.Sprintf("%d. %s", i+1, edge.explain()))
	}
	return path
}
