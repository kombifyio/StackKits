package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/logging"
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

func (s *Server) handleManagementStatus(w http.ResponseWriter, r *http.Request) {
	spec, specPath, specErr := s.loadWorkspaceSpec()
	state, statePath, stateErr := s.loadWorkspaceState()
	latestRunID := ""
	if s.logDir() != "" {
		if files, err := logging.ListLogFiles(s.logDir()); err == nil && len(files) > 0 {
			latestRunID = strings.TrimSuffix(files[len(files)-1], ".jsonl")
		}
	}

	data := map[string]any{
		"status":         "ok",
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
		data["mode"] = spec.Mode
		data["domain"] = spec.Domain
	}
	if specErr != nil {
		data["specError"] = specErr.Error()
	}
	if state != nil {
		data["deploymentStatus"] = state.Status
		data["lastApplied"] = state.LastApplied
	}
	if stateErr != nil {
		data["stateError"] = stateErr.Error()
	}
	writeSuccess(w, r, http.StatusOK, data)
}

func (s *Server) handleManagementVerify(w http.ResponseWriter, r *http.Request) {
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

	spec, _, _ := s.loadWorkspaceSpec()
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
		if spec.StackKit == "base-kit" {
			add("release-kit", "pass", spec.StackKit, "BaseKit is the release-ready one-click path")
		} else {
			add("release-kit", "warn", spec.StackKit, "Modern Homelab and HA Kit are alpha/scaffolding until their matrices graduate")
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

func (s *Server) handleRunEvidence(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	if !isValidRunID(runID) {
		writeError(w, r, http.StatusBadRequest, "invalid run ID")
		return
	}

	runDir := filepath.Join(s.config.BaseDir, ".stackkit", "runs", runID)
	if info, err := os.Stat(runDir); err != nil || !info.IsDir() {
		writeError(w, r, http.StatusNotFound, "run evidence not found")
		return
	}

	metadata := readJSONFile(filepath.Join(runDir, "metadata.json"))
	summary := readJSONFile(filepath.Join(runDir, "summary.json"))
	events, err := readJSONLines(filepath.Join(runDir, "events.jsonl"))
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

func (s *Server) loadWorkspaceSpec() (*models.StackSpec, string, error) {
	loader := config.NewLoader(s.config.BaseDir)
	path := config.GetDefaultSpecPath()
	specPath := filepath.Join(s.config.BaseDir, path)
	spec, err := loader.LoadStackSpec(path)
	if err != nil {
		return nil, specPath, err
	}
	return spec, specPath, nil
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

func readJSONFile(path string) map[string]any {
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

func readJSONLines(path string) ([]map[string]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
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
