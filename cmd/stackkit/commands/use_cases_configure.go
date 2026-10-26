package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/spf13/cobra"
)

type intentChangeOptions struct {
	expectedHash, output string
	settings, secretRefs []string
	json                 bool
}

type useCaseChangeOptions struct {
	intentChangeOptions
	alternative, platform, node, compute, storage, accelerator string
}

func bindIntentChangeFlags(cmd *cobra.Command, o *intentChangeOptions) {
	cmd.Flags().StringVar(&o.expectedHash, "expected-spec-hash", "", "Require the current canonical spec hash")
	cmd.Flags().StringVar(&o.output, "output", "", "Write a new candidate file, retaining the active baseline; required for Advanced/multi-node intent")
	cmd.Flags().StringArrayVar(&o.settings, "setting", nil, "Public setting key=JSON; repeat to merge named settings")
	cmd.Flags().StringArrayVar(&o.secretRefs, "secret-ref", nil, "Input key=secret://reference; never supply secret material")
	cmd.Flags().BoolVar(&o.json, "json", false, "Emit versioned authoring result; execution remains pending")
}

func bindUseCaseChangeFlags(cmd *cobra.Command, o *useCaseChangeOptions) {
	bindIntentChangeFlags(cmd, &o.intentChangeOptions)
	cmd.Flags().StringVar(&o.alternative, "alternative", "", "Catalog alternative for a new use case; existing alternatives require migration")
	cmd.Flags().StringVar(&o.platform, "platform", "", "Runtime adapter for a new use case; existing delivery identity is retained")
	cmd.Flags().StringVar(&o.node, "node", "", "Existing enabled node; required for additions when multiple nodes exist")
	cmd.Flags().StringVar(&o.compute, "compute-profile", "", "Module-local low, standard or high; omission retains selection")
	cmd.Flags().StringVar(&o.storage, "storage-profile", "", "Catalog module storage profile; omission retains selection")
	cmd.Flags().StringVar(&o.accelerator, "accelerator-profile", "", "Catalog module accelerator profile; omission retains selection")
}

func newUseCasesConfigureCommand() *cobra.Command {
	o := &useCaseChangeOptions{}
	cmd := &cobra.Command{
		Use: "configure <slug>", Aliases: []string{"update"}, Short: "Update one selected application's settings and module profiles",
		Long: `Merge named public settings, secret references and module profiles into one
selected application. Omitted settings, data, routes, placement, adapter and
unrelated applications are retained. Application swaps and relocation require
explicit migration. Use normal generate/plan/apply, or --output and Advanced
change-set dispatch. This command authors intent only.`,
		Args: cobra.ExactArgs(1), Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error { return runUseCaseChange(cmd, args[0], *o, false) },
	}
	bindUseCaseChangeFlags(cmd, o)
	return cmd
}

func newCapabilitiesConfigureCommand() *cobra.Command {
	var enable bool
	var provider string
	o := &intentChangeOptions{}
	root := &cobra.Command{Use: "capabilities", Short: "Supplement the retained Foundation through its capability contracts"}
	cmd := &cobra.Command{
		Use: "configure <id>", Short: "Enable or configure one declared Foundation capability",
		Long: `Merge public settings, a provider and secret references into one capability.
--enable explicitly adds an optional capability. Review the normal plan before
apply. Advanced intent requires --output and capability-gated change-set dispatch.
No runtime change occurs; existing applications and storage remain selected.`,
		Args: cobra.ExactArgs(1), Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, refs, err := parseIntentChangeInputs(*o)
			if err != nil {
				return err
			}
			return authorRetainedIntent(cmd, *o, func(service *architecturev2.Service, raw []byte) (architecturev2.StackSpecValidation, error) {
				return service.MaterializeCapabilityChange(raw, args[0], architecturev2.CapabilityChange{Enable: enable, ProviderRef: provider, Settings: settings, SecretRefs: refs})
			}, "", refs)
		},
	}
	bindIntentChangeFlags(cmd, o)
	cmd.Flags().BoolVar(&enable, "enable", false, "Explicitly enable this kit-declared capability")
	cmd.Flags().StringVar(&provider, "provider", "", "Explicit CUE provider; omission retains selection")
	root.AddCommand(cmd)
	return root
}

func parseIntentChangeInputs(o intentChangeOptions) (map[string]any, map[string]string, error) {
	settings, refs := map[string]any{}, map[string]string{}
	for _, raw := range o.settings {
		key, value, found := strings.Cut(raw, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return nil, nil, errors.New("--setting requires key=JSON")
		}
		if _, exists := settings[key]; exists {
			return nil, nil, fmt.Errorf("setting %q is specified twice", key)
		}
		var decoded any
		if err := json.Unmarshal([]byte(value), &decoded); err != nil {
			return nil, nil, fmt.Errorf("setting %q requires a JSON value: %w", key, err)
		}
		settings[key] = decoded
	}
	for _, raw := range o.secretRefs {
		key, value, found := strings.Cut(raw, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" || !strings.HasPrefix(value, "secret://") {
			return nil, nil, errors.New("--secret-ref requires key=secret://reference")
		}
		if _, exists := refs[key]; exists {
			return nil, nil, fmt.Errorf("secret reference %q is specified twice", key)
		}
		refs[key] = value
	}
	return settings, refs, nil
}

func runUseCaseChange(cmd *cobra.Command, slug string, o useCaseChangeOptions, add bool) error {
	settings, refs, err := parseIntentChangeInputs(o.intentChangeOptions)
	if err != nil {
		return err
	}
	change := architecturev2.UseCaseChange{Alternative: o.alternative, RuntimeAdapterRef: o.platform, NodeRef: o.node,
		Profiles: architecturev2.ModuleProfileOverride{ComputeProfile: o.compute, StorageProfile: o.storage, AcceleratorProfile: o.accelerator}, Settings: settings, SecretRefs: refs}
	return authorRetainedIntent(cmd, o.intentChangeOptions, func(service *architecturev2.Service, raw []byte) (architecturev2.StackSpecValidation, error) {
		return service.MaterializeUseCaseChange(raw, slug, change, add)
	}, strings.TrimSpace(slug), refs)
}

func authorRetainedIntent(cmd *cobra.Command, o intentChangeOptions, author func(*architecturev2.Service, []byte) (architecturev2.StackSpecValidation, error), workload string, suppliedRefs map[string]string) error {
	wd := getWorkDir()
	specPath, _, _, err := config.NewLoader(wd).ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return err
	}
	current, err := service.ValidateStackSpec(raw)
	if err != nil {
		return err
	}
	if o.expectedHash != "" && o.expectedHash != current.SpecHash {
		return fmt.Errorf("expected spec hash does not match current intent; current spec hash: %s", current.SpecHash)
	}
	var document struct {
		Install struct {
			Mode string `json:"mode"`
		} `json:"install"`
		Nodes []json.RawMessage `json:"nodes"`
		Sites []json.RawMessage `json:"sites"`
	}
	if err := json.Unmarshal(current.CanonicalStackSpec, &document); err != nil {
		return err
	}
	if o.output == "" && (document.Install.Mode == "advanced" || len(document.Nodes) != 1 || len(document.Sites) != 1) {
		return errors.New("managed or multi-node intent requires --output <candidate.json>; retain the baseline and execute through Techstack Advanced dispatch")
	}
	candidate, err := author(service, current.CanonicalStackSpec)
	if err != nil {
		return err
	}
	path, outcome := specPath, "candidate"
	if o.output != "" {
		path = o.output
		if !filepath.IsAbs(path) {
			path = filepath.Join(wd, path)
		}
		active, err := filepath.Abs(specPath)
		if err != nil {
			return err
		}
		output, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if strings.EqualFold(active, output) {
			return errors.New("--output must not replace the active baseline StackSpec")
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("create new candidate: %w", err)
		}
		_, writeErr := file.Write(append(candidate.CanonicalStackSpec, '\n'))
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return err
		}
	} else {
		if _, err := localevidence.LoadOwnerCustody(wd); err != nil {
			return fmt.Errorf("existing local Owner custody is required: %w", err)
		}
		if workload != "" {
			var selected struct {
				Workloads map[string]struct {
					SecretRefs map[string]string `json:"secretRefs"`
				} `json:"workloads"`
			}
			if err := json.Unmarshal(candidate.CanonicalStackSpec, &selected); err != nil {
				return err
			}
			for _, ref := range selected.Workloads[workload].SecretRefs {
				material, err := localevidence.ResolveLocalSecretMaterial(wd, ref)
				clear(material)
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("existing selected use case custody is invalid: %w", err)
				}
			}
		}
		// Supplied references identify existing material; do not fabricate
		// replacement credentials for a reconfigured application.
		for _, ref := range suppliedRefs {
			material, err := localevidence.ResolveLocalSecretMaterial(wd, ref)
			clear(material)
			if err != nil {
				return fmt.Errorf("supplied reference must already have local custody: %w", err)
			}
		}
		result, err := stackspecintent.Persist(stackspecintent.Request{WorkspaceRoot: wd, SpecPath: specPath, Candidate: candidate.CanonicalStackSpec, ExpectedSpecHash: current.SpecHash, BuildVersion: version, Authority: service})
		if err != nil {
			return err
		}
		outcome = string(result.Outcome)
		if workload != "" {
			var selected struct {
				Workloads map[string]json.RawMessage `json:"workloads"`
			}
			if err := json.Unmarshal(candidate.CanonicalStackSpec, &selected); err != nil {
				return err
			}
			one, err := json.Marshal(map[string]any{"workloads": map[string]json.RawMessage{workload: selected.Workloads[workload]}})
			if err != nil {
				return err
			}
			if _, err := materializeArchitectureV2LocalSecrets(wd, one); err != nil {
				return fmt.Errorf("intent persisted at %s; retry the same command to complete selected custody: %w", result.SpecHash, err)
			}
		}
	}
	result := map[string]any{"apiVersion": "stackkit.intent-change/v1", "previousSpecHash": current.SpecHash, "specHash": candidate.SpecHash, "path": path, "outcome": outcome, "runtimeApplied": false}
	if o.output != "" {
		result["previousCanonicalSpec"] = json.RawMessage(current.CanonicalStackSpec)
	}
	if o.json {
		return writeCommandResult(cmd, cmd.CommandPath(), result)
	}
	printSuccess("Authored intent: %s (%s)", path, outcome)
	printInfo("Spec hash: %s", candidate.SpecHash)
	if o.output != "" {
		printInfo("Candidate only; use existing managed change-set dispatch for Advanced execution")
	} else {
		printInfo("Next: stackkit generate, stackkit plan, then stackkit apply")
	}
	return nil
}
