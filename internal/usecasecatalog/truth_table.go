package usecasecatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// UseCaseTruth joins documentation facts to the catalog without promoting a
// package declaration into runtime or current-source evidence.
type UseCaseTruth struct {
	PackageLifecycle string             `json:"packageLifecycle"`
	Visibility       string             `json:"visibility"`
	Kits             []KitMembership    `json:"kits"`
	SetupActions     []AlternativeSetup `json:"setupActions"`
	AgentDeclared    bool               `json:"agentDeclared"`
	AgentSkills      []string           `json:"agentSkills"`
	ExistingSkills   []string           `json:"existingSkills"`
}

type KitMembership struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

type AlternativeSetup struct {
	WorkloadRef    string   `json:"workloadRef"`
	AlternativeRef string   `json:"alternativeRef"`
	ActionRefs     []string `json:"actionRefs"`
}

type TruthTableRow struct {
	InternalUseCase
	Truth *UseCaseTruth `json:"truth"`
}

type TruthTable struct {
	Rows         []TruthTableRow `json:"rows"`
	MainUseCases []MainUseCase   `json:"mainUseCases"`
}

// TruthTableCounts keeps accepted intents, catalog slugs and public export
// membership separate. These are authoring facts, never installation evidence.
type TruthTableCounts struct {
	SchemaVersion       string `json:"schemaVersion"`
	AcceptedMainIntents int    `json:"acceptedMainIntents"`
	CatalogSlugs        int    `json:"catalogSlugs"`
	CatalogMainIntents  int    `json:"catalogMainIntents"`
	PublicCatalogSlugs  int    `json:"publicCatalogSlugs"`
	PublicMainIntents   int    `json:"publicMainIntents"`
}

func CountTruthTable(table TruthTable) TruthTableCounts {
	counts := TruthTableCounts{SchemaVersion: "stackkits-use-case-counts/v1", AcceptedMainIntents: len(table.MainUseCases), CatalogSlugs: len(table.Rows)}
	all, public := map[string]bool{}, map[string]bool{}
	for _, row := range table.Rows {
		all[row.MainUseCase.ID] = true
		if row.Truth != nil && row.Truth.Visibility == "public" {
			counts.PublicCatalogSlugs++
			public[row.MainUseCase.ID] = true
		}
	}
	counts.CatalogMainIntents, counts.PublicMainIntents = len(all), len(public)
	return counts
}

func packageTruth(pkg map[string]any, workloads ...map[string]any) *UseCaseTruth {
	truth := &UseCaseTruth{PackageLifecycle: "missing", Visibility: "unprojected", Kits: []KitMembership{}, SetupActions: []AlternativeSetup{}, AgentSkills: []string{}, ExistingSkills: []string{}}
	if metadata, ok := pkg["metadata"].(map[string]any); ok {
		truth.PackageLifecycle = stringField(metadata, "lifecycle")
	}
	if agent, ok := pkg["agentSurface"].(map[string]any); ok {
		truth.AgentDeclared = true
		skills, _ := agent["skills"].([]any)
		for _, raw := range skills {
			skill, _ := raw.(map[string]any)
			if path := stringField(skill, "path"); path != "" {
				truth.AgentSkills = append(truth.AgentSkills, path)
			}
		}
	}
	for _, workload := range workloads {
		alternatives, _ := workload["alternatives"].([]any)
		for _, raw := range alternatives {
			alternative, _ := raw.(map[string]any)
			setup, _ := alternative["setup"].(map[string]any)
			refs, _ := setup["actionRefs"].([]any)
			actions := []string{}
			for _, ref := range refs {
				if action, ok := ref.(string); ok {
					actions = append(actions, action)
				}
			}
			if len(actions) > 0 {
				sort.Strings(actions)
				truth.SetupActions = append(truth.SetupActions, AlternativeSetup{WorkloadRef: metadataID(workload), AlternativeRef: stringField(alternative, "id"), ActionRefs: actions})
			}
		}
	}
	sort.Slice(truth.SetupActions, func(i, j int) bool {
		a, b := truth.SetupActions[i], truth.SetupActions[j]
		if a.WorkloadRef != b.WorkloadRef {
			return a.WorkloadRef < b.WorkloadRef
		}
		return a.AlternativeRef < b.AlternativeRef
	})
	sort.Strings(truth.AgentSkills)
	return truth
}

// BuildTruthTable evaluates kit membership and accepted intent titles. Counts
// follow these sources and the public export policy, never an authored number.
func GenerateTruthTable(root string) (TruthTable, error) {
	sourceSHA, err := truthSourceSHA(root)
	if err != nil {
		return TruthTable{}, err
	}
	source, err := loadSource(root, ReleaseIdentity{SourceSHA: sourceSHA})
	if err != nil {
		return TruthTable{}, err
	}
	if err := attachAuthoringVocabulary(&source, source.catalogWorkloads, source.catalogModules); err != nil {
		return TruthTable{}, err
	}
	table := TruthTable{Rows: []TruthTableRow{}, MainUseCases: []MainUseCase{}}
	for _, entry := range internalProjection(source, nil, false) {
		workloads := append([]map[string]any{source.Workloads[entry.ID]}, source.AddOnWorkloads[entry.ID]...)
		table.Rows = append(table.Rows, TruthTableRow{InternalUseCase: entry, Truth: packageTruth(source.Packages[entry.ID], workloads...)})
	}
	titles := map[string]string{}
	if err := loadCUE(root, "foundation", "#MainUseCaseTitles", &titles); err != nil {
		return TruthTable{}, err
	}
	for id, title := range titles {
		table.MainUseCases = append(table.MainUseCases, MainUseCase{ID: id, Title: title})
	}
	sort.Slice(table.MainUseCases, func(i, j int) bool { return table.MainUseCases[i].ID < table.MainUseCases[j].ID })
	var policy struct {
		SchemaVersion string   `json:"schemaVersion"`
		Exact         []string `json:"forbiddenPathExact"`
		Prefixes      []string `json:"forbiddenPathPrefixes"`
	}
	policyData, err := os.ReadFile(filepath.Join(root, "scripts", "public", "public-surface-policy.json"))
	if err != nil {
		return TruthTable{}, fmt.Errorf("read public visibility authority: %w", err)
	}
	if err := json.Unmarshal(policyData, &policy); err != nil {
		return TruthTable{}, err
	}
	if policy.SchemaVersion != "stackkits.public-surface-policy/v1" {
		return TruthTable{}, fmt.Errorf("unsupported public surface policy %q", policy.SchemaVersion)
	}
	forbidden := func(path string) bool {
		for _, exact := range policy.Exact {
			if path == exact || strings.HasPrefix(path, exact+"/") {
				return true
			}
		}
		for _, prefix := range policy.Prefixes {
			if strings.HasPrefix(path, prefix) {
				return true
			}
		}
		return false
	}
	directories, err := os.ReadDir(root)
	if err != nil {
		return TruthTable{}, err
	}
	for _, directory := range directories {
		if !directory.IsDir() || !fileExists(filepath.Join(root, directory.Name(), "stackfile.cue")) {
			continue
		}
		var kit struct {
			Metadata struct {
				Slug string `json:"slug"`
			} `json:"metadata"`
			Workloads struct{ Required, Defaults, Optional, Forbidden []string } `json:"workloads"`
		}
		if err := loadCUE(root, directory.Name(), "Definition", &kit); err != nil {
			return TruthTable{}, err
		}
		if kit.Metadata.Slug == "" {
			return TruthTable{}, fmt.Errorf("kit %s has no slug", directory.Name())
		}
		groups := []struct {
			Role string
			Refs []string
		}{{"required", kit.Workloads.Required}, {"default", kit.Workloads.Defaults}, {"optional", kit.Workloads.Optional}, {"forbidden", kit.Workloads.Forbidden}}
		for index := range table.Rows {
			if table.Rows[index].Truth == nil {
				return TruthTable{}, fmt.Errorf("use case %s lacks truth projection", table.Rows[index].ID)
			}
			for _, group := range groups {
				for _, ref := range group.Refs {
					if ref == table.Rows[index].ID {
						table.Rows[index].Truth.Kits = append(table.Rows[index].Truth.Kits, KitMembership{ID: kit.Metadata.Slug, Role: group.Role})
					}
				}
			}
		}
	}
	for index := range table.Rows {
		row := &table.Rows[index]
		row.Truth.Visibility = "public"
		if forbidden("use-cases/" + row.ID) {
			row.Truth.Visibility = "private"
		}
		sort.Slice(row.Truth.Kits, func(i, j int) bool {
			a, b := row.Truth.Kits[i], row.Truth.Kits[j]
			if a.ID != b.ID {
				return a.ID < b.ID
			}
			return a.Role < b.Role
		})
		agentRoot := filepath.Join(root, "use-cases", row.ID, "agent")
		if _, err := os.Stat(agentRoot); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return TruthTable{}, err
		}
		err := filepath.WalkDir(agentRoot, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && entry.Name() == "SKILL.md" {
				relative, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				row.Truth.ExistingSkills = append(row.Truth.ExistingSkills, filepath.ToSlash(relative))
			}
			return nil
		})
		if err != nil {
			return TruthTable{}, err
		}
		sort.Strings(row.Truth.ExistingSkills)
	}
	return table, nil
}

func truthCell(value string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ").Replace(value)
}
func truthList(values []string) string {
	if len(values) == 0 {
		return "—"
	}
	return truthCell(strings.Join(values, ", "))
}

// RenderTruthTableMarkdown is shared by every documentation projection.
func RenderTruthTableMarkdown(table TruthTable, publicOnly bool) string {
	if publicOnly {
		rows := []TruthTableRow{}
		for _, row := range table.Rows {
			if row.Truth.Visibility == "public" {
				rows = append(rows, row)
			}
		}
		table.Rows = rows
	}
	var b strings.Builder
	encoded, _ := json.Marshal(table)
	digest := sha256.Sum256(encoded)
	fmt.Fprintf(&b, "<!-- Generated by stackkit docs emit-use-case-overview; projection_digest: sha256:%s -->\n\n", hex.EncodeToString(digest[:]))
	counts := CountTruthTable(table)
	if publicOnly {
		fmt.Fprintf(&b, "CUE declares **%d accepted main intents**; the public catalog contains **%d slugs across %d represented main intents**. Counts describe catalog entries, not executable or supported installations.\n\n", counts.AcceptedMainIntents, counts.PublicCatalogSlugs, counts.PublicMainIntents)
	} else {
		fmt.Fprintf(&b, "CUE declares **%d accepted main intents**; the private catalog contains **%d slugs across %d represented main intents**; the public export contains **%d slugs across %d represented main intents**. Counts describe catalog entries, not executable or supported installations.\n\n", counts.AcceptedMainIntents, counts.CatalogSlugs, counts.CatalogMainIntents, counts.PublicCatalogSlugs, counts.PublicMainIntents)
	}
	b.WriteString("Provenance: `foundation.#MainUseCaseTitles`, evaluated `foundation.UseCaseCatalog`, `use-cases/*/Package`, evaluated kit `Definition.workloads`, `foundation.ArchitectureV2Catalog`, `scripts/public/public-surface-policy.json`, and exact-source runtime receipts. The evidence column compares actual authority input bytes with the immutable producer source; declarations never promote current runtime support.\n\n")
	represented := map[string]bool{}
	for _, row := range table.Rows {
		represented[row.MainUseCase.ID] = true
	}
	missing := []string{}
	for _, intent := range table.MainUseCases {
		if !represented[intent.ID] {
			missing = append(missing, intent.Title+" (`"+intent.ID+"`)")
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(&b, "Accepted intents without a catalog slug: %s.\n\n", strings.Join(missing, ", "))
	}
	b.WriteString("| Slug / main intent | Lifecycle | Kits / role | Primary | Alternatives | Add-ons | Setup actions by alternative | Agent surface | Evidence |\n| --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, row := range table.Rows {
		kits := []string{}
		for _, kit := range row.Truth.Kits {
			kits = append(kits, kit.ID+" ("+kit.Role+")")
		}
		primary := []string{}
		alternatives := []string{}
		for _, component := range row.Components {
			if component.Role == "primary" {
				primary = append(primary, component.Name+" (`"+component.ID+"`)")
			}
			if component.Role == "alternative" {
				alternatives = append(alternatives, component.Name+" ("+component.Realization+")")
			}
		}
		// Include admitted alternatives even when they are not a separately authored
		// component choice (for example runtime Lite variants).
		for _, alternative := range row.Alternatives {
			if alternative.ID == row.DefaultAlternative {
				continue
			}
			found := false
			for _, component := range row.Components {
				if component.Role == "alternative" && component.ID == alternative.ID {
					found = true
				}
			}
			if !found {
				alternatives = append(alternatives, alternative.Name+" (install)")
			}
		}
		addons := []string{}
		for _, addon := range row.AddOns {
			addons = append(addons, addon.ID+" (install)")
		}
		for _, component := range row.Components {
			if component.Role != "supporting" {
				continue
			}
			found := false
			for _, addon := range row.AddOns {
				for _, alternative := range addon.Alternatives {
					if alternative.ID == component.ID {
						found = true
					}
				}
			}
			if !found {
				addons = append(addons, component.Name+" (declared supporting component)")
			}
		}
		actions := []string{}
		for _, setup := range row.Truth.SetupActions {
			actions = append(actions, setup.WorkloadRef+"/"+setup.AlternativeRef+": "+strings.Join(setup.ActionRefs, ", "))
		}
		agent := "not declared"
		if row.Truth.AgentDeclared {
			agent = "declared; " + fmt.Sprintf("%d referenced skills", len(row.Truth.AgentSkills))
		}
		if len(row.Truth.ExistingSkills) > 0 {
			agent += fmt.Sprintf("; %d existing skill files", len(row.Truth.ExistingSkills))
		}
		evidence := "RUNTIME_EVIDENCE_MISSING"
		for _, gate := range row.Gates {
			if gate.ID == "runtime-evidence" {
				evidence = gate.ReasonCode
				if gate.Status == "complete" && !publicOnly {
					evidence = gate.Detail
				}
			}
		}
		slug := "`" + row.ID + "` / " + row.MainUseCase.Title
		if row.Truth.Visibility == "private" {
			slug += " (private export exclusion)"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", truthCell(slug), truthCell(row.Truth.PackageLifecycle), truthList(kits), truthList(primary), truthList(alternatives), truthList(addons), truthList(actions), truthCell(agent), truthCell(evidence))
	}
	return b.String()
}

func truthSourceSHA(root string) (string, error) {
	// Runtime proof is invalidated by executable product inputs as well as CUE facts.
	// Documentation and receipt-only commits may retain the immutable producer identity.
	paths := []string{".", ":(top,exclude)docs", ":(top,exclude).beads", ":(top,exclude)AGENTS.md", ":(top,exclude)STATUS.md", ":(top,exclude)ROADMAP.md", ":(top,exclude)README.md", ":(top,exclude)CHANGELOG.md"}
	git := func(args ...string) ([]byte, error) {
		command := exec.Command("git", args...)
		command.Dir = root
		return command.Output()
	}
	args := append([]string{"status", "--porcelain", "--"}, paths...)
	dirty, err := git(args...)
	if err != nil {
		return "", fmt.Errorf("inspect truth-table authority inputs: %w", err)
	}
	if len(strings.TrimSpace(string(dirty))) > 0 {
		return strings.Repeat("0", 40), nil
	}
	args = append([]string{"log", "-1", "--format=%H", "--"}, paths...)
	output, err := git(args...)
	if err != nil {
		return "", fmt.Errorf("identify truth-table authority source: %w", err)
	}
	sha := strings.TrimSpace(string(output))
	if len(sha) != 40 {
		return "", fmt.Errorf("missing truth-table authority source")
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "data", "use-case-runtime-evidence", "latest.json"))
	if os.IsNotExist(err) {
		return sha, nil
	}
	if err != nil {
		return "", err
	}
	var receipt RuntimeEvidenceReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return "", err
	}
	if len(receipt.SourceSHA) == 40 {
		if _, err := git("merge-base", "--is-ancestor", receipt.SourceSHA, "HEAD"); err == nil {
			args = append([]string{"diff", "--quiet", receipt.SourceSHA, "--"}, paths...)
			if _, err := git(args...); err == nil {
				return receipt.SourceSHA, nil
			}
		}
	}
	return sha, nil
}
