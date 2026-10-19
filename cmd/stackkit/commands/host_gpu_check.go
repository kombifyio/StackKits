package commands

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/kombifyio/stackkits/internal/hostpreflight"
	"github.com/spf13/cobra"
)

const gpuCheckSchemaVersion = "stackkit.host-gpu-check/v1"

type gpuCheckResult struct {
	SchemaVersion string                        `json:"schemaVersion"`
	Check         hostpreflight.Check           `json:"check"`
	Evidence      hostpreflight.GPULoadEvidence `json:"evidence"`
}

func newHostGPUCheckCommand() *cobra.Command {
	var (
		jsonOutput bool
		runs       int
		image      string
	)
	cmd := &cobra.Command{
		Use:   "gpu-check",
		Short: "Prove the NVIDIA GPU under a short real CUDA load",
		Long: `Run NVIDIA's CUDA vectorAdd sample in a container on the GPU (Docker CDI
device nvidia.com/gpu=all), then read the kernel log for NVRM Xid errors and
ask nvidia-smi again.

nvidia-smi listing the GPU is not proof that it works: a GPU on a weak power
supply or a marginal link, typical for a Thunderbolt eGPU, answers at idle and
falls off the bus (Xid 79) as soon as CUDA draws power. This check refuses
then and names the cause.

It changes nothing on the host except pulling the pinned sample image. Run it
as root so the kernel log can be read.`,
		Example: `  # Prove the GPU after the driver and Container Toolkit are installed
  sudo stackkit host gpu-check

  # Longer load, machine-readable result
  sudo stackkit host gpu-check --runs 50 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			check, evidence := hostpreflight.RunGPULoadCheck(cmd.Context(), hostGPUCheckCommandOutput, image, runs)
			if jsonOutput {
				status := "success"
				if check.Status == hostpreflight.StatusBlocked {
					status = "failed"
				}
				if err := writeCommandResultStatus(cmd, cmd.CommandPath(), status, gpuCheckResult{
					SchemaVersion: gpuCheckSchemaVersion, Check: check, Evidence: evidence,
				}); err != nil {
					return err
				}
			} else {
				printGPUCheck(check, evidence)
			}
			if check.Status == hostpreflight.StatusBlocked {
				return &exitCodeError{code: ExitCodeHostBlocked, err: fmt.Errorf("%s: %s", check.ID, check.Summary)}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit the check and its evidence as machine-readable JSON")
	cmd.Flags().IntVar(&runs, "runs", hostpreflight.DefaultGPULoadRuns, "How many times the CUDA sample runs")
	cmd.Flags().StringVar(&image, "image", hostpreflight.GPULoadImage, "CUDA sample image that prints \"Test PASSED\"")
	return cmd
}

func hostGPUCheckCommandOutput(ctx context.Context, argv []string) ([]byte, error) {
	return exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
}

func printGPUCheck(check hostpreflight.Check, evidence hostpreflight.GPULoadEvidence) {
	if humanOutputSuppressed() {
		return
	}
	switch check.Status {
	case hostpreflight.StatusPass:
		printSuccess("%s", check.Summary)
	case hostpreflight.StatusWarning:
		printWarning("%s", check.Summary)
	default:
		printError("%s", check.Summary)
	}
	for _, event := range evidence.XidDuringLoad {
		printInfo("  %s", event.Line)
	}
	if evidence.LoadFailureLog != "" {
		printInfo("  %s", strings.ReplaceAll(evidence.LoadFailureLog, "\n", "\n  "))
	}
	for _, step := range check.Remediation {
		printInfo("  %s", step)
	}
}
