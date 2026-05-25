package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/kitio"
	"github.com/kombifyio/stackkits/internal/servicecatalog"
	"github.com/kombifyio/stackkits/pkg/models"
)

func TestBuildAccessSummaryFromInputs(t *testing.T) {
	spec := &models.StackSpec{
		StackKit:        "base-kit",
		Mode:            "local",
		Domain:          "example.test",
		SubdomainPrefix: "lab",
	}
	tfvars := map[string]any{
		"enable_https":   true,
		"enable_traefik": true,
		"enable_whoami":  false,
	}
	catalog := []servicecatalog.Service{
		{
			Key:         "base",
			Name:        "dashboard",
			DisplayName: "Dashboard",
			ToolName:    "dashboard-tool",
			ModuleSlug:  "dashboard-module",
			PublicSlug:  "dash",
			LocalSlug:   "base",
			Default:     true,
		},
		{
			Key:         "traefik",
			DisplayName: "Traefik",
			PublicSlug:  "edge",
			Default:     false,
		},
		{
			Key:         "whoami",
			DisplayName: "Whoami",
			Default:     true,
		},
	}

	summary := buildAccessSummaryFromInputs(spec, tfvars, catalog)
	if summary.StackKit != "base-kit" || summary.Domain != "example.test" || summary.Mode != "local" {
		t.Fatalf("unexpected summary identity: %+v", summary)
	}
	if summary.HubURL != "https://lab-dash.example.test" {
		t.Fatalf("unexpected hub url: %q", summary.HubURL)
	}
	if len(summary.Services) != 2 {
		t.Fatalf("expected disabled whoami to be omitted, got %d services", len(summary.Services))
	}
	if summary.Services[0].Host != "lab-dash.example.test" || summary.Services[0].RouteSlug != "dash" {
		t.Fatalf("unexpected base route: %+v", summary.Services[0])
	}
	if summary.Services[1].Key != "traefik" || summary.Services[1].URL != "https://lab-edge.example.test" {
		t.Fatalf("unexpected traefik route: %+v", summary.Services[1])
	}
}

func TestAccessSummaryHelpers(t *testing.T) {
	for key, want := range map[string]string{
		"base":    "enable_dashboard",
		"home":    "enable_homepage",
		"id":      "enable_pocketid",
		"vault":   "enable_vaultwarden",
		"photos":  "enable_immich",
		"unknown": "",
	} {
		if got := defaultEnableVar(key); got != want {
			t.Fatalf("defaultEnableVar(%q) = %q, want %q", key, got, want)
		}
	}

	entry := servicecatalog.Service{Key: "id", PublicSlug: "identity", LocalSlug: "pocketid", Default: true}
	if !entryEnabled(entry, nil) {
		t.Fatal("entry should use default when tfvars are nil")
	}
	if !entryEnabled(entry, map[string]any{"enable_pocketid": true}) {
		t.Fatal("entry should respect default enable var")
	}
	if entryEnabled(entry, map[string]any{"enable_pocketid": false}) {
		t.Fatal("entry should be disabled by tfvars")
	}
	if got := hostForEntry(entry, "example.test", "lab"); got != "lab-identity.example.test" {
		t.Fatalf("unexpected public host: %q", got)
	}
	if got := hostForEntry(entry, "home.arpa", ""); got != "pocketid.home.arpa" {
		t.Fatalf("unexpected local host: %q", got)
	}
	if got := routeSlugForEntry(entry, "lab"); got != "identity" {
		t.Fatalf("unexpected public route slug: %q", got)
	}
	if got := firstHostLabel("dash.example.test"); got != "dash" {
		t.Fatalf("unexpected first host label: %q", got)
	}

	summary := &accessSummary{Services: []accessService{{
		Key:           "id",
		Name:          "PocketID",
		ToolName:      "pocket-id",
		ModuleSlug:    "pocketid",
		RouteSlug:     "identity",
		Host:          "id.home.arpa",
		URL:           "https://id.home.arpa",
		LegacyAliases: []string{"authn"},
	}}}
	aliases := urlAliases(summary)
	for _, alias := range []string{"id", "pocketid", "pocket-id", "identity", "authn"} {
		if aliases[alias] != "https://id.home.arpa" {
			t.Fatalf("missing alias %q in %#v", alias, aliases)
		}
	}

	states := serviceStatesFromAccessSummary(summary)
	if len(states) != 1 || states[0].Name != "id" || states[0].Status != models.ServiceStatusRunning {
		t.Fatalf("unexpected service states: %+v", states)
	}
}

func TestLoadAndWriteAccessSummary(t *testing.T) {
	tmp := t.TempDir()
	tfvarsDir := filepath.Join(tmp, config.GetDeployDir())
	if err := os.MkdirAll(tfvarsDir, 0750); err != nil {
		t.Fatal(err)
	}
	tfvarsPath := filepath.Join(tfvarsDir, "terraform.tfvars.json")
	if err := os.WriteFile(tfvarsPath, []byte(`{"domain":"example.test","enable_https":true}`), 0600); err != nil {
		t.Fatal(err)
	}

	tfvars, err := loadGeneratedTFVars(tmp)
	if err != nil {
		t.Fatalf("loadGeneratedTFVars returned error: %v", err)
	}
	if tfvars["domain"] != "example.test" || tfvars["enable_https"] != true {
		t.Fatalf("unexpected tfvars: %#v", tfvars)
	}

	summary := &accessSummary{StackKit: "base-kit", Domain: "example.test"}
	if err := writeAccessSummary(tmp, summary); err != nil {
		t.Fatalf("writeAccessSummary returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, ".stackkit", "access.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded accessSummary
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("access summary is not json: %v", err)
	}
	if decoded.StackKit != "base-kit" || decoded.Domain != "example.test" {
		t.Fatalf("unexpected decoded summary: %+v", decoded)
	}
}

func TestPrimaryServiceProbeTargetUsesAccessSummaryHubURL(t *testing.T) {
	access := &accessSummary{
		HubURL: "http://base.home.localhost",
	}

	host, rawURL := primaryServiceProbeTarget(&models.StackSpec{Domain: models.DomainHomeLab}, access)
	if host != "base.home.localhost" || rawURL != "http://base.home.localhost" {
		t.Fatalf("primaryServiceProbeTarget local = (%q, %q), want base.home.localhost/http", host, rawURL)
	}

	access.HubURL = "https://sh-my-homelab-abc123-base.kombify.me"
	host, rawURL = primaryServiceProbeTarget(&models.StackSpec{Domain: models.DomainKombifyMe}, access)
	if host != "sh-my-homelab-abc123-base.kombify.me" || rawURL != access.HubURL {
		t.Fatalf("primaryServiceProbeTarget kombify.me = (%q, %q), want access hub URL", host, rawURL)
	}
}

func TestPrimaryServiceProbeTargetFallbackMatchesDomainMode(t *testing.T) {
	cases := []struct {
		name string
		spec *models.StackSpec
		host string
		url  string
	}{
		{
			name: "local browser-native localhost HTTP",
			spec: &models.StackSpec{Domain: models.DomainHomeLab},
			host: "base.home.localhost",
			url:  "http://base.home.localhost",
		},
		{
			name: "explicit local DNS Step-CA HTTPS",
			spec: &models.StackSpec{Domain: models.DomainStackHome},
			host: "base.stack.home",
			url:  "https://base.stack.home",
		},
		{
			name: "kombify.me flat HTTPS",
			spec: &models.StackSpec{Domain: models.DomainKombifyMe, SubdomainPrefix: "sh-my-homelab-abc123"},
			host: "sh-my-homelab-abc123-base.kombify.me",
			url:  "https://sh-my-homelab-abc123-base.kombify.me",
		},
		{
			name: "custom domain HTTPS",
			spec: &models.StackSpec{Domain: "example.com"},
			host: "base.example.com",
			url:  "https://base.example.com",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			host, rawURL := primaryServiceProbeTarget(tt.spec, nil)
			if host != tt.host || rawURL != tt.url {
				t.Fatalf("primaryServiceProbeTarget = (%q, %q), want (%q, %q)", host, rawURL, tt.host, tt.url)
			}
		})
	}
}

func TestApplyTroubleshootingPureHelpers(t *testing.T) {
	patterns := knownFailurePatterns()
	if len(patterns) != 4 {
		t.Fatalf("unexpected pattern count: %d", len(patterns))
	}
	cases := map[string]string{
		"docker-image-pull": "Error pulling image alpine in docker_image.foo",
		"docker-network":    "docker_network.default: Unable to create network: operation not permitted",
		"docker-daemon":     "Cannot connect to the Docker daemon at unix:///var/run/docker.sock",
		"state-lock":        "Error acquiring the state lock",
	}
	for _, pattern := range patterns {
		input := cases[pattern.Name]
		if input == "" {
			t.Fatalf("missing fixture for pattern %q", pattern.Name)
		}
		if !pattern.Match(input) {
			t.Fatalf("pattern %q did not match %q", pattern.Name, input)
		}
		if pattern.Match("unrelated stderr") {
			t.Fatalf("pattern %q matched unrelated stderr", pattern.Name)
		}
	}

	for input, want := range map[string]string{
		"toomanyrequests from registry":                             "Container registry rate limit reached.",
		"Reference to undeclared resource docker_container.missing": "Generated OpenTofu configuration references a missing resource.",
		"│ Error: invalid value":                                    "Deployment error: Error: invalid value",
		"plain failure":                                             "Deployment failed. Run 'stackkit prepare' then retry with 'stackkit apply'.",
	} {
		if got := formatApplyError(input); !strings.HasPrefix(got, want) {
			t.Fatalf("formatApplyError(%q) = %q, want prefix %q", input, got, want)
		}
	}
}

func TestPatchTfvarsNetworkMode(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "terraform.tfvars.json")
	if err := os.WriteFile(path, []byte(`{"domain":"example.test","network_mode":"bridge"}`), 0600); err != nil {
		t.Fatal(err)
	}

	if err := patchTfvarsNetworkMode(tmp, "host"); err != nil {
		t.Fatalf("patchTfvarsNetworkMode returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var vars map[string]any
	if err := json.Unmarshal(data, &vars); err != nil {
		t.Fatalf("patched tfvars are invalid json: %v", err)
	}
	if vars["network_mode"] != "host" || vars["domain"] != "example.test" {
		t.Fatalf("unexpected patched tfvars: %#v", vars)
	}
}

func TestPrepareAndCompatibilityPureHelpers(t *testing.T) {
	if boolToStatus(true) != "available" || boolToStatus(false) != "unavailable" {
		t.Fatal("boolToStatus returned unexpected values")
	}
	if got := pocketIDURLForSpec(&models.StackSpec{Domain: " example.com "}); got != "https://id.example.com" {
		t.Fatalf("unexpected PocketID URL: %q", got)
	}
	if got := pocketIDURLForSpec(&models.StackSpec{Domain: models.DomainHomeLab}); got != "http://id.home.localhost" {
		t.Fatalf("unexpected PocketID URL: %q", got)
	}
	if got := pocketIDURLForSpec(&models.StackSpec{Domain: models.DomainStackHome}); got != "https://id.stack.home" {
		t.Fatalf("unexpected PocketID URL: %q", got)
	}
	if got := pocketIDURLForSpec(&models.StackSpec{}); got != "" {
		t.Fatalf("expected empty PocketID URL for missing domain, got %q", got)
	}
	if got := firstLine(" first line \n second line "); got != "first line" {
		t.Fatalf("unexpected firstLine result: %q", got)
	}
	if got := firstLine("single"); got != "single" {
		t.Fatalf("unexpected single-line result: %q", got)
	}

	if got := classifyCompatibilityTier(models.VirtKVM, true, true, true); got != models.TierFull {
		t.Fatalf("unexpected KVM full tier: %s", got)
	}
	if got := classifyCompatibilityTier(models.VirtLXC, true, false, true); got != models.TierDegraded {
		t.Fatalf("unexpected LXC degraded tier: %s", got)
	}
	if got := classifyCompatibilityTier(models.VirtOpenVZ, false, true, true); got != models.TierIncompatible {
		t.Fatalf("unexpected OpenVZ incompatible tier: %s", got)
	}

	avail, total, mount := parseDfOutput("Avail Size Mounted on\n1073741824 2147483648 /\n")
	if avail != 1 || total != 2 || mount != "/" {
		t.Fatalf("unexpected parsed df output: avail=%v total=%v mount=%q", avail, total, mount)
	}
	if a, total, mount := parseDfOutput("bad\n"); a != 0 || total != 0 || mount != "" {
		t.Fatalf("expected malformed df output to parse as zeroes, got %v %v %q", a, total, mount)
	}
	if !isNoSpaceError("write layer: no space left on device") || isNoSpaceError("permission denied") {
		t.Fatal("isNoSpaceError returned unexpected result")
	}
}

func TestBaseKitImagesByTier(t *testing.T) {
	t.Setenv("STACKKIT_SERVICE_PROFILE", "")

	low := baseKitImages(models.ComputeTierLow)
	if slices.Contains(low, "louislam/dockge:1") || slices.Contains(low, "dokploy/dokploy:latest") {
		t.Fatalf("low tier image set is wrong: %#v", low)
	}

	standard := baseKitImages(models.ComputeTierStandard)
	if slices.Contains(standard, "dokploy/dokploy:latest") || slices.Contains(standard, "louislam/dockge:1") {
		t.Fatalf("standard tier image set is wrong: %#v", standard)
	}
	for _, image := range []string{
		"ghcr.io/gethomepage/homepage:latest",
		"ghcr.io/tecnativa/docker-socket-proxy:latest",
		"public.ecr.aws/docker/library/postgres:15-alpine",
		"public.ecr.aws/docker/library/redis:7-alpine",
		"ghcr.io/coollabsio/coolify-helper:1.0.13",
		"ghcr.io/coollabsio/coolify-realtime:1.0.13",
		"ghcr.io/coollabsio/coolify:4.0.0",
		"ghcr.io/dani-garcia/vaultwarden:latest",
		"ghcr.io/immich-app/immich-server:release",
		"ghcr.io/immich-app/immich-machine-learning:release",
		"ghcr.io/immich-app/postgres:16-vectorchord0.3.0-pgvectors0.3.0",
		"ghcr.io/valkey-io/valkey:9",
	} {
		if !slices.Contains(standard, image) {
			t.Fatalf("standard image set missing %s: %#v", image, standard)
		}
	}

	all := baseKitImages("")
	if !slices.Contains(all, "dokploy/dokploy:latest") || !slices.Contains(all, "louislam/dockge:1") {
		t.Fatalf("all image set should include standard and low images: %#v", all)
	}

	t.Setenv("STACKKIT_SERVER_IMAGE", "stackkit-server:local")
	if slices.Contains(baseKitImages(models.ComputeTierStandard), "stackkit-server:local") {
		t.Fatalf("local stackkit-server image should not be pre-pulled")
	}
	t.Setenv("STACKKIT_SERVER_IMAGE", "ghcr.io/kombifyio/stackkits:latest")
	if !slices.Contains(baseKitImages(models.ComputeTierStandard), "ghcr.io/kombifyio/stackkits:latest") {
		t.Fatalf("remote stackkit-server image should be pre-pulled")
	}
}

func TestBaseKitImagesUsesSelectedPlatform(t *testing.T) {
	t.Setenv("STACKKIT_SERVICE_PROFILE", "")
	t.Setenv("STACKKIT_PLATFORM", "komodo")

	images := baseKitImages(models.ComputeTierStandard)
	for _, image := range []string{
		"public.ecr.aws/docker/library/mongo:7",
		"ghcr.io/moghtech/komodo-core:2",
		"ghcr.io/moghtech/komodo-periphery:2",
		"smallstep/step-ca:0.30.2",
	} {
		if !slices.Contains(images, image) {
			t.Fatalf("komodo image set missing %s: %#v", image, images)
		}
	}
	for _, image := range []string{
		"ghcr.io/coollabsio/coolify:4.0.0",
		"ghcr.io/coollabsio/coolify-helper:1.0.13",
		"ghcr.io/coollabsio/coolify-realtime:1.0.13",
	} {
		if slices.Contains(images, image) {
			t.Fatalf("komodo image set should not pre-pull Coolify image %s: %#v", image, images)
		}
	}
}

func TestPreparePrePullComputeTierDefaultsToStandard(t *testing.T) {
	if got := preparePrePullComputeTier(nil); got != models.ComputeTierStandard {
		t.Fatalf("nil spec pre-pull tier = %q, want %q", got, models.ComputeTierStandard)
	}

	if got := preparePrePullComputeTier(&models.StackSpec{}); got != models.ComputeTierStandard {
		t.Fatalf("empty spec pre-pull tier = %q, want %q", got, models.ComputeTierStandard)
	}

	lowSpec := &models.StackSpec{}
	lowSpec.Compute.Tier = models.ComputeTierLow
	if got := preparePrePullComputeTier(lowSpec); got != models.ComputeTierLow {
		t.Fatalf("explicit low pre-pull tier = %q, want %q", got, models.ComputeTierLow)
	}

	images := baseKitImages(preparePrePullComputeTier(nil))
	if slices.Contains(images, "dokploy/dokploy:latest") || slices.Contains(images, "louislam/dockge:1") {
		t.Fatalf("prepare without a spec must use the standard default image set, got %#v", images)
	}
}

func TestShouldPrePullImagesHonorsEnv(t *testing.T) {
	t.Setenv("STACKKIT_PREPULL_IMAGES", "")
	t.Setenv("STACKKIT_IMAGE_PREPULL", "")
	if !shouldPrePullImages() {
		t.Fatal("image pre-pull should default to enabled")
	}

	t.Setenv("STACKKIT_PREPULL_IMAGES", "false")
	if shouldPrePullImages() {
		t.Fatal("STACKKIT_PREPULL_IMAGES=false should disable image pre-pull")
	}

	t.Setenv("STACKKIT_PREPULL_IMAGES", "")
	t.Setenv("STACKKIT_IMAGE_PREPULL", "0")
	if shouldPrePullImages() {
		t.Fatal("legacy STACKKIT_IMAGE_PREPULL=0 should disable image pre-pull")
	}
}

func TestBaseKitImagesAdminOnlyProfile(t *testing.T) {
	t.Setenv("STACKKIT_SERVICE_PROFILE", "admin-only")

	images := baseKitImages("")
	for _, image := range []string{
		"ghcr.io/gethomepage/homepage:latest",
		"ghcr.io/tecnativa/docker-socket-proxy:latest",
		"public.ecr.aws/docker/library/postgres:15-alpine",
		"public.ecr.aws/docker/library/redis:7-alpine",
		"ghcr.io/coollabsio/coolify-helper:1.0.13",
		"ghcr.io/coollabsio/coolify-realtime:1.0.13",
		"ghcr.io/coollabsio/coolify:4.0.0",
		"ghcr.io/louislam/uptime-kuma:2.0.2",
		"ghcr.io/traefik/whoami:latest",
	} {
		if !slices.Contains(images, image) {
			t.Fatalf("admin-only image set missing %s: %#v", image, images)
		}
	}
	for _, image := range []string{
		"dokploy/dokploy:latest",
		"louislam/dockge:1",
		"ghcr.io/dani-garcia/vaultwarden:latest",
		"ghcr.io/immich-app/immich-server:release",
		"ghcr.io/immich-app/immich-machine-learning:release",
		"ghcr.io/immich-app/postgres:16-vectorchord0.3.0-pgvectors0.3.0",
		"ghcr.io/valkey-io/valkey:9",
	} {
		if slices.Contains(images, image) {
			t.Fatalf("admin-only image set should not include %s: %#v", image, images)
		}
	}
}

func TestPackageManagerLockWaitScriptCoversAptLocks(t *testing.T) {
	script := packageManagerLockWaitScript()
	for _, want := range []string{
		"apt-get",
		"/var/lib/dpkg/lock-frontend",
		"unattended-upgr",
		"Timed out waiting for apt/dpkg lock",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("packageManagerLockWaitScript missing %q in:\n%s", want, script)
		}
	}
}

func TestKitFormattingHelpers(t *testing.T) {
	if got := formatTS("2026-05-07T08:09:10.123456789+02:00"); got != "2026-05-07 06:09:10Z" {
		t.Fatalf("unexpected formatted timestamp: %q", got)
	}
	if got := formatTS("not-a-time"); got != "not-a-time" {
		t.Fatalf("invalid timestamp should pass through, got %q", got)
	}
	if boolDelta(true, true) == "" || boolDelta(false, false) == "" || boolDelta(true, false) == "" || boolDelta(false, true) == "" {
		t.Fatal("boolDelta should return a visible marker for every state transition")
	}
	if got := hashDelta("1234567890", "abcdef1234"); got != "12345678→abcdef12" {
		t.Fatalf("unexpected hash delta: %q", got)
	}
	if got := hashDelta("", ""); got != "(empty)" {
		t.Fatalf("unexpected empty hash delta: %q", got)
	}
}

func TestRoundtripDiffHelpers(t *testing.T) {
	cosmetic := []kitio.FieldDifference{{Severity: "cosmetic"}}
	critical := []kitio.FieldDifference{{Severity: "cosmetic"}, {Severity: "critical"}}

	if !onlyCosmetic(nil) || !onlyCosmetic(cosmetic) {
		t.Fatal("cosmetic-only diffs should pass")
	}
	if onlyCosmetic(critical) {
		t.Fatal("critical diff should fail onlyCosmetic")
	}
	if got := criticalCount(critical); got != 1 {
		t.Fatalf("unexpected critical count: %d", got)
	}
}
