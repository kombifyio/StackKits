package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type aiConnectOptions struct {
	tokenFile           string
	disable, outputJSON bool
}

// aiConnectResult is the secret-free outcome of `stackkit setup ai-connect`.
type aiConnectResult struct {
	WorkloadRef string   `json:"workloadRef"`
	Preparation string   `json:"preparation"`
	SpecHash    string   `json:"specHash"`
	NextSteps   []string `json:"nextSteps"`
}

// maxAIConnectorTokenInput bounds what is read before the shape check.
const maxAIConnectorTokenInput = 16 << 10

func newSetupAIConnectCommand() *cobra.Command {
	options := &aiConnectOptions{}
	command := &cobra.Command{
		Use:         "ai-connect",
		Short:       "Connect the Private AI Ollama to your kombify AI as an own model endpoint",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		Long: `Turn on the optional kombify AI connector of the Private AI use case. Create a
model endpoint in Companion Studio (Settings, AI access, Model endpoints) with
the origin http://ollama:11434 and copy its connector token. This command reads
the token from standard input or from --token-file, never from an argument,
checks its shape, keeps it in local owner custody and turns the connector on.
The next generate and apply start the outbound connector next to Ollama.
Nothing inbound is opened and Ollama stays unpublished.

--disable turns the connector off and removes the token from local custody.
Revoke the endpoint in Companion Studio to make the token useless everywhere.`,
		Example: `  # Paste the connector token, then press Enter and Ctrl-D (or read it hidden on a terminal)
  stackkit setup ai-connect --token-file -

  # Read the token from a private file
  stackkit setup ai-connect --token-file ~/kombify-connector-token

  # Turn the connector off and remove its token
  stackkit setup ai-connect --disable`,
		RunE: func(cmd *cobra.Command, _ []string) error { return runSetupAIConnect(cmd, *options) },
	}
	command.Flags().StringVar(&options.tokenFile, "token-file", "", `File with the connector token; "-" or no flag reads standard input`)
	command.Flags().BoolVar(&options.disable, "disable", false, "Turn the connector off and remove its token from local custody")
	command.Flags().BoolVar(&options.outputJSON, "json", false, "Emit the secret-free result")
	return command
}

func runSetupAIConnect(cmd *cobra.Command, options aiConnectOptions) error {
	var (
		result aiConnectResult
		err    error
	)
	if options.disable {
		if options.tokenFile != "" {
			return machineAwareCommandError(cmd, errors.New("--disable takes no token"))
		}
		result, err = disableAIConnector(getWorkDir())
	} else {
		var token []byte
		token, err = readAIConnectorToken(cmd, options.tokenFile)
		if err == nil {
			result, err = enableAIConnector(getWorkDir(), token)
			clear(token)
		}
	}
	if err != nil {
		return machineAwareCommandError(cmd, err)
	}
	if options.outputJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}
	message := "ai: the kombify AI connector token is in local custody and the connector is turned on. Run stackkit generate and stackkit apply to start it; revoke the endpoint in Companion Studio at any time."
	if result.Preparation == applicationlifecycle.KombifyAIConnectorDisabledPreparation {
		message = "ai: the kombify AI connector is turned off and its token is removed from local custody. Run stackkit generate and stackkit apply to stop it; delete the endpoint in Companion Studio to revoke the token."
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\nSpec: %s\n", message, result.SpecHash)
	return err
}

// readAIConnectorToken reads the token from a file or standard input, hidden
// when standard input is a terminal. The token never passes through an
// argument, so it stays out of shell history and process lists.
func readAIConnectorToken(cmd *cobra.Command, path string) ([]byte, error) {
	if path != "" && path != "-" {
		file, err := os.Open(path) //nolint:gosec // the owner names the private token file
		if err != nil {
			return nil, fmt.Errorf("open the connector token file: %w", err)
		}
		defer func() { _ = file.Close() }()
		return readBoundedAIConnectorToken(file)
	}
	if file, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		_, _ = fmt.Fprint(cmd.ErrOrStderr(), "Paste the kombify AI connector token: ")
		token, err := term.ReadPassword(int(file.Fd()))
		_, _ = fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return nil, fmt.Errorf("read the connector token: %w", err)
		}
		return token, nil
	}
	return readBoundedAIConnectorToken(cmd.InOrStdin())
}

func readBoundedAIConnectorToken(reader io.Reader) ([]byte, error) {
	token, err := io.ReadAll(io.LimitReader(reader, maxAIConnectorTokenInput+1))
	if err != nil {
		clear(token)
		return nil, fmt.Errorf("read the connector token: %w", err)
	}
	if len(token) > maxAIConnectorTokenInput {
		clear(token)
		return nil, appsetup.ErrKombifyAIConnectorToken
	}
	return token, nil
}

// enableAIConnector checks the token before touching the workspace, custodies
// it, and only then turns the connector setting on, so the setting never names
// an absent token.
func enableAIConnector(workspace string, raw []byte) (aiConnectResult, error) {
	token, err := appsetup.NormalizeKombifyAIConnectorToken(raw)
	if err != nil {
		return aiConnectResult{}, err
	}
	defer clear(token)
	service, current, err := loadAIConnectorSpec(workspace)
	if err != nil {
		return aiConnectResult{}, err
	}
	if err := localevidence.StoreLocalIssuedSecret(workspace, architecturev2renderer.PrivateAIConnectorTokenRef, token); err != nil {
		return aiConnectResult{}, fmt.Errorf("keep the connector token in local custody: %w", err)
	}
	specHash, err := persistAIConnectorSetting(workspace, service, current, true)
	if err != nil {
		return aiConnectResult{}, err
	}
	return aiConnectResult{
		WorkloadRef: "ai", Preparation: applicationlifecycle.KombifyAIConnectorEnabledPreparation,
		SpecHash: specHash, NextSteps: []string{"stackkit generate", "stackkit apply"},
	}, nil
}

// disableAIConnector turns the setting off first, so no later apply looks for
// the token, and then removes the token from custody.
func disableAIConnector(workspace string) (aiConnectResult, error) {
	service, current, err := loadAIConnectorSpec(workspace)
	if err != nil {
		return aiConnectResult{}, err
	}
	specHash, err := persistAIConnectorSetting(workspace, service, current, false)
	if err != nil {
		return aiConnectResult{}, err
	}
	if err := localevidence.RemoveLocalIssuedSecret(workspace, architecturev2renderer.PrivateAIConnectorTokenRef); err != nil {
		return aiConnectResult{}, fmt.Errorf("remove the connector token from local custody: %w", err)
	}
	return aiConnectResult{
		WorkloadRef: "ai", Preparation: applicationlifecycle.KombifyAIConnectorDisabledPreparation,
		SpecHash: specHash, NextSteps: []string{"stackkit generate", "stackkit apply"},
	}, nil
}

type aiConnectorSpec struct {
	path       string
	specHash   string
	canonical  []byte
	aiSelected bool
}

func loadAIConnectorSpec(workspace string) (*architecturev2.Service, aiConnectorSpec, error) {
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return nil, aiConnectorSpec{}, fmt.Errorf("load the embedded StackSpec authority: %w", err)
	}
	loaded, err := config.NewLoader(workspace).ReadStackSpecDocument(specFile)
	if err != nil {
		return nil, aiConnectorSpec{}, err
	}
	validation, err := service.ValidateStackSpec(loaded.Document.Raw)
	if err != nil {
		return nil, aiConnectorSpec{}, err
	}
	spec, err := decodeAIConnectorSpec(validation.CanonicalStackSpec)
	if err != nil {
		return nil, aiConnectorSpec{}, err
	}
	workloads, _ := spec["workloads"].(map[string]any)
	ai, _ := workloads["ai"].(map[string]any)
	alternative, _ := ai["alternative"].(string)
	return service, aiConnectorSpec{
		path: loaded.Path, specHash: validation.SpecHash, canonical: validation.CanonicalStackSpec,
		aiSelected: alternative == "private-ai",
	}, nil
}

// persistAIConnectorSetting writes the kombify-connector setting of the ai
// workload through the compare-and-swap StackSpec store.
func persistAIConnectorSetting(workspace string, service *architecturev2.Service, current aiConnectorSpec, enabled bool) (string, error) {
	if !current.aiSelected {
		if !enabled {
			return current.specHash, nil
		}
		return "", errors.New("the kombify AI connector needs the Private AI use case (workload ai, alternative private-ai) in this StackSpec")
	}
	spec, err := decodeAIConnectorSpec(current.canonical)
	if err != nil {
		return "", err
	}
	ai := spec["workloads"].(map[string]any)["ai"].(map[string]any)
	settings, _ := ai["settings"].(map[string]any)
	if settings == nil {
		settings = map[string]any{}
	}
	if enabled {
		settings[architecturev2renderer.PrivateAIConnectorSetting] = true
	} else {
		delete(settings, architecturev2renderer.PrivateAIConnectorSetting)
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

func decodeAIConnectorSpec(canonical []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	var spec map[string]any
	if err := decoder.Decode(&spec); err != nil {
		return nil, fmt.Errorf("decode the canonical StackSpec: %w", err)
	}
	return spec, nil
}
