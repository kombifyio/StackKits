package commands

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// materializeAdvancedCandidateSecrets creates the owner-signed local custody
// for every governed secret ref (secret://...) the Advanced candidate
// StackSpec declares, before the target generate and apply read them. A
// workload added by a change set (Vault's admin-token, Photos' database
// password) otherwise has no custody: only init and `stackkit secrets
// materialize` created it, and neither runs on a Techstack-managed host.
// Existing custody is reused, never regenerated. The candidate may be JSON
// (Techstack) or YAML (CLI); yaml.v3 decodes both. It returns the ordered refs
// so the result can name them without values.
func materializeAdvancedCandidateSecrets(workspace string, candidate []byte) ([]string, error) {
	var spec struct {
		Workloads map[string]struct {
			SecretRefs map[string]string `yaml:"secretRefs"`
		} `yaml:"workloads"`
	}
	if err := yaml.Unmarshal(candidate, &spec); err != nil {
		return nil, fmt.Errorf("decode Advanced candidate secret authority: %w", err)
	}
	refs := map[string]struct{}{}
	for _, workload := range spec.Workloads {
		for _, ref := range workload.SecretRefs {
			if trimmed := strings.TrimSpace(ref); strings.HasPrefix(trimmed, "secret://") {
				refs[trimmed] = struct{}{}
			}
		}
	}
	ordered := make([]string, 0, len(refs))
	for ref := range refs {
		ordered = append(ordered, ref)
	}
	sort.Strings(ordered)
	for _, ref := range ordered {
		if err := localevidence.MaterializeLocalSecret(workspace, ref); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}
