package commands

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	skcue "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/internal/logging"
	"github.com/kombifyio/stackkits/pkg/models"
)

func TestLogFiltersByPrefixAndLevel(t *testing.T) {
	entries := []logging.LogEntry{
		{Msg: "decision.network", Level: "INFO"},
		{Msg: "spec.loaded", Level: "INFO"},
		{Msg: "apply.failed", Level: "ERROR"},
		{Msg: "apply.warn", Level: "WARN"},
		{Msg: "apply.start", Level: "INFO"},
	}

	decisions := filterByPrefix(entries, "decision.", "spec.loaded")
	if len(decisions) != 2 {
		t.Fatalf("len(decisions) = %d, want 2", len(decisions))
	}
	errors := filterByLevel(entries, "ERROR", "WARN")
	if len(errors) != 2 {
		t.Fatalf("len(errors) = %d, want 2", len(errors))
	}
	if errors[0].Msg != "apply.failed" || errors[1].Msg != "apply.warn" {
		t.Fatalf("unexpected errors = %#v", errors)
	}
}

func TestBuildDoctorReportBaseKitReference(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("key"), 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	report := buildDoctorReport(&models.StackSpec{
		StackKit: "base-kit",
		Context:  string(models.ContextLocal),
		Domain:   models.DomainHomeLab,
		SSH: models.SSHSpec{
			User:    "admin",
			KeyPath: keyPath,
		},
		Services: map[string]any{
			"media":  map[string]any{"enabled": false},
			"photos": map[string]any{"enabled": true},
			"vault":  map[string]any{"enabled": true},
		},
	})

	if report.Status != "pass" {
		t.Fatalf("report status = %q, checks=%#v", report.Status, report.Checks)
	}
	if len(report.Checks) == 0 {
		t.Fatal("expected doctor checks")
	}
}

func TestBuildDoctorReportFailsForRootUserAndDisabledReferenceServices(t *testing.T) {
	report := buildDoctorReport(&models.StackSpec{
		StackKit: "base-kit",
		Context:  string(models.ContextLocal),
		Domain:   models.DomainHomeLab,
		SSH: models.SSHSpec{
			User:    "root",
			KeyPath: "",
		},
		Services: map[string]any{
			"photos": map[string]any{"enabled": false},
			"vault":  map[string]any{"enabled": false},
		},
	})

	if report.Status != "fail" {
		t.Fatalf("report status = %q, want fail", report.Status)
	}
	assertDoctorCheck(t, report, "ssh-non-root", "fail")
	assertDoctorCheck(t, report, "photos", "fail")
	assertDoctorCheck(t, report, "vault", "fail")
}

func TestServiceEnabledValueFallbacks(t *testing.T) {
	services := map[string]any{
		"explicit": map[string]any{"enabled": false},
		"bad":      "not-a-map",
		"missing":  map[string]any{"other": true},
	}

	if serviceEnabledValue(services, "explicit", true) {
		t.Fatal("explicit disabled service should be false")
	}
	if !serviceEnabledValue(services, "bad", true) {
		t.Fatal("non-map service should use fallback")
	}
	if !serviceEnabledValue(services, "missing", true) {
		t.Fatal("missing enabled field should use fallback")
	}
	if serviceEnabledValue(nil, "anything", false) {
		t.Fatal("nil services should use false fallback")
	}
}

func TestParseAddonMetadataAndDiscovery(t *testing.T) {
	root := t.TempDir()
	addonDir := filepath.Join(root, "addons", "backup")
	if err := os.MkdirAll(addonDir, 0750); err != nil {
		t.Fatalf("mkdir addon: %v", err)
	}
	addonCue := filepath.Join(addonDir, "addon.cue")
	if err := os.WriteFile(addonCue, []byte(`
package backup

_addon: {
  name: "backup"
  displayName: "Backup"
  layer: "L3"
  description: "Back up application data"
}
`), 0600); err != nil {
		t.Fatalf("write addon: %v", err)
	}

	info := parseAddonMetadata(addonCue, "fallback")
	if info.Name != "backup" || info.DisplayName != "Backup" || info.Layer != "L3" {
		t.Fatalf("metadata = %#v", info)
	}
	if info.Description != "Back up application data" {
		t.Fatalf("description = %q", info.Description)
	}

	addons, err := discoverAddons(root)
	if err != nil {
		t.Fatalf("discoverAddons: %v", err)
	}
	if len(addons) != 1 || addons[0].Name != "backup" {
		t.Fatalf("addons = %#v", addons)
	}
}

func TestExtractCUEFieldTrimsQuotesAndMissingField(t *testing.T) {
	content := `name: "backup"
layer: "L3"`
	if got := extractCUEField(content, "name"); got != "backup" {
		t.Fatalf("name = %q", got)
	}
	if got := extractCUEField(content, "missing"); got != "" {
		t.Fatalf("missing = %q", got)
	}
}

func TestNormalizeYAMLConvertsNestedMapsAndSlices(t *testing.T) {
	input := map[interface{}]interface{}{
		"metadata": map[interface{}]interface{}{
			"name": "base-kit",
			123:    "numeric-key",
		},
		"items": []interface{}{
			map[interface{}]interface{}{"name": "one"},
		},
	}

	got := normalizeYAML(input)
	metadata, ok := got["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("metadata type = %T", got["metadata"])
	}
	if metadata["123"] != "numeric-key" {
		t.Fatalf("numeric key was not stringified: %#v", metadata)
	}
	items, ok := got["items"].([]interface{})
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v", got["items"])
	}
	if _, ok := items[0].(map[string]interface{}); !ok {
		t.Fatalf("nested item type = %T", items[0])
	}
}

func TestModuleContractToCanonicalMapCopiesRequiredFields(t *testing.T) {
	contract := skcue.ModuleContract{
		Metadata: skcue.ModuleMetadata{
			Name:        "tinyauth",
			DisplayName: "TinyAuth",
			Version:     "1.2.3",
			Layer:       "L1",
			Description: "Forward auth",
			Core:        true,
		},
		Requires: &skcue.RequiresSpec{
			Services: map[string]skcue.RequiredService{
				"traefik": {MinVersion: "1.0.0", Provides: []string{"router"}, Optional: false},
			},
			Infrastructure: skcue.InfraRequirements{
				Docker:            true,
				Network:           "frontend",
				DockerSocket:      true,
				PersistentStorage: true,
				MinMemory:         "256Mi",
				Arch:              "amd64",
			},
		},
		Provides: &skcue.ProvidesSpec{
			Capabilities: map[string]bool{"auth": true},
			Middleware: map[string]skcue.MiddlewareDef{
				"forwardauth": {Type: "forwardAuth", Description: "Auth middleware"},
			},
			Endpoints: map[string]skcue.EndpointDef{
				"auth": {URL: "https://auth.example", Internal: false, Description: "Login"},
			},
		},
		Settings: &skcue.SettingsSpec{
			Perma:    map[string]any{"image": "tinyauth"},
			Flexible: map[string]any{"replicas": 1},
		},
		Provisioners: map[string]skcue.ProvisionerDef{
			"init-tinyauth": {
				Image:       "python:3.11-alpine",
				Command:     "python3 bootstrap.py",
				DependsOn:   "tinyauth",
				Networks:    []string{"base_net"},
				Environment: map[string]string{"AUTH_URL": "http://tinyauth:3000"},
			},
		},
	}

	got := moduleContractToCanonicalMap(contract)
	metadata := got["metadata"].(map[string]interface{})
	if metadata["name"] != "tinyauth" || metadata["core"] != true {
		t.Fatalf("metadata = %#v", metadata)
	}
	if mapField(got, "requires")["services"] == nil {
		t.Fatalf("requires services missing: %#v", got)
	}
	if mapField(got, "provides")["middleware"] == nil {
		t.Fatalf("provides middleware missing: %#v", got)
	}
	if mapField(got, "settings")["perma"] == nil {
		t.Fatalf("settings perma missing: %#v", got)
	}
	provisioners := mapField(got, "provisioners")
	initProvisioner := mapField(provisioners, "init-tinyauth")
	if initProvisioner["command"] != "python3 bootstrap.py" {
		t.Fatalf("provisioners missing command: %#v", got)
	}
}

func TestPostJSONAndFetchLatestContractHash(t *testing.T) {
	var postedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			postedAuth = r.Header.Get("Authorization")
			if r.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("Content-Type = %q", r.Header.Get("Content-Type"))
			}
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			postedAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"version": "1.0.0", "contract_hash": "abcdef1234567890"}},
			})
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer srv.Close()

	if err := postJSON(srv.URL+"/modules", "tok", []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("postJSON: %v", err)
	}
	if postedAuth != "Bearer tok" {
		t.Fatalf("POST Authorization = %q", postedAuth)
	}

	hash, version, err := fetchLatestContractHash(srv.URL, "tinyauth", "tok")
	if err != nil {
		t.Fatalf("fetchLatestContractHash: %v", err)
	}
	if hash != "abcdef1234567890" || version != "1.0.0" {
		t.Fatalf("hash/version = %q/%q", hash, version)
	}
	if postedAuth != "Bearer tok" {
		t.Fatalf("GET Authorization = %q", postedAuth)
	}
}

func TestPostJSONAndFetchLatestContractHashErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "broken", http.StatusBadGateway)
	}))
	defer srv.Close()

	if err := postJSON(srv.URL, "", []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "status=502") {
		t.Fatalf("postJSON error = %v", err)
	}
	if _, _, err := fetchLatestContractHash(srv.URL, "missing", ""); err == nil || !strings.Contains(err.Error(), "status=502") {
		t.Fatalf("fetchLatestContractHash error = %v", err)
	}
}

func TestShortHashAndTrimTrailingSlash(t *testing.T) {
	if got := shortHash("1234567890abcdef"); got != "1234567890ab" {
		t.Fatalf("shortHash long = %q", got)
	}
	if got := shortHash("short"); got != "short" {
		t.Fatalf("shortHash short = %q", got)
	}
	if got := trimTrailingSlash("https://example.test///"); got != "https://example.test" {
		t.Fatalf("trimTrailingSlash = %q", got)
	}
}

func TestPrintDoctorReportWritesStatusAndChecks(t *testing.T) {
	var buf bytes.Buffer
	cmd := rootCmd
	cmd.SetOut(&buf)
	t.Cleanup(func() { cmd.SetOut(os.Stdout) })

	printDoctorReport(cmd, doctorReport{
		Status: "warn",
		Gate:   "fresh-ubuntu-local",
		Checks: []doctorCheck{{
			Name:    "spec",
			Status:  "pass",
			Message: "stack spec loaded",
		}},
	})

	got := buf.String()
	if !strings.Contains(got, "Doctor: warn") || !strings.Contains(got, "- spec: pass") {
		t.Fatalf("doctor output = %q", got)
	}
}

func TestRunLogsListWithNoLogs(t *testing.T) {
	prevWorkDir := workDir
	prevQuiet := quiet
	workDir = t.TempDir()
	quiet = false
	t.Cleanup(func() {
		workDir = prevWorkDir
		quiet = prevQuiet
	})

	if err := runLogsList(rootCmd, nil); err != nil {
		t.Fatalf("runLogsList: %v", err)
	}
}

func assertDoctorCheck(t *testing.T, report doctorReport, name, status string) {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			if check.Status != status {
				t.Fatalf("%s status = %q, want %q", name, check.Status, status)
			}
			return
		}
	}
	t.Fatalf("doctor check %q not found in %#v", name, report.Checks)
}
