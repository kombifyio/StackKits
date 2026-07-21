package commands

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/productkits"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

const architectureV2DomainOverride = "network.domain.base"

func runArchitectureV2Init(cmd *cobra.Command, args []string, wd string) error {
	if err := validateArchitectureV2InitFlags(cmd); err != nil {
		return err
	}
	stackkitName, prompt, err := selectArchitectureV2InitKit(args)
	if err != nil {
		return err
	}

	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return fmt.Errorf("load embedded Architecture v2 authoring authority: %w", err)
	}
	profile := stackspecmigration.KitProfile(stackkitName)
	authoring, err := service.InitialStackSpecAuthoringContract(profile)
	if err != nil {
		return fmt.Errorf("load %s authoring contract: %w", stackkitName, err)
	}

	domain := strings.TrimSpace(initDomain)
	if containsString(authoring.RequiredOverrides, architectureV2DomainOverride) && domain == "" {
		if initNonInteractive {
			return fmt.Errorf("%s requires --domain as the CUE-owned %s authoring override", stackkitName, architectureV2DomainOverride)
		}
		if prompt == nil {
			prompt = newPrompter()
		}
		domain, err = prompt.inputString("Domain (required by this StackKit)", "")
		if err != nil {
			return fmt.Errorf("domain authoring override: %w", err)
		}
		domain = strings.TrimSpace(domain)
		if domain == "" {
			return fmt.Errorf("%s requires a non-empty --domain authoring override", stackkitName)
		}
	}

	name, normalizedName := architectureV2InitName(wd)
	if normalizedName {
		printInfo("Using normalized deployment contract ID %q for workspace %q", name, filepath.Base(filepath.Clean(wd)))
	}
	validation, err := service.MaterializeInitialStackSpec(profile, architecturev2.AuthoringOverrides{
		Name:       name,
		DomainBase: domain,
	})
	if err != nil {
		return fmt.Errorf("materialize %s initial StackSpec from CUE authority: %w", stackkitName, err)
	}

	loader := config.NewLoader(wd)
	specPath, displayPath, _, err := loader.ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return err
	}
	result, err := stackspecintent.Persist(stackspecintent.Request{
		WorkspaceRoot:    wd,
		SpecPath:         specPath,
		Candidate:        validation.CanonicalStackSpec,
		ExpectedSpecHash: initExpectedSpecHash,
		BuildVersion:     version,
	})
	if err != nil {
		return fmt.Errorf("persist canonical Architecture v2 StackSpec: %w", err)
	}

	switch result.Outcome {
	case stackspecintent.OutcomeCreated:
		printSuccess("Created canonical Architecture v2 spec: %s", displayPath)
	case stackspecintent.OutcomeReplaced:
		printSuccess("Replaced canonical Architecture v2 spec by expected hash: %s", displayPath)
	case stackspecintent.OutcomeAlreadyApplied:
		printSuccess("Canonical Architecture v2 spec is already current: %s", displayPath)
	}
	printInfo("StackKit: %s", stackkitName)
	printInfo("Spec hash: %s", result.SpecHash)
	if authoring.Status == "preview" {
		printWarning("%s native Architecture v2 authoring is preview.", stackkitName)
	}
	printArchitectureV2InitSummary(displayPath)
	return nil
}

func validateArchitectureV2InitFlags(cmd *cobra.Command) error {
	unsupported := make([]string, 0, 12)
	add := func(flag string, used bool) {
		if used {
			unsupported = append(unsupported, "--"+flag)
		}
	}
	add("context", strings.TrimSpace(contextFlag) != "")
	add("compute-tier", strings.TrimSpace(initComputeTier) != "")
	add("mode", strings.TrimSpace(initMode) != "")
	add("admin-email", strings.TrimSpace(initAdminEmail) != "")
	add("service-profile", strings.TrimSpace(initServiceProfile) != "")
	add("local-dns", initLocalDNS)
	add("local-name", strings.TrimSpace(initLocalName) != "")
	add("cluster-mode", initClusterMode != "" && (initClusterMode != "first" || commandFlagChanged(cmd, "cluster-mode")))
	add("owner-bootstrap-mode", strings.TrimSpace(initOwnerBootstrapMode) != "")
	add("owner-source", strings.TrimSpace(initOwnerSource) != "")
	add("owner-email", strings.TrimSpace(initOwnerEmail) != "")
	add("owner-username", strings.TrimSpace(initOwnerUsername) != "")
	add("owner-display-name", strings.TrimSpace(initOwnerDisplayName) != "")
	add("cloud-oidc-issuer", strings.TrimSpace(initCloudOIDCIssuer) != "")
	add("cloud-oidc-client-id", strings.TrimSpace(initCloudOIDCClientID) != "")
	add("cloud-oidc-client-secret-ref", strings.TrimSpace(initCloudOIDCSecretRef) != "")
	add("cloud-oidc-foreign-subject", strings.TrimSpace(initCloudOIDCForeignSubject) != "")
	add("recovery-passphrase-hash", strings.TrimSpace(initRecoveryPassphraseHash) != "")
	add("recovery-material-ref", strings.TrimSpace(initRecoveryMaterialRef) != "")
	add("output", initOutputDir != "" && (initOutputDir != "deploy" || commandFlagChanged(cmd, "output")))
	add("force", initForce)
	if len(unsupported) == 0 {
		return nil
	}
	sort.Strings(unsupported)
	return fmt.Errorf(
		"native Architecture v2 init does not accept legacy topology, host, identity, service, or output overrides: %s; the selected KitDefinition owns topology and generation output, observed host facts belong in Inventory, and identity is a separate handoff",
		strings.Join(unsupported, ", "),
	)
}

func commandFlagChanged(cmd *cobra.Command, name string) bool {
	if cmd == nil {
		return false
	}
	flag := cmd.Flags().Lookup(name)
	return flag != nil && flag.Changed
}

func selectArchitectureV2InitKit(args []string) (string, *prompter, error) {
	stackkitName := ""
	if len(args) > 0 {
		stackkitName = strings.TrimSpace(args[0])
	}
	if strings.ContainsAny(stackkitName, `/\`) {
		return "", nil, fmt.Errorf("native Architecture v2 init accepts a canonical product slug, not a local StackKit path")
	}

	var prompt *prompter
	if stackkitName == "" {
		if initNonInteractive {
			return "", nil, fmt.Errorf("stackkit name required in non-interactive mode\n\nAvailable StackKits: %v", productkits.Slugs())
		}
		prompt = newPrompter()
		choices := make([]choice, 0, len(productkits.Slugs()))
		for index, slug := range productkits.Slugs() {
			choices = append(choices, choice{Key: slug, Display: slug, IsDefault: index == 0})
		}
		selected, err := prompt.selectOne("Select a StackKit:", choices)
		if err != nil {
			return "", nil, fmt.Errorf("stackkit selection: %w", err)
		}
		stackkitName = selected
	}
	if models.IsLegacyStackKitName(stackkitName) {
		printWarning("StackKit %q is a retired alias; using %q.", stackkitName, models.NormalizeStackKitName(stackkitName))
	}
	stackkitName = models.NormalizeStackKitName(stackkitName)
	if err := productkits.Validate(stackkitName); err != nil {
		return "", nil, err
	}
	return stackkitName, prompt, nil
}

func architectureV2InitName(wd string) (string, bool) {
	if explicit := strings.TrimSpace(initName); explicit != "" {
		return explicit, false
	}
	original := filepath.Base(filepath.Clean(wd))
	lower := strings.ToLower(original)
	var normalized strings.Builder
	separatorPending := false
	for _, character := range lower {
		valid := (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')
		if !valid {
			separatorPending = normalized.Len() > 0
			continue
		}
		if separatorPending {
			normalized.WriteByte('-')
			separatorPending = false
		}
		normalized.WriteRune(character)
	}
	name := strings.Trim(normalized.String(), "-")
	if name == "" {
		name = "stack"
	}
	if name[0] < 'a' || name[0] > 'z' {
		name = "stack-" + name
	}
	return name, name != original
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func printArchitectureV2InitSummary(specPath string) {
	fmt.Println()
	printInfo("Next steps:")
	fmt.Printf("  1. Review desired intent:  %s\n", cyan("cat "+specPath))
	fmt.Printf("  2. Validate StackSpec:    %s\n", cyan("stackkit validate --spec "+specPath))
	fmt.Printf("  3. Add Inventory facts, then resolve the governed plan.\n")
	printInfo("Init makes no generation or apply-readiness claim; readiness is decided by the resolved plan.")
}
