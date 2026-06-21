package testscenarios

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"
)

const BrowserPreflightEvidenceKind = "browser-evidence-preflight"

var RequiredBaseKitBetaBrowserPreflightChecks = []string{
	"Required command: go",
	"Required command: node",
	"Required command: npm",
	"Required command: docker",
	"Docker Desktop availability",
	"Docker Desktop context",
	"Install isolated Playwright package",
	"Install isolated Playwright Chromium",
	"Playwright package availability",
	"Playwright Chromium availability",
}

type BrowserPreflightEvidence struct {
	ScenarioID          string                  `json:"scenarioId"`
	RunID               string                  `json:"runId,omitempty"`
	Kind                string                  `json:"kind"`
	Status              string                  `json:"status"`
	GeneratedAt         string                  `json:"generatedAt"`
	EvidenceRoot        string                  `json:"evidenceRoot"`
	PlaywrightModuleDir string                  `json:"playwrightModuleDir"`
	BrowserChannel      string                  `json:"browserChannel,omitempty"`
	PhaseTimeoutSeconds int                     `json:"phaseTimeoutSeconds"`
	Checks              []BrowserPreflightCheck `json:"checks"`
	FailedChecks        []string                `json:"failedChecks,omitempty"`
	Error               string                  `json:"error,omitempty"`
}

type BrowserPreflightCheck struct {
	Name           string                                  `json:"name"`
	Status         string                                  `json:"status"`
	TimeoutSeconds int                                     `json:"timeoutSeconds"`
	Error          string                                  `json:"error,omitempty"`
	Evidence       map[string]string                       `json:"evidence,omitempty"`
	NativeCommand  *BrowserWrapperNativeCommandDiagnostics `json:"nativeCommand,omitempty"`
}

func LoadBrowserPreflightEvidence(path string) (BrowserPreflightEvidence, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BrowserPreflightEvidence{}, fmt.Errorf("read browser preflight evidence %s: %w", path, err)
	}
	var evidence BrowserPreflightEvidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		return BrowserPreflightEvidence{}, fmt.Errorf("parse browser preflight evidence %s: %w", path, err)
	}
	return evidence, nil
}

func ValidateBaseKitBetaBrowserPreflightEvidence(e BrowserPreflightEvidence) error {
	if strings.TrimSpace(e.ScenarioID) != "SK-S1" {
		return fmt.Errorf("browser preflight evidence scenarioId = %q, want SK-S1", e.ScenarioID)
	}
	if strings.TrimSpace(e.Kind) != BrowserPreflightEvidenceKind {
		return fmt.Errorf("browser preflight evidence kind = %q, want %s", e.Kind, BrowserPreflightEvidenceKind)
	}
	if strings.TrimSpace(e.RunID) == "" {
		return fmt.Errorf("browser preflight evidence must include runId")
	}
	status := strings.TrimSpace(e.Status)
	if status != BrowserEvidenceStatusPass && status != BrowserEvidenceStatusFail {
		return fmt.Errorf("browser preflight evidence status = %q, want pass or fail", e.Status)
	}
	if _, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(e.GeneratedAt)); err != nil {
		return fmt.Errorf("browser preflight evidence generatedAt must be RFC3339: %w", err)
	}
	if strings.TrimSpace(e.EvidenceRoot) == "" {
		return fmt.Errorf("browser preflight evidence must include evidenceRoot")
	}
	if strings.TrimSpace(e.PlaywrightModuleDir) == "" {
		return fmt.Errorf("browser preflight evidence must include playwrightModuleDir")
	}
	if strings.TrimSpace(e.BrowserChannel) == "" {
		return fmt.Errorf("browser preflight evidence must include browserChannel")
	}
	if e.PhaseTimeoutSeconds <= 0 {
		return fmt.Errorf("browser preflight evidence must record phaseTimeoutSeconds")
	}
	if e.PhaseTimeoutSeconds > MaxBrowserCheckDurationSeconds {
		return fmt.Errorf("browser preflight evidence phaseTimeoutSeconds = %d, exceeds 15 minute budget", e.PhaseTimeoutSeconds)
	}
	browserChannel := browserPreflightChannelLabel(e.BrowserChannel)
	failedChecks, err := validateBrowserPreflightChecks(e.Checks, browserChannel)
	if err != nil {
		return err
	}
	if status == BrowserEvidenceStatusPass {
		if len(failedChecks) > 0 {
			return fmt.Errorf("browser preflight evidence status is pass but failed checks are present: %s", strings.Join(failedChecks, ", "))
		}
		if len(e.FailedChecks) > 0 {
			return fmt.Errorf("browser preflight evidence status is pass but failedChecks is not empty")
		}
		if strings.TrimSpace(e.Error) != "" {
			return fmt.Errorf("browser preflight evidence status is pass but error is set")
		}
		return nil
	}
	if len(failedChecks) == 0 {
		return fmt.Errorf("browser preflight evidence status is fail but no checks failed")
	}
	if strings.TrimSpace(e.Error) == "" {
		return fmt.Errorf("browser preflight evidence status is fail but error is empty")
	}
	expectedFailedChecks := slices.Clone(failedChecks)
	actualFailedChecks := normalizedPreflightFailedChecks(e.FailedChecks)
	if !reflect.DeepEqual(actualFailedChecks, expectedFailedChecks) {
		return fmt.Errorf("browser preflight evidence failedChecks = %v, want %v", actualFailedChecks, expectedFailedChecks)
	}
	return nil
}

func validateBrowserPreflightChecks(checks []BrowserPreflightCheck, browserChannel string) ([]string, error) {
	if len(checks) == 0 {
		return nil, fmt.Errorf("browser preflight evidence must include checks")
	}
	checksByName := map[string]BrowserPreflightCheck{}
	failedChecks := []string{}
	for _, check := range checks {
		name := strings.TrimSpace(check.Name)
		if name == "" {
			return nil, fmt.Errorf("browser preflight evidence contains a check without name")
		}
		if _, exists := checksByName[name]; exists {
			return nil, fmt.Errorf("browser preflight evidence contains duplicate check %q", name)
		}
		status := strings.TrimSpace(check.Status)
		switch status {
		case BrowserEvidenceStatusPass, BrowserEvidenceStatusFail, "skipped":
		default:
			return nil, fmt.Errorf("browser preflight evidence check %q status = %q, want pass, fail, or skipped", name, check.Status)
		}
		if check.TimeoutSeconds < 0 {
			return nil, fmt.Errorf("browser preflight evidence check %q timeoutSeconds must be non-negative", name)
		}
		if check.TimeoutSeconds > MaxBrowserCheckDurationSeconds {
			return nil, fmt.Errorf("browser preflight evidence check %q timeoutSeconds = %d, exceeds 15 minute budget", name, check.TimeoutSeconds)
		}
		if status == BrowserEvidenceStatusFail {
			if strings.TrimSpace(check.Error) == "" {
				return nil, fmt.Errorf("browser preflight evidence failed check %q must include error", name)
			}
			if err := validateBrowserNativeCommandDiagnostics(check.NativeCommand, "browser preflight evidence failed check "+name); err != nil {
				return nil, err
			}
			failedChecks = append(failedChecks, name)
		}
		checksByName[name] = check
	}
	for _, required := range RequiredBaseKitBetaBrowserPreflightChecks {
		check, ok := checksByName[required]
		if !ok {
			return nil, fmt.Errorf("browser preflight evidence missing required check %q", required)
		}
		if err := validateBrowserPreflightRequiredStatus(check, browserChannel); err != nil {
			return nil, err
		}
		if err := validateBrowserPreflightRequiredEvidence(check, browserChannel); err != nil {
			return nil, err
		}
	}
	return failedChecks, nil
}

func validateBrowserPreflightRequiredStatus(check BrowserPreflightCheck, browserChannel string) error {
	status := strings.TrimSpace(check.Status)
	if status == BrowserEvidenceStatusPass || status == BrowserEvidenceStatusFail {
		return nil
	}
	if status != "skipped" {
		return nil
	}
	if check.Name == "Install isolated Playwright Chromium" && browserChannel != "playwright-chromium" {
		return nil
	}
	return fmt.Errorf("browser preflight evidence check %q is skipped; only Install isolated Playwright Chromium may be skipped when browserChannel is an installed browser channel", check.Name)
}

func validateBrowserPreflightRequiredEvidence(check BrowserPreflightCheck, browserChannel string) error {
	if strings.TrimSpace(check.Status) != BrowserEvidenceStatusPass {
		return nil
	}
	switch check.Name {
	case "Docker Desktop context":
		// Docker Desktop hosts report desktop-linux; plain Docker Engine
		// hosts (CI runners, Linux servers) report default. Both are Linux
		// engines the fresh-VM rollout and capture can use.
		output := strings.TrimSpace(check.Evidence["output"])
		if output != "desktop-linux" && output != "default" {
			return fmt.Errorf("browser preflight evidence check %q output = %q, want desktop-linux or default", check.Name, check.Evidence["output"])
		}
	case "Playwright package availability":
		if strings.TrimSpace(check.Evidence["output"]) != "playwright=available" {
			return fmt.Errorf("browser preflight evidence check %q output = %q, want playwright=available", check.Name, check.Evidence["output"])
		}
	case "Playwright Chromium availability":
		want := "chromium=available"
		if browserChannel != "playwright-chromium" {
			want = "browser-channel=" + browserChannel
		}
		if strings.TrimSpace(check.Evidence["output"]) != want {
			return fmt.Errorf("browser preflight evidence check %q output = %q, want %s", check.Name, check.Evidence["output"], want)
		}
	}
	return nil
}

func browserPreflightChannelLabel(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "", "default", "chromium", "playwright-chromium":
		return "playwright-chromium"
	default:
		return value
	}
}

func normalizedPreflightFailedChecks(values []string) []string {
	result := []string{}
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
