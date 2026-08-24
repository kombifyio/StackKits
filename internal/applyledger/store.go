package applyledger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// FileName is the per-unit account stored beside a rollout run's events and
// summary, so the truth about a rollout survives the process that produced it.
const FileName = "outcomes.json"

// Store writes a ledger beside the current rollout run's other durable
// evidence. Keeping the path and encoding here gives every producer and reader
// one storage contract.
func Store(runRoot string, ledger Ledger) (string, error) {
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(runRoot, FileName)
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// Latest returns the most recent per-unit account in a workspace, or nil when
// no run recorded one.
//
// It lives here rather than in a caller because more than one surface has to
// answer the same question -- what did the last Apply actually do -- and two
// readers would eventually disagree about where the answer is kept.
func Latest(workspace string) *Ledger {
	entries, err := os.ReadDir(filepath.Join(workspace, ".stackkit", "runs"))
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	// Run IDs lead with a UTC timestamp, so lexical order is chronological.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		data, readErr := os.ReadFile(filepath.Join(workspace, ".stackkit", "runs", name, FileName))
		if readErr != nil {
			continue
		}
		var ledger Ledger
		if json.Unmarshal(data, &ledger) != nil || ledger.SchemaVersion != SchemaVersion {
			continue
		}
		return &ledger
	}
	return nil
}
