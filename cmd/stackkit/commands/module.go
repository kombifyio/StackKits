package commands

// module.go implements `stackkit module` subcommands for the DB-first
// registry. The `release` command computes a module contract hash from a
// local CUE definition and either writes a release artifact or posts it to
// the Admin API. The `verify-db` command fetches each module's latest
// contract_hash from the Admin API and diffs it against the freshly
// computed hash from local CUE -- used as a CI guard.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	skcue "github.com/kombifyio/stackkits/internal/cue"
	"github.com/spf13/cobra"
)

var (
	moduleReleaseModule     string
	moduleReleaseVersion    string
	moduleReleaseEndpoint   string
	moduleReleaseToken      string
	moduleReleaseOutput     string
	moduleReleaseDryRun     bool
	moduleReleaseReleasedBy string

	moduleVerifyEndpoint string
	moduleVerifyToken    string
	moduleVerifyModule   string
	moduleVerifyAll      bool
	moduleVerifyStrict   bool
	moduleVerifyModules  string
)

var moduleCmd = &cobra.Command{
	Use:   "module",
	Short: "Module registry operations (DB-first, ADR-0010)",
	Long:  "Release modules to the StackKits DB registry and verify parity between local CUE and DB.",
}

var moduleReleaseCmd = &cobra.Command{
	Use:   "release",
	Short: "Compute a module contract hash and optionally publish it to the registry",
	RunE:  runModuleRelease,
}

var moduleVerifyDBCmd = &cobra.Command{
	Use:   "verify-db",
	Short: "Compare the latest DB contract_hash with the freshly computed hash from local CUE",
	RunE:  runModuleVerifyDB,
}

func init() {
	moduleReleaseCmd.Flags().StringVar(&moduleReleaseModule, "module", "", "Path to the module directory (containing module.cue). Required.")
	moduleReleaseCmd.Flags().StringVar(&moduleReleaseVersion, "version", "", "Override metadata.version")
	moduleReleaseCmd.Flags().StringVar(&moduleReleaseEndpoint, "endpoint", "", "Admin API base URL (e.g. https://admin.kombify.io). If unset, only computes hash.")
	moduleReleaseCmd.Flags().StringVar(&moduleReleaseToken, "token", "", "Bearer token for the Admin API. Defaults to $STACKKIT_ADMIN_TOKEN or $KOMBIFY_ADMIN_API_KEY; $SERVICE_AUTH_SECRET takes precedence.")
	moduleReleaseCmd.Flags().StringVar(&moduleReleaseOutput, "output", "", "Write release artifact to this path (JSON). If unset, writes to module-release.json in the module dir.")
	moduleReleaseCmd.Flags().BoolVar(&moduleReleaseDryRun, "dry-run", false, "Compute hash + write artifact but do not POST to API")
	moduleReleaseCmd.Flags().StringVar(&moduleReleaseReleasedBy, "released-by", "", "Identifier of the releaser (user/email/CI ref). Defaults to $USER or 'ci'.")

	moduleVerifyDBCmd.Flags().StringVar(&moduleVerifyEndpoint, "endpoint", "", "Admin API base URL. Required.")
	moduleVerifyDBCmd.Flags().StringVar(&moduleVerifyToken, "token", "", "Bearer token for the Admin API. Defaults to $STACKKIT_ADMIN_TOKEN or $KOMBIFY_ADMIN_API_KEY; $SERVICE_AUTH_SECRET takes precedence.")
	moduleVerifyDBCmd.Flags().StringVar(&moduleVerifyModule, "module", "", "Verify a single module directory")
	moduleVerifyDBCmd.Flags().StringVar(&moduleVerifyModules, "modules-dir", "modules", "Root modules directory (used with --all)")
	moduleVerifyDBCmd.Flags().BoolVar(&moduleVerifyAll, "all", false, "Verify all modules in --modules-dir")
	moduleVerifyDBCmd.Flags().BoolVar(&moduleVerifyStrict, "strict", false, "Exit non-zero on any mismatch (CI mode)")

	moduleCmd.AddCommand(moduleReleaseCmd)
	moduleCmd.AddCommand(moduleVerifyDBCmd)
	rootCmd.AddCommand(moduleCmd)
}

// moduleReleasePayload is what we POST to the Admin API and write to disk.
type moduleReleasePayload struct {
	ModuleSlug        string                 `json:"module_slug"`
	Version           string                 `json:"version"`
	ContractHash      string                 `json:"contract_hash"`
	CueSource         string                 `json:"cue_source"`
	Requires          map[string]interface{} `json:"requires"`
	Provides          map[string]interface{} `json:"provides"`
	Settings          map[string]interface{} `json:"settings"`
	Provisioners      map[string]interface{} `json:"provisioners,omitempty"`
	SupportedContexts []string               `json:"supported_contexts"`
	ReleasedAt        time.Time              `json:"released_at"`
	ReleasedBy        string                 `json:"released_by"`
	Metadata          map[string]interface{} `json:"metadata"`
}

func runModuleRelease(cmd *cobra.Command, args []string) error {
	if moduleReleaseModule == "" {
		return fmt.Errorf("--module is required")
	}
	absPath, err := filepath.Abs(moduleReleaseModule)
	if err != nil {
		return fmt.Errorf("resolve module path: %w", err)
	}
	if _, err := os.Stat(filepath.Join(absPath, "module.cue")); err != nil {
		return fmt.Errorf("module.cue not found in %s", absPath)
	}

	reader := skcue.NewModuleReader()
	contract, err := reader.ReadModule(absPath)
	if err != nil {
		return fmt.Errorf("read module: %w", err)
	}

	cueBytes, err := os.ReadFile(filepath.Join(absPath, "module.cue"))
	if err != nil {
		return fmt.Errorf("read module.cue: %w", err)
	}

	version := moduleReleaseVersion
	if version == "" {
		version = contract.Metadata.Version
	}
	if version == "" {
		return fmt.Errorf("module metadata.version is empty and --version not given")
	}

	slug := contract.Metadata.Name
	if slug == "" {
		slug = filepath.Base(absPath)
	}

	contractMap := moduleContractToCanonicalMap(contract)
	hash, err := skcue.ContractHash(contractMap)
	if err != nil {
		return fmt.Errorf("compute contract hash: %w", err)
	}

	releasedBy := resolveModuleReleaseActor()

	payload := moduleReleasePayload{
		ModuleSlug:        slug,
		Version:           version,
		ContractHash:      hash,
		CueSource:         string(cueBytes),
		Requires:          mapField(contractMap, "requires"),
		Provides:          mapField(contractMap, "provides"),
		Settings:          mapField(contractMap, "settings"),
		Provisioners:      mapField(contractMap, "provisioners"),
		SupportedContexts: []string{"local", "cloud", "pi"},
		ReleasedAt:        time.Now().UTC(),
		ReleasedBy:        releasedBy,
		Metadata: map[string]interface{}{
			"displayName": contract.Metadata.DisplayName,
			"layer":       contract.Metadata.Layer,
			"description": contract.Metadata.Description,
			"core":        contract.Metadata.Core,
		},
	}

	outPath := moduleReleaseOutput
	if outPath == "" {
		outPath = filepath.Join(absPath, "module-release.json")
	}

	artifact, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal release artifact: %w", err)
	}
	if err := os.WriteFile(outPath, artifact, 0o644); err != nil {
		return fmt.Errorf("write release artifact: %w", err)
	}

	printSuccess("Module %s@%s contract_hash=%s", slug, version, hash)
	printInfo("Release artifact: %s", outPath)

	if moduleReleaseDryRun || moduleReleaseEndpoint == "" {
		if moduleReleaseEndpoint == "" {
			printInfo("No --endpoint configured -- stopped after hash computation")
		} else {
			printInfo("--dry-run: skipping API POST")
		}
		return nil
	}

	token := resolveModuleReleaseToken()

	url := fmt.Sprintf("%s/api/v1/sk/registry/modules/%s/versions", trimTrailingSlash(moduleReleaseEndpoint), slug)
	if err := postJSON(url, token, artifact); err != nil {
		return fmt.Errorf("publish release: %w", err)
	}

	printSuccess("Published %s@%s to %s", slug, version, url)
	return nil
}

func resolveModuleReleaseActor() string {
	if moduleReleaseReleasedBy != "" {
		return moduleReleaseReleasedBy
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "ci"
}

func resolveModuleReleaseToken() string {
	if moduleReleaseToken != "" {
		return moduleReleaseToken
	}
	if token := os.Getenv("STACKKIT_ADMIN_TOKEN"); token != "" {
		return token
	}
	return os.Getenv("KOMBIFY_ADMIN_API_KEY")
}

func runModuleVerifyDB(cmd *cobra.Command, args []string) error {
	if moduleVerifyEndpoint == "" {
		return fmt.Errorf("--endpoint is required")
	}
	token := resolveModuleVerifyToken()

	var paths []string
	switch {
	case moduleVerifyAll:
		entries, err := os.ReadDir(moduleVerifyModules)
		if err != nil {
			return fmt.Errorf("read modules dir: %w", err)
		}
		for _, e := range entries {
			if !e.IsDir() || e.Name()[0] == '_' {
				continue
			}
			p := filepath.Join(moduleVerifyModules, e.Name())
			if _, err := os.Stat(filepath.Join(p, "module.cue")); err == nil {
				paths = append(paths, p)
			}
		}
	case moduleVerifyModule != "":
		paths = []string{moduleVerifyModule}
	default:
		return fmt.Errorf("either --module or --all is required")
	}

	reader := skcue.NewModuleReader()
	mismatches := 0
	checked := 0
	for _, p := range paths {
		contract, err := reader.ReadModule(p)
		if err != nil {
			printError("%s: read failed: %v", p, err)
			mismatches++
			continue
		}
		slug := contract.Metadata.Name
		if slug == "" {
			slug = filepath.Base(p)
		}
		localHash, err := skcue.ContractHash(moduleContractToCanonicalMap(contract))
		if err != nil {
			printError("%s: hash failed: %v", slug, err)
			mismatches++
			continue
		}

		dbHash, dbVersion, err := fetchLatestContractHash(trimTrailingSlash(moduleVerifyEndpoint), slug, token)
		if err != nil {
			printWarning("%s: DB lookup failed: %v", slug, err)
			mismatches++
			continue
		}

		checked++
		if dbHash == localHash {
			printSuccess("%s@%s contract_hash=%s (match)", slug, dbVersion, shortHash(localHash))
			continue
		}

		printError("%s: contract_hash mismatch (db=%s local=%s)", slug, shortHash(dbHash), shortHash(localHash))
		mismatches++
	}

	printInfo("checked=%d mismatches=%d", checked, mismatches)
	if mismatches > 0 && moduleVerifyStrict {
		return fmt.Errorf("verify-db: %d mismatches", mismatches)
	}
	return nil
}

func resolveModuleVerifyToken() string {
	if moduleVerifyToken != "" {
		return moduleVerifyToken
	}
	if token := os.Getenv("STACKKIT_ADMIN_TOKEN"); token != "" {
		return token
	}
	return os.Getenv("KOMBIFY_ADMIN_API_KEY")
}

func moduleContractToCanonicalMap(mc skcue.ModuleContract) map[string]interface{} {
	m := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":        mc.Metadata.Name,
			"displayName": mc.Metadata.DisplayName,
			"version":     mc.Metadata.Version,
			"layer":       mc.Metadata.Layer,
			"description": mc.Metadata.Description,
			"core":        mc.Metadata.Core,
		},
	}
	if mc.Requires != nil {
		m["requires"] = requiresMap(mc.Requires)
	}
	if mc.Provides != nil {
		m["provides"] = providesMap(mc.Provides)
	}
	if mc.Settings != nil {
		m["settings"] = settingsMap(mc.Settings)
	}
	if len(mc.Provisioners) > 0 {
		m["provisioners"] = provisionersMap(mc.Provisioners)
	}
	return m
}

func requiresMap(r *skcue.RequiresSpec) map[string]interface{} {
	svcs := map[string]interface{}{}
	for k, v := range r.Services {
		svcs[k] = map[string]interface{}{
			"minVersion": v.MinVersion,
			"provides":   v.Provides,
			"optional":   v.Optional,
		}
	}
	return map[string]interface{}{
		"services": svcs,
		"infrastructure": map[string]interface{}{
			"docker":            r.Infrastructure.Docker,
			"network":           r.Infrastructure.Network,
			"dockerSocket":      r.Infrastructure.DockerSocket,
			"persistentStorage": r.Infrastructure.PersistentStorage,
			"minMemory":         r.Infrastructure.MinMemory,
			"arch":              r.Infrastructure.Arch,
		},
	}
}

func providesMap(p *skcue.ProvidesSpec) map[string]interface{} {
	caps := map[string]interface{}{}
	for k, v := range p.Capabilities {
		caps[k] = v
	}
	mw := map[string]interface{}{}
	for k, v := range p.Middleware {
		mw[k] = map[string]interface{}{
			"type":        v.Type,
			"description": v.Description,
		}
	}
	eps := map[string]interface{}{}
	for k, v := range p.Endpoints {
		eps[k] = map[string]interface{}{
			"url":         v.URL,
			"internal":    v.Internal,
			"description": v.Description,
		}
	}
	return map[string]interface{}{
		"capabilities": caps,
		"middleware":   mw,
		"endpoints":    eps,
	}
}

func settingsMap(s *skcue.SettingsSpec) map[string]interface{} {
	perma := map[string]interface{}{}
	for k, v := range s.Perma {
		perma[k] = v
	}
	flex := map[string]interface{}{}
	for k, v := range s.Flexible {
		flex[k] = v
	}
	return map[string]interface{}{
		"perma":    perma,
		"flexible": flex,
	}
}

func provisionersMap(p map[string]skcue.ProvisionerDef) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range p {
		out[k] = map[string]interface{}{
			"image":       v.Image,
			"command":     v.Command,
			"dependsOn":   v.DependsOn,
			"networks":    v.Networks,
			"environment": v.Environment,
		}
	}
	return out
}

func mapField(m map[string]interface{}, key string) map[string]interface{} {
	if v, ok := m[key]; ok {
		if mm, ok := v.(map[string]interface{}); ok {
			return mm
		}
	}
	return map[string]interface{}{}
}

func postJSON(url, token string, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := attachKitClientAuth(req, token); err != nil {
		return fmt.Errorf("attach auth: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s: status=%d body=%s", url, resp.StatusCode, string(respBody))
	}
	return nil
}

func fetchLatestContractHash(baseURL, slug, token string) (hash, version string, err error) {
	url := fmt.Sprintf("%s/api/v1/sk/registry/modules/%s/versions?latest=true", baseURL, slug)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	if err := attachKitClientAuth(req, token); err != nil {
		return "", "", fmt.Errorf("attach auth: %w", err)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", "", fmt.Errorf("module %s not found", slug)
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("status=%d body=%s", resp.StatusCode, string(body))
	}

	var payload struct {
		Data []struct {
			Version      string `json:"version"`
			ContractHash string `json:"contract_hash"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", err
	}
	if len(payload.Data) == 0 {
		return "", "", fmt.Errorf("no versions for %s", slug)
	}
	return payload.Data[0].ContractHash, payload.Data[0].Version, nil
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
