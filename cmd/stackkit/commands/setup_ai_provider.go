package commands

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/spf13/cobra"
)

type aiProviderOptions struct {
	baseURL, tenantRef, walletItem string
	disable, outputJSON            bool
}

// aiProviderResult is the secret-free outcome of `stackkit setup ai-provider`.
type aiProviderResult struct {
	WorkloadRef string   `json:"workloadRef"`
	Provider    string   `json:"provider"`
	BaseURL     string   `json:"baseUrl,omitempty"`
	ItemRef     string   `json:"itemRef,omitempty"`
	SpecHash    string   `json:"specHash"`
	NextSteps   []string `json:"nextSteps"`
}

func newSetupAIProviderCommand() *cobra.Command {
	options := &aiProviderOptions{}
	command := &cobra.Command{
		Use:         "ai-provider",
		Short:       "Offer an external OpenAI-compatible provider in Private AI with a key from your kombify Wallet",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		Long: `Let Open WebUI of the Private AI use case use an external OpenAI-compatible
inference provider (for example OpenAI, Mistral, Groq or OpenRouter) next to
the node's Ollama. The provider's API key never passes through this command,
the StackSpec or the generated bundle: keep it as an AI credential in your
kombify Wallet, allow this node on the item, and deliver it sealed to this node
with "stackkit wallet-item import". This command binds the provider key to that
delivered item and sets the provider's https base URL. Every apply then reads
the newest delivered version, so a rotation in the Wallet takes effect after
"stackkit wallet-item import" of the rotated item and the next apply. Apply
fails closed when the item is not in this node's custody.

--disable removes the provider from Open WebUI and the binding from local
custody. The delivered item stays in the node's Wallet item custody.`,
		Example: `  # Bind the provider key delivered as Wallet item itm_... and set the provider
  stackkit setup ai-provider --base-url https://api.openai.com/v1 --tenant-ref "$TENANT" --wallet-item itm_abc...

  # Remove the provider
  stackkit setup ai-provider --disable`,
		RunE: func(cmd *cobra.Command, _ []string) error { return runSetupAIProvider(cmd, *options) },
	}
	command.Flags().StringVar(&options.baseURL, "base-url", "", "https base URL of the OpenAI-compatible provider API")
	command.Flags().StringVar(&options.tenantRef, "tenant-ref", "", "Tenant the Wallet item was delivered under")
	command.Flags().StringVar(&options.walletItem, "wallet-item", "", "Wallet item reference of the delivered provider key")
	command.Flags().BoolVar(&options.disable, "disable", false, "Remove the external provider and its key binding")
	command.Flags().BoolVar(&options.outputJSON, "json", false, "Emit the secret-free stackkit.command-result/v1 result")
	return command
}

func runSetupAIProvider(cmd *cobra.Command, options aiProviderOptions) error {
	var (
		result aiProviderResult
		err    error
	)
	if options.disable {
		if options.baseURL != "" || options.tenantRef != "" || options.walletItem != "" {
			return machineAwareCommandError(cmd, errors.New("--disable takes no provider, tenant or item"))
		}
		result, err = disableAIProvider(getWorkDir())
	} else {
		result, err = enableAIProvider(getWorkDir(), options)
	}
	if err != nil {
		return machineAwareCommandError(cmd, err)
	}
	if options.outputJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), result)
	}
	message := fmt.Sprintf("ai: Open WebUI offers %s with the key from Wallet item %s. Run stackkit generate and stackkit apply to use it.", result.BaseURL, result.ItemRef)
	if result.Provider == "disabled" {
		message = "ai: the external provider is removed and its key binding is gone from local custody. Run stackkit generate and stackkit apply to stop using it."
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\nSpec: %s\n", message, result.SpecHash)
	return err
}

// enableAIProvider checks every input and that the item is in this node's
// custody before it touches the StackSpec, so the setting never names an
// absent key.
func enableAIProvider(workspace string, options aiProviderOptions) (aiProviderResult, error) {
	baseURL, err := architecturev2renderer.NormalizePrivateAIExternalProviderURL(options.baseURL)
	if err != nil {
		return aiProviderResult{}, err
	}
	if options.tenantRef == "" || options.walletItem == "" {
		return aiProviderResult{}, errors.New("ai-provider requires --base-url, --tenant-ref and --wallet-item")
	}
	service, current, err := loadAIConnectorSpec(workspace)
	if err != nil {
		return aiProviderResult{}, err
	}
	if !current.aiSelected {
		return aiProviderResult{}, errors.New("the external provider needs the Private AI use case with Open WebUI (workload ai, alternative private-ai) in this StackSpec")
	}
	if err := localevidence.BindLocalWalletSecret(workspace, architecturev2renderer.PrivateAIExternalProviderKeyRef, options.tenantRef, options.walletItem); err != nil {
		return aiProviderResult{}, fmt.Errorf("bind the provider key to the Wallet item: %w", err)
	}
	specHash, err := persistAIProviderSetting(workspace, service, current, baseURL)
	if err != nil {
		return aiProviderResult{}, err
	}
	return aiProviderResult{
		WorkloadRef: "ai", Provider: "enabled", BaseURL: baseURL, ItemRef: options.walletItem,
		SpecHash: specHash, NextSteps: []string{"stackkit generate", "stackkit apply"},
	}, nil
}

// disableAIProvider removes the setting first, so no later apply looks for the
// key, and then the binding.
func disableAIProvider(workspace string) (aiProviderResult, error) {
	service, current, err := loadAIConnectorSpec(workspace)
	if err != nil {
		return aiProviderResult{}, err
	}
	specHash := current.specHash
	if current.aiSelected {
		if specHash, err = persistAIProviderSetting(workspace, service, current, ""); err != nil {
			return aiProviderResult{}, err
		}
	}
	if err := localevidence.RemoveLocalIssuedSecret(workspace, architecturev2renderer.PrivateAIExternalProviderKeyRef); err != nil {
		return aiProviderResult{}, fmt.Errorf("remove the provider key binding from local custody: %w", err)
	}
	return aiProviderResult{
		WorkloadRef: "ai", Provider: "disabled", SpecHash: specHash, NextSteps: []string{"stackkit generate", "stackkit apply"},
	}, nil
}

// persistAIProviderSetting writes or removes the external-provider setting of
// the ai workload through the compare-and-swap StackSpec store.
func persistAIProviderSetting(workspace string, service *architecturev2.Service, current aiConnectorSpec, baseURL string) (string, error) {
	spec, err := decodeAIConnectorSpec(current.canonical)
	if err != nil {
		return "", err
	}
	ai := spec["workloads"].(map[string]any)["ai"].(map[string]any)
	settings, _ := ai["settings"].(map[string]any)
	if settings == nil {
		settings = map[string]any{}
	}
	if baseURL != "" {
		settings[architecturev2renderer.PrivateAIExternalProviderSetting] = baseURL
	} else {
		if _, set := settings[architecturev2renderer.PrivateAIExternalProviderSetting]; !set {
			return current.specHash, nil
		}
		delete(settings, architecturev2renderer.PrivateAIExternalProviderSetting)
	}
	if len(settings) == 0 {
		delete(ai, "settings")
	} else {
		ai["settings"] = settings
	}
	candidate, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	result, err := stackspecintent.Persist(stackspecintent.Request{
		WorkspaceRoot: workspace, SpecPath: current.path, Candidate: candidate,
		ExpectedSpecHash: current.specHash, BuildVersion: version, Authority: service,
	})
	if err != nil {
		return "", fmt.Errorf("update the StackSpec: %w", err)
	}
	return result.SpecHash, nil
}
