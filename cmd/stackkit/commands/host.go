package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/kombifyio/stackkits/internal/hostconformance"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/spf13/cobra"
)

type hostConformanceCommandDeps struct {
	producer  func() hostconformance.Producer
	candidate func(string) (hostconformance.Candidate, error)
	readFile  func(string) ([]byte, error)
	writeFile func(string, []byte) error
}

func defaultHostConformanceCommandDeps() hostConformanceCommandDeps {
	return hostConformanceCommandDeps{
		producer: func() hostconformance.Producer {
			return hostconformance.Producer{Probe: hostconformance.LocalProbe{}, Now: time.Now}
		},
		candidate: func(stackKitsVersion string) (hostconformance.Candidate, error) {
			return hostconformance.CandidateFromExecutable(stackKitsVersion, "")
		},
		readFile:  os.ReadFile,
		writeFile: writeNewFileAtomic0600,
	}
}

var hostCmd = newHostCommand(defaultHostConformanceCommandDeps())

func newHostCommand(deps hostConformanceCommandDeps) *cobra.Command {
	host := &cobra.Command{
		Use:   "host",
		Short: "Provider-neutral host contract operations",
		Annotations: map[string]string{
			noDeployObservabilityAnnotation: "true",
		},
	}
	host.AddCommand(newHostConformanceCommand(deps))
	return host
}

func newHostConformanceCommand(deps hostConformanceCommandDeps) *cobra.Command {
	var bindingPath string
	var outputPath string
	cmd := &cobra.Command{
		Use:   "conformance",
		Short: "Produce a provider-neutral conformance receipt on this host",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHostConformance(cmd.Context(), cmd.OutOrStdout(), bindingPath, outputPath, deps)
		},
	}
	cmd.Flags().StringVar(&bindingPath, "binding", "", "Local ExternalHostBinding JSON file")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "-", "Receipt destination ('-' for JSON on stdout)")
	_ = cmd.MarkFlagRequired("binding")
	return cmd
}

func runHostConformance(ctx context.Context, stdout io.Writer, bindingPath, outputPath string, deps hostConformanceCommandDeps) error {
	if deps.producer == nil || deps.candidate == nil || deps.readFile == nil || deps.writeFile == nil {
		return errors.New("host conformance command dependencies are incomplete")
	}
	raw, err := deps.readFile(bindingPath)
	if err != nil {
		return fmt.Errorf("read external host binding: %w", err)
	}
	var binding resolvedplan.ExternalHostBinding
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&binding); err != nil {
		return fmt.Errorf("decode external host binding: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("external host binding must contain exactly one JSON document")
		}
		return fmt.Errorf("decode trailing external host binding data: %w", err)
	}
	candidate, err := deps.candidate(architectureV2ComponentVersion(version))
	if err != nil {
		return fmt.Errorf("identify running StackKits candidate: %w", err)
	}
	receipt, err := deps.producer().Produce(ctx, binding, candidate)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("encode host conformance receipt: %w", err)
	}
	encoded = append(encoded, '\n')
	if outputPath == "-" {
		if _, err := stdout.Write(encoded); err != nil {
			return fmt.Errorf("write host conformance receipt: %w", err)
		}
		return nil
	}
	if outputPath == "" {
		return errors.New("host conformance output path is required")
	}
	return deps.writeFile(outputPath, encoded)
}

func writeNewFileAtomic0600(path string, data []byte) error {
	target, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve host conformance output path: %w", err)
	}
	parent := filepath.Dir(target)
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("inspect host conformance output directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("host conformance output parent must be a directory")
	}
	if _, err := os.Lstat(target); err == nil {
		return errors.New("host conformance output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect host conformance output: %w", err)
	}
	temporary, err := os.CreateTemp(parent, "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create host conformance output: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure host conformance output: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write host conformance output: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync host conformance output: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close host conformance output: %w", err)
	}
	// Installing with a hard link gives a no-clobber atomic create on the same
	// filesystem. The temporary name is removed only after the target exists.
	if err := os.Link(temporaryPath, target); err != nil {
		return fmt.Errorf("install host conformance output: %w", err)
	}
	if err := os.Chmod(target, 0o600); err != nil {
		_ = os.Remove(target)
		return fmt.Errorf("secure installed host conformance output: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("remove host conformance temporary output: %w", err)
	}
	return nil
}
