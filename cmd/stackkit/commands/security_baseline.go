package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/securitybaseline"
	"github.com/kombifyio/stackkits/pkg/models"
)

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

func securityBaselineApplies(spec *models.StackSpec) bool {
	// The host security baseline is a universal Foundation contract: every
	// single-environment server deployment (any kit) gets it. The current legacy
	// executor supports Linux/apt hosts and fails closed on unsupported Linux
	// package managers; target eligibility is not inferred from compatibility
	// documentation.
	return spec != nil
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
