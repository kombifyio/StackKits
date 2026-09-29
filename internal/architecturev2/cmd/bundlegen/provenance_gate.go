package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// measuredProvenanceEnforced switches the C1 provenance gate from a warning to
// a bundlegen error. It stays false until the follow-up that binds measured
// reservation/recommended values (scripts/compat/bind-resource-evidence.mjs)
// lands; that follow-up flips this one line (plan 18, owner decision 4).
const measuredProvenanceEnforced = false

// measuredResourceBlocks are the profile resource blocks a supported
// application profile must back with a measurement or an upstream citation.
// hostFloor is deliberately absent: it is the Kombify support floor and may
// stay policy. headroom is optional and not gated.
var measuredResourceBlocks = []string{"reservation", "recommended"}

// profileProvenanceFindings is the catalog gate for plan 18 decision 2 in its
// C1 form: every supported compute profile of an application module declares
// reservation and recommended, each with measured or upstream provenance.
// Core profiles are the only ones allowed to declare platformManagement, which
// identifies them; their budgets stay a support policy.
func profileProvenanceFindings(catalog []byte) ([]string, error) {
	var document struct {
		Modules []struct {
			Metadata struct {
				ID string `json:"id"`
			} `json:"metadata"`
			ComputeProfiles map[string]struct {
				Maturity           string                     `json:"maturity"`
				PlatformManagement string                     `json:"platformManagement"`
				Provenance         map[string]json.RawMessage `json:"provenance"`
			} `json:"computeProfiles"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(catalog, &document); err != nil {
		return nil, fmt.Errorf("read catalog for provenance gate: %w", err)
	}
	var findings []string
	for _, module := range document.Modules {
		profileIDs := make([]string, 0, len(module.ComputeProfiles))
		for profileID := range module.ComputeProfiles {
			profileIDs = append(profileIDs, profileID)
		}
		sort.Strings(profileIDs)
		for _, profileID := range profileIDs {
			profile := module.ComputeProfiles[profileID]
			if profile.Maturity != "supported" || profile.PlatformManagement != "" {
				continue
			}
			var missing, unmeasured []string
			for _, block := range measuredResourceBlocks {
				raw, declared := profile.Provenance[block]
				if !declared {
					missing = append(missing, block)
					continue
				}
				var provenance struct {
					Source string `json:"source"`
				}
				if err := json.Unmarshal(raw, &provenance); err != nil {
					return nil, fmt.Errorf("read %s.computeProfiles.%s.provenance.%s: %w", module.Metadata.ID, profileID, block, err)
				}
				if provenance.Source != "measured" && provenance.Source != "upstream" {
					unmeasured = append(unmeasured, block)
				}
			}
			var reasons []string
			if len(missing) > 0 {
				reasons = append(reasons, fmt.Sprintf("declares no %v", missing))
			}
			if len(unmeasured) > 0 {
				reasons = append(reasons, fmt.Sprintf("rests %v on policy", unmeasured))
			}
			if len(reasons) > 0 {
				findings = append(findings, fmt.Sprintf("%s.computeProfiles.%s is supported but %s; bind measured values (mise run compat:resource-proposals) or cite upstream", module.Metadata.ID, profileID, strings.Join(reasons, " and ")))
			}
		}
	}
	return findings, nil
}

// applyProvenanceGate reports the findings as warnings, or fails the bundle
// when the gate is enforced.
func applyProvenanceGate(findings []string, enforced bool, stderr io.Writer) error {
	if len(findings) == 0 {
		return nil
	}
	if enforced {
		return errors.New("provenance gate: " + strings.Join(findings, "; "))
	}
	for _, finding := range findings {
		_, _ = fmt.Fprintln(stderr, "bundlegen: WARNING provenance:", finding)
	}
	return nil
}
