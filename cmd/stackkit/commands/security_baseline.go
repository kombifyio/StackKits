package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/securitybaseline"
	"github.com/kombifyio/stackkits/pkg/models"
)

const securityBaselineEvidencePath = ".stackkit/security-baseline.json"
const securityBaselineSchemaVersion = securitybaseline.EvidenceSchemaVersion
const securityBaselineMode = securitybaseline.EvidenceModePublicBeta
const securityBaselineTimeout = 10 * time.Minute

type securityBaselineConfig struct {
	SSHPort         int
	PermitRootLogin string
	MaxAuthTries    int
}

type securityBaselineEvidence struct {
	SchemaVersion string            `json:"schemaVersion"`
	Status        string            `json:"status"`
	Mode          string            `json:"mode"`
	AppliedAt     string            `json:"appliedAt,omitempty"`
	Reason        string            `json:"reason,omitempty"`
	Controls      map[string]string `json:"controls,omitempty"`
}

func applyPublicBetaSecurityBaseline(ctx context.Context, wd string, spec *models.StackSpec) error {
	if !securityBaselineApplies(spec) {
		return nil
	}
	if disabledByEnv("STACKKIT_SECURITY_BASELINE") {
		printWarning("Security baseline skipped because STACKKIT_SECURITY_BASELINE disables it")
		return writeSecurityBaselineEvidence(wd, securityBaselineEvidence{
			SchemaVersion: securityBaselineSchemaVersion,
			Status:        "skipped",
			Mode:          securityBaselineMode,
			Reason:        "disabled-by-env",
		})
	}
	if runtime.GOOS != "linux" {
		printWarning("Security baseline skipped on non-Linux host %s", runtime.GOOS)
		return writeSecurityBaselineEvidence(wd, securityBaselineEvidence{
			SchemaVersion: securityBaselineSchemaVersion,
			Status:        "skipped",
			Mode:          securityBaselineMode,
			Reason:        "non-linux-host",
		})
	}

	cfg := securityBaselineConfigForSpec(spec)
	script, err := securityBaselineScript(cfg)
	if err != nil {
		return fmt.Errorf("render security baseline: %w", err)
	}
	baselineCtx, cancel := context.WithTimeout(ctx, securityBaselineTimeout)
	defer cancel()

	printInfo("Applying BaseKit public-beta security baseline...")
	rolloutEvent("security_baseline", "started", "applying host security baseline", nil)
	cmd := exec.CommandContext(baselineCtx, "sh", "-c", script)
	cmd.Dir = wd
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		failure := err
		if errors.Is(baselineCtx.Err(), context.DeadlineExceeded) {
			failure = fmt.Errorf("timed out after %s: %w", securityBaselineTimeout, baselineCtx.Err())
		}
		rolloutFailure("security_baseline", failure)
		return fmt.Errorf("security baseline failed: %w\n%s", failure, redactedSecurityBaselineOutput(out.String()))
	}

	evidence, err := readSecurityBaselineEvidence(wd)
	if err != nil {
		rolloutFailure("security_baseline", err)
		return err
	}
	if err := validateSecurityBaselineEvidence(evidence); err != nil {
		rolloutFailure("security_baseline", err)
		return err
	}
	rolloutEvent("security_baseline", "succeeded", "host security baseline applied", evidence.Controls)
	printSuccess("Security baseline applied")
	return nil
}

func securityBaselineApplies(spec *models.StackSpec) bool {
	// The host security baseline is a universal Foundation contract: every
	// single-environment server deployment (any kit) gets it. The current legacy
	// executor supports Linux/apt hosts and fails closed on unsupported Linux
	// package managers; target eligibility is not inferred from compatibility
	// documentation.
	return spec != nil
}

func disabledByEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "0", "false", "off", "skip", "disabled":
		return true
	default:
		return false
	}
}

func securityBaselineConfigForSpec(spec *models.StackSpec) securityBaselineConfig {
	cfg := securityBaselineConfig{
		SSHPort:         22,
		PermitRootLogin: "prohibit-password",
		MaxAuthTries:    3,
	}
	if spec == nil {
		return cfg
	}
	if spec.SSH.Port > 0 {
		cfg.SSHPort = spec.SSH.Port
	}
	if spec.SSH.MaxAuthTries > 0 {
		cfg.MaxAuthTries = spec.SSH.MaxAuthTries
	}
	if permit := securitybaseline.NormalizePermitRootLogin(spec.SSH.PermitRootLogin); permit != "" {
		cfg.PermitRootLogin = permit
	}
	return cfg
}

func securityBaselineScript(cfg securityBaselineConfig) (string, error) {
	return securitybaseline.Build(securitybaseline.Config{
		Mode:                         securitybaseline.ModeLegacyV1,
		SSHPort:                      cfg.SSHPort,
		PermitRootLogin:              cfg.PermitRootLogin,
		MaxAuthTries:                 cfg.MaxAuthTries,
		PackageManagerLockWaitScript: packageManagerLockWaitScript(),
	})
}

func writeSecurityBaselineEvidence(wd string, evidence securityBaselineEvidence) error {
	if evidence.SchemaVersion == "" {
		evidence.SchemaVersion = securityBaselineSchemaVersion
	}
	path := filepath.Join(wd, filepath.FromSlash(securityBaselineEvidencePath))
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("create security baseline evidence directory: %w", err)
	}
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal security baseline evidence: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write security baseline evidence: %w", err)
	}
	return nil
}

func readSecurityBaselineEvidence(wd string) (securityBaselineEvidence, error) {
	path := filepath.Join(wd, filepath.FromSlash(securityBaselineEvidencePath))
	data, err := os.ReadFile(path)
	if err != nil {
		return securityBaselineEvidence{}, fmt.Errorf("read security baseline evidence: %w", err)
	}
	var evidence securityBaselineEvidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		return securityBaselineEvidence{}, fmt.Errorf("parse security baseline evidence: %w", err)
	}
	return evidence, nil
}

func validateSecurityBaselineEvidence(evidence securityBaselineEvidence) error {
	if strings.TrimSpace(evidence.SchemaVersion) != securityBaselineSchemaVersion {
		return fmt.Errorf("security baseline evidence schemaVersion = %q, want %q", evidence.SchemaVersion, securityBaselineSchemaVersion)
	}
	if strings.TrimSpace(evidence.Mode) != securityBaselineMode {
		return fmt.Errorf("security baseline evidence mode = %q, want %q", evidence.Mode, securityBaselineMode)
	}
	if appliedAt := strings.TrimSpace(evidence.AppliedAt); appliedAt == "" {
		return fmt.Errorf("security baseline evidence appliedAt is missing")
	} else if _, err := time.Parse(time.RFC3339, appliedAt); err != nil {
		return fmt.Errorf("security baseline evidence appliedAt = %q, want RFC3339: %w", appliedAt, err)
	}
	if evidence.Status != "pass" {
		return fmt.Errorf("security baseline evidence status = %q, want pass", evidence.Status)
	}
	if evidence.Controls == nil {
		return fmt.Errorf("security baseline evidence controls are missing")
	}
	required := map[string]string{
		"firewall":                  "enabled",
		"sshPasswordAuthentication": "disabled",
		"fail2ban":                  "enabled",
		"unattendedUpgrades":        "security",
		"sysctl":                    "applied",
	}
	for key, want := range required {
		if got := strings.TrimSpace(evidence.Controls[key]); got != want {
			return fmt.Errorf("security baseline evidence controls[%s] = %q, want %q", key, got, want)
		}
	}
	if got := strings.TrimSpace(evidence.Controls["sshRootLogin"]); got != "key-only" && got != "disabled" {
		return fmt.Errorf("security baseline evidence controls[sshRootLogin] = %q, want key-only or disabled", got)
	}
	return nil
}

func redactedSecurityBaselineOutput(output string) string {
	const max = 6000
	if len(output) <= max {
		return output
	}
	return output[len(output)-max:]
}
