package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kombifyio/stackkits/internal/servicecatalog"
	"github.com/kombifyio/stackkits/internal/testscenarios"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalScenarioGenerationContracts(t *testing.T) {
	scenarios, err := testscenarios.LoadAll()
	require.NoError(t, err)

	for _, scenario := range scenarios {
		if scenario.Expected.Failure.MessageContains != "" {
			continue
		}
		t.Run(scenario.ID, func(t *testing.T) {
			expected := scenario.Expected.Generation
			setCapabilitiesHome(t, models.NodeContext(expected.Context))

			spec := scenario.StackSpec
			vars := decodeTFVars(t, &spec)

			assert.Equal(t, expected.Domain, stringVar(t, vars, "domain"))
			assert.Equal(t, expected.PAAS, stringVar(t, vars, "paas"))
			assert.Equal(t, expected.ReverseProxyBackend, stringVar(t, vars, "reverse_proxy_backend"))
			assert.Equal(t, expected.AdminEmail, stringVar(t, vars, "admin_email"))
			assert.Equal(t, expected.EnableHTTPS, boolVar(t, vars, "enable_https"))
			assert.Equal(t, expected.StepCAEnabled, boolVar(t, vars, "step_ca_enabled"))
			assert.Equal(t, expected.EnableKombifyPoint, boolVar(t, vars, "enable_kombify_point"))
			if expected.ACMEChallenge != "" {
				assert.Equal(t, expected.ACMEChallenge, stringVar(t, vars, "acme_challenge"))
			}
			if expected.DNSProvider != "" {
				assert.Equal(t, expected.DNSProvider, stringVar(t, vars, "dns_provider"))
			}
			for key, want := range expected.ServiceFlags {
				assert.Equal(t, want, boolVar(t, vars, key), key)
			}
		})
	}
}

func TestCanonicalScenarioMissingMailNonInteractiveInitFails(t *testing.T) {
	scenario, err := testscenarios.ByID("SK-S5")
	require.NoError(t, err)

	prevComputeTier := initComputeTier
	prevDomain := initDomain
	prevMode := initMode
	prevForce := initForce
	prevNonInteractive := initNonInteractive
	prevAdminEmail := initAdminEmail
	prevLocalDNS := initLocalDNS
	prevLocalName := initLocalName
	prevContextFlag := contextFlag
	prevSpecFile := specFile
	t.Cleanup(func() {
		initComputeTier = prevComputeTier
		initDomain = prevDomain
		initMode = prevMode
		initForce = prevForce
		initNonInteractive = prevNonInteractive
		initAdminEmail = prevAdminEmail
		initLocalDNS = prevLocalDNS
		initLocalName = prevLocalName
		contextFlag = prevContextFlag
		specFile = prevSpecFile
	})
	t.Setenv("KOMBIFY_USER_EMAIL", "")
	t.Setenv("STACKKIT_ADMIN_EMAIL", "")
	specFile = "stack-spec.yaml"

	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))
	tmpDir, err := os.MkdirTemp(repoRoot, "init-missing-mail-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	_, err = executeCommand(
		"init", "base-kit",
		"--non-interactive",
		"--force",
		"--context", scenario.StackSpec.Context,
		"--compute-tier", scenario.StackSpec.Compute.Tier,
		"--domain", scenario.StackSpec.Domain,
		"--chdir", tmpDir,
	)
	require.Error(t, err)
	if !strings.Contains(err.Error(), scenario.Expected.Failure.MessageContains) {
		t.Fatalf("init error %q does not contain %q", err.Error(), scenario.Expected.Failure.MessageContains)
	}
}

func TestCanonicalScenarioAccessSummaryContracts(t *testing.T) {
	for _, id := range []string{"SK-S1", "SK-S2", "SK-S3"} {
		scenario, err := testscenarios.ByID(id)
		require.NoError(t, err)

		t.Run(id, func(t *testing.T) {
			setCapabilitiesHome(t, models.NodeContext(scenario.Expected.Generation.Context))

			spec := scenario.StackSpec
			vars := decodeTFVars(t, &spec)
			summary := buildAccessSummaryFromInputs(&spec, vars, servicecatalog.Default())

			require.Equal(t, scenario.Expected.Access.HubURL, summary.HubURL)
			services := servicesByAccessKey(summary)
			for _, want := range scenario.Expected.Access.Services {
				got, ok := services[want.Key]
				require.Truef(t, ok, "missing access service %q; got %#v", want.Key, services)
				assert.Equal(t, want.Host, got.Host, want.Key)
				assert.Equal(t, want.Scheme+"://"+want.Host, got.URL, want.Key)
			}
		})
	}
}
