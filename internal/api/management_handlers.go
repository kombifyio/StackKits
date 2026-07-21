package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/logging"
	"github.com/kombifyio/stackkits/internal/stackspecadmission"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
	"github.com/kombifyio/stackkits/internal/verify"
	"github.com/kombifyio/stackkits/pkg/models"
)

type managementCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Target  string `json:"target,omitempty"`
	Message string `json:"message"`
}

type managementVerifyRequest struct {
	HTTP   bool `json:"http"`
	Strict bool `json:"strict"`
}

type managementSpecSummary struct {
	SourceVersion   stackspecmigration.SourceVersion
	StackKit        string
	Mode            string
	Domain          string
	SpecHash        string
	ValidationScope string
}

func (s *Server) handleManagementStatus(w http.ResponseWriter, r *http.Request) {
	spec, specPath, specErr := s.loadWorkspaceSpecSummary()
	state, statePath, stateErr := s.loadWorkspaceState()
	latestRunID := ""
	if s.logDir() != "" {
		if files, err := logging.ListLogFiles(s.logDir()); err == nil && len(files) > 0 {
			latestRunID = strings.TrimSuffix(files[len(files)-1], ".jsonl")
		}
	}

	data := map[string]any{
		"status":         "not_ready",
		"baseDir":        s.config.BaseDir,
		"specPath":       specPath,
		"specLoaded":     specErr == nil && spec != nil,
		"statePath":      statePath,
		"stateLoaded":    stateErr == nil && state != nil,
		"logDir":         s.logDir(),
		"latestRunId":    latestRunID,
		"generatedAt":    time.Now().UTC().Format(time.RFC3339),
		"mutationPolicy": "management mutations are disabled by default; use CLI apply/remove with explicit operator approval",
	}
	if spec != nil {
		data["stackkit"] = spec.StackKit
		data["sourceVersion"] = spec.SourceVersion
		data["validationScope"] = spec.ValidationScope
		if spec.SpecHash != "" {
			data["specHash"] = spec.SpecHash
		}
		if spec.SourceVersion == stackspecmigration.SourceVersionV2Alpha1 {
			data["status"] = "intent_valid"
			data["operational"] = false
			data["readiness"] = "resolve-required"
			data["stateAuthority"] = "resolved-plan-and-execution-evidence"
		} else {
			data["status"] = "ok"
			data["operational"] = true
			data["mode"] = spec.Mode
			data["domain"] = spec.Domain
		}
	}
	if specErr != nil {
		data["specError"] = specErr.Error()
		var resolveErr *architecturev2.ResolveError
		if errors.As(specErr, &resolveErr) {
			data["status"] = resolveErr.Code
			data["operational"] = false
			if resolveErr.Report != nil {
				data["migrationReport"] = resolveErr.Report
			}
		} else {
			data["status"] = "error"
		}
	}
	if state != nil && spec != nil && spec.SourceVersion == stackspecmigration.SourceVersionV1 {
		data["deploymentStatus"] = state.Status
		data["lastApplied"] = state.LastApplied
	}
	if stateErr != nil {
		data["stateError"] = stateErr.Error()
	}
	writeSuccess(w, r, http.StatusOK, data)
}

func (s *Server) handleManagementVerify(w http.ResponseWriter, r *http.Request) {
	if s.rejectNativeManagementWithoutPlan(w) {
		return
	}
	var req managementVerifyRequest
	if r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "failed to read request body")
			return
		}
		defer r.Body.Close()
		if len(strings.TrimSpace(string(body))) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				writeError(w, r, http.StatusBadRequest, "invalid JSON format")
				return
			}
		}
	}

	spec, _, specErr := s.loadWorkspaceSpec()
	if specErr != nil {
		var resolveErr *architecturev2.ResolveError
		if errors.As(specErr, &resolveErr) {
			writeMappedArchitectureV2ResolveError(w, specErr)
		} else {
			writeError(w, r, http.StatusBadRequest, specErr.Error())
		}
		return
	}
	state, _, _ := s.loadWorkspaceState()
	access := s.loadVerifyAccessSummary()
	report := verify.RunLocal(context.Background(), verify.Input{
		Spec:   spec,
		State:  state,
		Docker: docker.NewClient(),
		Access: access,
		Options: verify.Options{
			HTTP:   req.HTTP,
			Strict: req.Strict,
		},
		HTTP: &http.Client{Timeout: 10 * time.Second},
	})
	writeSuccess(w, r, http.StatusOK, report)
}

func (s *Server) handleManagementDoctor(w http.ResponseWriter, r *http.Request) {
	if s.rejectNativeManagementWithoutPlan(w) {
		return
	}
	spec, specPath, specErr := s.loadWorkspaceSpec()
	state, statePath, stateErr := s.loadWorkspaceState()
	checks := []managementCheck{}
	add := func(name, status, target, message string) {
		checks = append(checks, managementCheck{Name: name, Status: status, Target: target, Message: message})
	}

	if specErr != nil || spec == nil {
		add("stack-spec", "fail", specPath, "stack-spec.yaml is missing or invalid")
	} else {
		add("stack-spec", "pass", specPath, "stack spec loaded")
		if spec.StackKit == "basement-kit" {
			add("release-kit", "pass", spec.StackKit, "BaseKit is the release-ready one-click path")
		} else {
			add("release-kit", "warn", spec.StackKit, "Unreleased kit definitions remain outside the public beta release matrix")
		}
	}

	if stateErr != nil {
		add("deployment-state", "fail", statePath, stateErr.Error())
	} else if state == nil {
		add("deployment-state", "warn", statePath, "deployment state is missing; run stackkit apply first")
	} else {
		add("deployment-state", "pass", statePath, "deployment state loaded")
	}

	deployDir := filepath.Join(s.config.BaseDir, config.GetDeployDir())
	if hasGeneratedTerraform(deployDir) {
		add("generated-preview", "pass", deployDir, "generated OpenTofu files are present")
	} else {
		add("generated-preview", "warn", deployDir, "generated OpenTofu files are missing; run stackkit generate")
	}

	if s.logDir() != "" {
		if _, err := os.Stat(s.logDir()); err == nil {
			add("logs", "pass", s.logDir(), "log directory is readable")
		} else {
			add("logs", "warn", s.logDir(), "log directory is not present yet")
		}
	}

	status := "pass"
	for _, check := range checks {
		if check.Status == "fail" {
			status = "fail"
			break
		}
		if check.Status == "warn" && status == "pass" {
			status = "warn"
		}
	}
	writeSuccess(w, r, http.StatusOK, map[string]any{
		"status": status,
		"checks": checks,
	})
}

func (s *Server) handleManagementPlan(w http.ResponseWriter, r *http.Request) {
	if s.rejectNativeManagementWithoutPlan(w) {
		return
	}
	spec, specPath, specErr := s.loadWorkspaceSpec()
	deployDir := filepath.Join(s.config.BaseDir, config.GetDeployDir())
	tfFiles := listTerraformFiles(deployDir)
	ready := specErr == nil && spec != nil && len(tfFiles) > 0

	data := map[string]any{
		"dryRun":         true,
		"mutation":       false,
		"ready":          ready,
		"specPath":       specPath,
		"deployDir":      deployDir,
		"terraformFiles": tfFiles,
		"nextCommands": []string{
			"stackkit validate",
			"stackkit generate --force",
			"stackkit plan",
		},
	}
	if spec != nil {
		data["stackkit"] = spec.StackKit
		data["mode"] = spec.Mode
	}
	if specErr != nil {
		data["specError"] = specErr.Error()
	}
	if !ready {
		data["status"] = "not_ready"
	} else {
		data["status"] = "ready"
	}
	writeSuccess(w, r, http.StatusOK, data)
}

func (s *Server) rejectNativeManagementWithoutPlan(w http.ResponseWriter) bool {
	if !stackspecadmission.RejectOperationalV1(s.config.Version) {
		return false
	}
	writeArchitectureV2ResolveError(w, http.StatusNotImplemented, &architecturev2.ResolveError{
		Code:    architecturev2.ErrOperationalUnavailable,
		Message: "native Architecture v2 management requires an exact verified ResolvedPlan and execution evidence; the legacy node-local management reader is retired",
	})
	return true
}

func (s *Server) loadWorkspaceSpecSummary() (*managementSpecSummary, string, error) {
	loader := config.NewLoader(s.config.BaseDir)
	path := config.GetDefaultSpecPath()
	specPath := filepath.Join(s.config.BaseDir, path)
	loaded, err := loader.ReadStackSpecDocument(path)
	if err != nil {
		return nil, specPath, err
	}
	if loaded.Document.Version == stackspecmigration.SourceVersionV2Alpha1 {
		service, serviceErr := s.architectureV2ResolveService()
		if serviceErr != nil {
			return nil, specPath, serviceErr
		}
		validation, validationErr := service.ValidateStackSpec(loaded.Document.Raw)
		if validationErr != nil {
			return nil, specPath, validationErr
		}
		return &managementSpecSummary{
			SourceVersion: loaded.Document.Version, StackKit: string(validation.KitProfile),
			SpecHash: validation.SpecHash, ValidationScope: "spec-only",
		}, specPath, nil
	}
	if stackspecadmission.RejectOperationalV1(s.config.Version) {
		service, serviceErr := s.architectureV2ResolveService()
		if serviceErr != nil {
			return nil, specPath, serviceErr
		}
		_, resolveErr := service.Resolve(architecturev2.ResolveInput{StackSpec: loaded.Document.Raw})
		if resolveErr == nil {
			resolveErr = &architecturev2.ResolveError{Code: architecturev2.ErrResolveFailed, Message: "StackSpec v1 unexpectedly entered the Architecture v2 resolver"}
		}
		return nil, specPath, resolveErr
	}
	legacy, err := loader.LoadLegacyStackSpec(path)
	if err != nil {
		return nil, specPath, err
	}
	return &managementSpecSummary{
		SourceVersion: loaded.Document.Version, StackKit: legacy.StackKit, Mode: legacy.Mode,
		Domain: legacy.Domain, ValidationScope: "legacy-operational",
	}, specPath, nil
}

func (s *Server) handleRunEvidence(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	if !isValidRunID(runID) {
		writeError(w, r, http.StatusBadRequest, "invalid run ID")
		return
	}

	runDir, ok := s.runEvidenceDir(runID)
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid run evidence path")
		return
	}
	// #nosec G304 -- runID is timestamp-validated and runDir is constrained under .stackkit/runs.
	if info, err := os.Stat(runDir); err != nil || !info.IsDir() {
		writeError(w, r, http.StatusNotFound, "run evidence not found")
		return
	}

	metadata := readRunJSONFile(runDir, "metadata.json")
	summary := readRunJSONFile(runDir, "summary.json")
	events, err := readRunJSONLines(runDir, "events.jsonl")
	if err != nil && !os.IsNotExist(err) {
		writeError(w, r, http.StatusInternalServerError, "failed to read run evidence")
		return
	}
	writeSuccess(w, r, http.StatusOK, map[string]any{
		"runId":    runID,
		"runDir":   runDir,
		"metadata": metadata,
		"summary":  summary,
		"events":   events,
	})
}

func (s *Server) runEvidenceDir(runID string) (string, bool) {
	root := filepath.Clean(filepath.Join(s.config.BaseDir, ".stackkit", "runs"))
	runDir := filepath.Clean(filepath.Join(root, runID))
	rel, err := filepath.Rel(root, runDir)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return runDir, true
}

func (s *Server) loadWorkspaceSpec() (*models.StackSpec, string, error) {
	loader := config.NewLoader(s.config.BaseDir)
	path := config.GetDefaultSpecPath()
	specPath := filepath.Join(s.config.BaseDir, path)
	loaded, err := loader.ReadStackSpecDocument(path)
	if err != nil {
		return nil, specPath, err
	}
	if loaded.Document.Version == stackspecmigration.SourceVersionV1 && stackspecadmission.RejectOperationalV1(s.config.Version) {
		service, serviceErr := s.architectureV2ResolveService()
		if serviceErr != nil {
			return nil, specPath, serviceErr
		}
		_, resolveErr := service.Resolve(architecturev2.ResolveInput{StackSpec: loaded.Document.Raw})
		if resolveErr == nil {
			resolveErr = &architecturev2.ResolveError{Code: architecturev2.ErrResolveFailed, Message: "StackSpec v1 unexpectedly entered the Architecture v2 resolver"}
		}
		return nil, specPath, resolveErr
	}
	if loaded.Document.Version == stackspecmigration.SourceVersionV2Alpha1 {
		return nil, specPath, &architecturev2.ResolveError{
			Code:    architecturev2.ErrInvalidStackSpec,
			Message: "canonical StackSpec v2 requires the governed Architecture v2 management path; refusing the legacy workspace reader",
		}
	}
	spec, err := loader.LoadLegacyStackSpec(path)
	return spec, specPath, err
}

func (s *Server) loadWorkspaceState() (*models.DeploymentState, string, error) {
	loader := config.NewLoader(s.config.BaseDir)
	path := filepath.Join(".stackkit", "state.yaml")
	statePath := filepath.Join(s.config.BaseDir, path)
	state, err := loader.LoadDeploymentState(path)
	if err != nil {
		return nil, statePath, err
	}
	return state, statePath, nil
}

func (s *Server) loadVerifyAccessSummary() *verify.AccessSummary {
	data, err := os.ReadFile(filepath.Join(s.config.BaseDir, ".stackkit", "access.json"))
	if err != nil {
		return nil
	}
	var access verify.AccessSummary
	if err := json.Unmarshal(data, &access); err != nil {
		return nil
	}
	return &access
}

func (s *Server) logDir() string {
	if strings.TrimSpace(s.config.LogDir) != "" {
		return s.config.LogDir
	}
	if strings.TrimSpace(s.config.BaseDir) == "" {
		return ""
	}
	return filepath.Join(s.config.BaseDir, ".stackkit", "logs")
}

func hasGeneratedTerraform(dir string) bool {
	return len(listTerraformFiles(dir)) > 0
}

func listTerraformFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	files := []string{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".tf" {
			continue
		}
		files = append(files, entry.Name())
	}
	return files
}

func readRunJSONFile(runDir, filename string) map[string]any {
	if filename != filepath.Base(filename) {
		return nil
	}
	path := filepath.Join(runDir, filename)
	// #nosec G304 -- filename is a fixed run-evidence filename under a validated runDir.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{"raw": string(data)}
	}
	return out
}

func readRunJSONLines(runDir, filename string) ([]map[string]any, error) {
	if filename != filepath.Base(filename) {
		return nil, os.ErrInvalid
	}
	path := filepath.Join(runDir, filename)
	// #nosec G304 -- filename is a fixed run-evidence filename under a validated runDir.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	events := []map[string]any{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			event = map[string]any{"raw": line}
		}
		events = append(events, event)
	}
	return events, scanner.Err()
}
