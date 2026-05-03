package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/kombifyio/stackkits/internal/composition"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// executeCommand runs the root command with the given args and captures
// cobra-buffered output. Commands that write directly to os.Stdout (e.g.
// version, completion) won't appear in the returned string; use
// executeCommandCaptureStdout for those.
func executeCommand(args ...string) (string, error) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	// Close deploy logger to release file handles (PostRun skips on error)
	if deployLog != nil {
		deployLog.Close()
		deployLog = nil
	}
	return buf.String(), err
}

// executeCommandCaptureStdout redirects os.Stdout so that commands using
// fmt.Printf / os.Stdout writes are captured. A goroutine drains the pipe
// concurrently to avoid blocking on Windows when output is large.
func executeCommandCaptureStdout(args ...string) (string, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = buf.ReadFrom(r)
		close(done)
	}()

	orig := os.Stdout
	os.Stdout = w

	rootCmd.SetArgs(args)
	execErr := rootCmd.Execute()
	// Close deploy logger to release file handles (PostRun skips on error)
	if deployLog != nil {
		deployLog.Close()
		deployLog = nil
	}

	_ = w.Close()
	os.Stdout = orig
	<-done
	_ = r.Close()

	return buf.String(), execErr
}

func TestRootCommand_SubcommandsRegistered(t *testing.T) {
	expected := []string{
		"init", "prepare", "generate", "validate",
		"plan", "apply", "remove", "status",
		"version", "completion",
	}

	registered := make(map[string]bool)
	for _, cmd := range rootCmd.Commands() {
		registered[cmd.Name()] = true
	}

	for _, name := range expected {
		assert.True(t, registered[name], "subcommand %q should be registered", name)
	}
}

func TestRootCommand_GlobalFlags(t *testing.T) {
	tests := []struct {
		flag      string
		shorthand string
	}{
		{"verbose", "v"},
		{"quiet", "q"},
		{"chdir", "C"},
		{"spec", "s"},
	}

	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			f := rootCmd.PersistentFlags().Lookup(tt.flag)
			require.NotNil(t, f, "flag --%s should exist", tt.flag)
			assert.Equal(t, tt.shorthand, f.Shorthand, "shorthand for --%s", tt.flag)
		})
	}
}

func TestVersionCommand(t *testing.T) {
	out, err := executeCommandCaptureStdout("version")
	require.NoError(t, err)
	assert.Contains(t, out, "stackkit version")
	assert.Contains(t, out, "Git commit:")
	assert.Contains(t, out, "Build date:")
	assert.Contains(t, out, "Go version:")
	assert.Contains(t, out, "OS/Arch:")
}

func TestInitCommand_NonInteractive_MissingName(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := executeCommand("init", "--non-interactive", "--chdir", tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-interactive")
}

func TestValidateCommand_NoSpecFile(t *testing.T) {
	tmpDir := t.TempDir()

	// validate returns an error when the spec file cannot be loaded (the
	// loader wraps the underlying error so os.IsNotExist does not match).
	_, err := executeCommand("validate", "--spec", filepath.Join(tmpDir, "nonexistent.yaml"), "--chdir", tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
}

func TestPlanCommand_NoSpecFile(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := executeCommand("plan", "--spec", filepath.Join(tmpDir, "nonexistent.yaml"), "--chdir", tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load spec")
}

func TestApplyCommand_NoSpecFile(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := executeCommand("apply", "--spec", filepath.Join(tmpDir, "nonexistent.yaml"), "--chdir", tmpDir)
	require.Error(t, err)
	// apply now attempts auto-init when spec is missing
	assert.True(t,
		strings.Contains(err.Error(), "failed to load spec") || strings.Contains(err.Error(), "no spec file"),
		"unexpected error: %s", err.Error())
}

func TestRemoveCommand_NoDeployDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a minimal spec so the loader doesn't fail before the deploy-dir check.
	specPath := filepath.Join(tmpDir, "stack-spec.yaml")
	specContent := `name: test
stackkit: test-kit
mode: simple
`
	require.NoError(t, os.WriteFile(specPath, []byte(specContent), 0600))

	// Remove should succeed even without a deploy dir — falls back to Docker cleanup
	_, err := executeCommand("remove", "--auto-approve", "--spec", specPath, "--chdir", tmpDir)
	assert.NoError(t, err)
}

func TestCompletionCommand_RequiresShellArg(t *testing.T) {
	_, err := executeCommand("completion")
	require.Error(t, err)
}

func TestCompletionCommand_ValidShells(t *testing.T) {
	shells := []string{"bash", "zsh", "fish", "powershell"}
	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			// Completion writes directly to os.Stdout. Drain the pipe
			// in a goroutine to prevent blocking on Windows when the
			// output exceeds the OS pipe buffer.
			r, w, err := os.Pipe()
			require.NoError(t, err)

			var buf bytes.Buffer
			done := make(chan struct{})
			go func() {
				_, _ = buf.ReadFrom(r)
				close(done)
			}()

			orig := os.Stdout
			os.Stdout = w

			rootCmd.SetArgs([]string{"completion", shell})
			execErr := rootCmd.Execute()

			_ = w.Close()
			os.Stdout = orig
			<-done
			_ = r.Close()

			assert.NoError(t, execErr)
			assert.Greater(t, buf.Len(), 0, "completion output should not be empty")
		})
	}
}

func TestStatusCommand_NoSpecFile(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := executeCommand("status", "--spec", filepath.Join(tmpDir, "nonexistent.yaml"), "--chdir", tmpDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load spec")
}

func TestGenerateRandomPassword(t *testing.T) {
	pw, err := generateRandomPassword(16)
	require.NoError(t, err)
	assert.Len(t, pw, 16)

	// Should be alphanumeric only
	for _, c := range pw {
		assert.True(t, (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'),
			"unexpected character %q in password", c)
	}

	// Two passwords should differ (probabilistic but virtually certain for 16 chars)
	pw2, err := generateRandomPassword(16)
	require.NoError(t, err)
	assert.NotEqual(t, pw, pw2)
}

func TestResolveModulesDirPrefersWorkspaceModules(t *testing.T) {
	root := t.TempDir()
	stackkitDir := filepath.Join(root, "base-kit")
	require.NoError(t, os.MkdirAll(stackkitDir, 0750))

	workspaceModules := filepath.Join(root, "modules")
	require.NoError(t, os.MkdirAll(workspaceModules, 0750))

	assert.Equal(t, workspaceModules, resolveModulesDir(stackkitDir, root))
}

func TestGenerateCommand_BaseKitDefaultSpecGoldenPath(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	outputRel := filepath.ToSlash(filepath.Join("build", "generate-command-"+strings.ReplaceAll(t.Name(), "/", "-")))
	outputAbs := filepath.Join(repoRoot, filepath.FromSlash(outputRel))
	require.NoError(t, os.RemoveAll(outputAbs))
	t.Cleanup(func() { _ = os.RemoveAll(outputAbs) })

	_, err = executeCommandCaptureStdout(
		"--no-log",
		"--spec", "base-kit/default-spec.yaml",
		"--chdir", repoRoot,
		"generate",
		"--output", outputRel,
		"--force",
	)
	require.NoError(t, err)

	for _, rel := range []string{
		"main.tf",
		"terraform.tfvars.json",
	} {
		path := filepath.Join(outputAbs, rel)
		info, statErr := os.Stat(path)
		require.NoError(t, statErr, "expected generated file %s", rel)
		assert.Greater(t, info.Size(), int64(0), "%s should not be empty", rel)
	}

	mainTFData, err := os.ReadFile(filepath.Join(outputAbs, "main.tf"))
	require.NoError(t, err)
	parser := hclparse.NewParser()
	_, diags := parser.ParseHCLFile(filepath.Join(outputAbs, "main.tf"))
	require.False(t, diags.HasErrors(), "generated main.tf should parse as HCL: %s", diags.Error())

	mainTF := string(mainTFData)
	assert.Contains(t, mainTF, `variable "tinyauth_users"`)
	assert.Contains(t, mainTF, `variable "tinyauth_oidc_enabled"`)
	assert.Contains(t, mainTF, `output "vaultwarden_admin_token"`)
	assert.Contains(t, mainTF, `resource "docker_container" "dashboard"`)
	assert.Contains(t, mainTF, `PROVIDERS_POCKETID_CLIENT_ID`)
	assert.Contains(t, mainTF, `OAUTH_AUTO_REDIRECT=pocketid`)

	_, statErr := os.Stat(filepath.Join(outputAbs, "immich.tf"))
	assert.True(t, os.IsNotExist(statErr), "default production generation should use the stable Base Kit template, not experimental fragments")
	_, statErr = os.Stat(filepath.Join(outputAbs, "pocketid.tf"))
	assert.True(t, os.IsNotExist(statErr), "stable Base Kit generation should keep PocketID in main.tf instead of experimental fragments")

	moduleFiles, err := os.ReadDir(outputAbs)
	require.NoError(t, err)
	for _, entry := range moduleFiles {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".tf" {
			continue
		}
		if entry.Name() == "main.tf" {
			// The stable template legitimately embeds Docker CLI Go templates
			// such as docker ps --format '{{.Names}}' inside shell snippets.
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(outputAbs, entry.Name()))
		require.NoError(t, readErr)
		assert.NotContains(t, string(data), "{{.", "%s should not contain unresolved template placeholders", entry.Name())
	}

	tfvarsData, err := os.ReadFile(filepath.Join(outputAbs, "terraform.tfvars.json"))
	require.NoError(t, err)
	var tfvars map[string]any
	require.NoError(t, json.Unmarshal(tfvarsData, &tfvars))
	assert.NotEmpty(t, tfvars["tinyauth_users"])
	assert.NotEmpty(t, tfvars["vaultwarden_admin_token"])
	assert.Equal(t, "UTC", tfvars["timezone"])
	assert.Equal(t, true, tfvars["enable_pocketid"])
	assert.Equal(t, "https://id.home.localhost", tfvars["pocketid_app_url"])
	assert.Equal(t, true, tfvars["tinyauth_oidc_enabled"])
	assert.Equal(t, "https://id.home.localhost", tfvars["tinyauth_oidc_issuer"])
	assert.NotEmpty(t, tfvars["tinyauth_oidc_client_id"])
}

func TestGenerateCommandRejectsPocketIDDisableWithoutPasskeyProvider(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	workDir := filepath.Join(repoRoot, ".tmp-generate-invalid-identity-"+strings.ReplaceAll(t.Name(), "/", "-"))
	require.NoError(t, os.MkdirAll(workDir, 0750))
	t.Cleanup(func() { _ = os.RemoveAll(workDir) })

	specPath := filepath.Join(workDir, "stack-spec.yaml")
	spec := `name: invalid-identity
stackkit: base-kit
mode: simple
domain: home.localhost
services:
  pocketid:
    enabled: false
`
	require.NoError(t, os.WriteFile(specPath, []byte(spec), 0600))

	_, err = executeCommandCaptureStdout(
		"--no-log",
		"--spec", specPath,
		"--chdir", repoRoot,
		"generate",
		"--output", filepath.ToSlash(filepath.Join("build", "invalid-identity-"+strings.ReplaceAll(t.Name(), "/", "-"))),
		"--force",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pocketid cannot be disabled")
}

func TestPocketIDSetupLinksUseSupportedRoute(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	for _, rel := range []string{
		"base-install.sh",
		filepath.Join("base-kit", "templates", "simple", "main.tf"),
	} {
		data, err := os.ReadFile(filepath.Join(repoRoot, rel))
		require.NoError(t, err, rel)
		content := string(data)
		assert.NotContains(t, content, "/login/setup", "%s must not point at PocketID's removed route", rel)
		assert.Contains(t, content, "/setup", "%s should point at PocketID's supported initial setup route", rel)
	}
}

func TestTinyAuthPocketIDUsesInternalServerSideOIDCEndpoints(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	data, err := os.ReadFile(filepath.Join(repoRoot, "base-kit", "templates", "simple", "main.tf"))
	require.NoError(t, err)
	mainTF := string(data)

	assert.Contains(t, mainTF, `PROVIDERS_POCKETID_AUTH_URL=${var.tinyauth_oidc_issuer}/authorize`)
	assert.Contains(t, mainTF, `PROVIDERS_POCKETID_REDIRECT_URL=${var.tinyauth_app_url}/api/oauth/callback/pocketid`)
	assert.Contains(t, mainTF, `pocketid_internal_oidc_origin`)
	assert.Contains(t, mainTF, `"http://pocketid:1411"`)
	assert.Contains(t, mainTF, `"http://127.0.0.1:${local.host_ports.pocketid}"`)
	assert.Contains(t, mainTF, `PROVIDERS_POCKETID_TOKEN_URL=${local.pocketid_internal_oidc_origin}/api/oidc/token`)
	assert.Contains(t, mainTF, `PROVIDERS_POCKETID_USER_INFO_URL=${local.pocketid_internal_oidc_origin}/api/oidc/userinfo`)
}

func TestTinyAuthPocketIDDoesNotRestrictOAuthToAdminEmailByDefault(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	data, err := os.ReadFile(filepath.Join(repoRoot, "base-kit", "templates", "simple", "main.tf"))
	require.NoError(t, err)
	mainTF := string(data)

	assert.Contains(t, mainTF, `variable "tinyauth_oauth_whitelist"`)
	assert.Contains(t, mainTF, `OAUTH_WHITELIST=${var.tinyauth_oauth_whitelist}`)
	assert.NotContains(t, mainTF, `OAUTH_WHITELIST=${var.admin_email}`)
}

func TestGenerateCommand_KombinationAliasFromChildWorkDir(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))

	workDir := filepath.Join(repoRoot, ".tmp-generate-alias-"+strings.ReplaceAll(t.Name(), "/", "-"))
	require.NoError(t, os.MkdirAll(workDir, 0750))
	t.Cleanup(func() { _ = os.RemoveAll(workDir) })

	spec := `name: alias-generate
stackkit: base-kit
mode: simple
domain: home.localhost
network:
  mode: local
compute:
  tier: standard
ssh:
  user: root
  port: 22
`
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "kombination.yaml"), []byte(spec), 0600))

	_, err = executeCommandCaptureStdout(
		"--no-log",
		"--spec", "stack-spec.yaml",
		"--chdir", workDir,
		"generate",
		"--output", "deploy",
		"--force",
	)
	require.NoError(t, err)

	generatedPath := filepath.Join(workDir, "deploy", "terraform.tfvars.json")
	info, statErr := os.Stat(generatedPath)
	require.NoError(t, statErr)
	assert.Greater(t, info.Size(), int64(0), "terraform.tfvars.json should not be empty")
}

func TestBcryptHash(t *testing.T) {
	password := "testpassword123"
	hash, err := bcryptHash(password)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "$2a$"), "hash should be bcrypt format")

	// Verify the hash matches the password
	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	assert.NoError(t, err)

	// Wrong password should not match
	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte("wrong"))
	assert.Error(t, err)
}

func TestGenerateTfvarsJSON_AdminEmail(t *testing.T) {
	spec := &models.StackSpec{
		Name:       "test-homelab",
		AdminEmail: "test@example.com",
		Domain:     "home.example.com",
	}

	// Composition engine provides identity credentials
	cr := &composition.CompositionResult{
		EnabledModules: []string{"socket-proxy", "traefik", "tinyauth", "pocketid"},
		ModuleSettings: map[string]map[string]any{},
		Identity: &composition.IdentityConfig{
			AdminEmail:            "test@example.com",
			AdminPassword:         "abcdef0123456789abcdef0123456789",
			TinyAuthEnabled:       true,
			PocketIDEnabled:       true,
			TinyAuthSessionSecret: "session_secret_hex",
			OIDCClientID:          "client_id_hex",
			OIDCClientSecret:      "client_secret_hex",
			OIDCIssuerURL:         "https://id.home.example.com",
			PocketIDAppURL:        "https://id.home.example.com",
			TinyAuthOAuthEnabled:  true,
		},
	}

	data, err := generateTfvarsJSON(spec, cr)
	require.NoError(t, err)

	var vars map[string]interface{}
	err = json.Unmarshal(data, &vars)
	require.NoError(t, err)

	assert.Equal(t, "test@example.com", vars["admin_email"])
	assert.Equal(t, true, vars["enable_dashboard"])
	assert.Equal(t, "86400", vars["tinyauth_session_expiry"])
	assert.Equal(t, "false", vars["tinyauth_secure_cookie"])

	// tinyauth_users should be email:bcrypt_hash
	users, ok := vars["tinyauth_users"].(string)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(users, "test@example.com:$2a$"),
		"tinyauth_users should be email:bcrypt, got: %s", users)

	// admin_password_plaintext should be present (32 hex chars from composition engine)
	pw, ok := vars["admin_password_plaintext"].(string)
	require.True(t, ok)
	assert.Len(t, pw, 32)
}

func TestGenerateTfvarsJSON_FallbackAdmin(t *testing.T) {
	spec := &models.StackSpec{
		Name: "test-homelab",
	}

	// Without composition result, bridge generates structural defaults only (no credentials)
	data, err := generateTfvarsJSON(spec, nil)
	require.NoError(t, err)

	var vars map[string]interface{}
	err = json.Unmarshal(data, &vars)
	require.NoError(t, err)

	// Without composition result, admin_email comes from bridge defaults
	assert.Equal(t, "admin", vars["admin_email"])

	// Without composition result, no credentials are generated
	assert.Empty(t, vars["admin_password_plaintext"], "no password without composition result")
	assert.Empty(t, vars["tinyauth_users"], "no users without composition result")
}

func TestInitCommand_AdminEmailFlag(t *testing.T) {
	f := initCmd.Flags().Lookup("admin-email")
	require.NotNil(t, f, "--admin-email flag should exist")
	assert.Equal(t, "", f.DefValue)
}

func TestInitCommand_DomainFlag(t *testing.T) {
	f := initCmd.Flags().Lookup("domain")
	require.NotNil(t, f, "--domain flag should exist")
	assert.Equal(t, "", f.DefValue)
}

func TestResolveInitDefaults(t *testing.T) {
	resetInitGlobals := func(t *testing.T) {
		t.Helper()
		prevComputeTier := initComputeTier
		prevDomain := initDomain
		prevMode := initMode
		prevForce := initForce
		prevNonInteractive := initNonInteractive
		prevAdminEmail := initAdminEmail
		prevLocalDNS := initLocalDNS
		prevLocalName := initLocalName
		prevContextFlag := contextFlag

		initComputeTier = ""
		initDomain = ""
		initMode = ""
		initForce = false
		initNonInteractive = false
		initAdminEmail = ""
		initLocalDNS = false
		initLocalName = ""
		contextFlag = ""

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
		})
	}

	t.Run("cloud defaults to kombify me public and detected standard tier", func(t *testing.T) {
		resetInitGlobals(t)
		setCapabilitiesHome(t, models.ContextCloud)

		defaults := resolveInitDefaults("")

		assert.Equal(t, models.ContextCloud, defaults.Context)
		assert.Equal(t, models.ComputeTierStandard, defaults.ComputeTier)
		assert.Equal(t, models.DomainKombifyMe, defaults.Domain)
		assert.Equal(t, "public", defaults.NetworkMode)
	})

	t.Run("local defaults to home lab local network and detected high tier", func(t *testing.T) {
		resetInitGlobals(t)

		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		capsDir := filepath.Join(home, ".stackkits")
		require.NoError(t, os.MkdirAll(capsDir, 0750))

		caps := models.DockerCapabilities{
			ResolvedContext:  models.ContextLocal,
			BridgeNetworking: true,
			StorageDriver:    models.StorageOverlay2,
			CPUCores:         8,
			MemoryGB:         16,
		}
		data, err := json.Marshal(caps)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(capsDir, "capabilities.json"), data, 0600))

		defaults := resolveInitDefaults("")

		assert.Equal(t, models.ContextLocal, defaults.Context)
		assert.Equal(t, models.ComputeTierHigh, defaults.ComputeTier)
		assert.Equal(t, models.DomainHomeLab, defaults.Domain)
		assert.Equal(t, "local", defaults.NetworkMode)
	})

	t.Run("custom domain is preserved for cloud", func(t *testing.T) {
		resetInitGlobals(t)
		setCapabilitiesHome(t, models.ContextCloud)

		defaults := resolveInitDefaults("apps.example.com")

		assert.Equal(t, models.ContextCloud, defaults.Context)
		assert.Equal(t, "apps.example.com", defaults.Domain)
		assert.Equal(t, "public", defaults.NetworkMode)
	})

	t.Run("explicit flags win over detected defaults", func(t *testing.T) {
		resetInitGlobals(t)
		setCapabilitiesHome(t, models.ContextCloud)
		initComputeTier = models.ComputeTierLow
		contextFlag = string(models.ContextLocal)

		defaults := resolveInitDefaults("")

		assert.Equal(t, models.ContextLocal, defaults.Context)
		assert.Equal(t, models.ComputeTierLow, defaults.ComputeTier)
		assert.Equal(t, models.DomainHomeLab, defaults.Domain)
		assert.Equal(t, "local", defaults.NetworkMode)
	})

	t.Run("local dns defaults to home and local network", func(t *testing.T) {
		resetInitGlobals(t)
		setCapabilitiesHome(t, models.ContextCloud)
		initLocalDNS = true

		defaults := resolveInitDefaults("")

		assert.Equal(t, models.ContextCloud, defaults.Context)
		assert.Equal(t, models.DomainHomeDNS, defaults.Domain)
		assert.Equal(t, "local", defaults.NetworkMode)
	})

	t.Run("local dns short name expands to nested home zone", func(t *testing.T) {
		resetInitGlobals(t)
		setCapabilitiesHome(t, models.ContextCloud)
		initLocalDNS = true
		initLocalName = "family"

		defaults := resolveInitDefaults("")

		assert.Equal(t, "family.home", defaults.Domain)
		assert.Equal(t, "local", defaults.NetworkMode)
	})
}

func TestInitCommand_NonInteractive_DomainOverrideCreatesSpec(t *testing.T) {
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

	specFile = "stack-spec.yaml"

	cwd, err := os.Getwd()
	require.NoError(t, err)

	repoRoot := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))
	tmpDir, err := os.MkdirTemp(repoRoot, "init-domain-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	_, err = executeCommand(
		"init", "base-kit",
		"--non-interactive",
		"--force",
		"--context", "cloud",
		"--compute-tier", "standard",
		"--domain", "apps.example.com",
		"--admin-email", "test@example.com",
		"--chdir", tmpDir,
	)
	require.NoError(t, err)

	specData, err := os.ReadFile(filepath.Join(tmpDir, "stack-spec.yaml"))
	require.NoError(t, err)

	var spec models.StackSpec
	require.NoError(t, yaml.Unmarshal(specData, &spec))
	assert.Equal(t, "apps.example.com", spec.Domain)
	assert.Equal(t, string(models.ContextCloud), spec.Context)
	assert.Equal(t, "public", spec.Network.Mode)
	assert.Equal(t, "test@example.com", spec.AdminEmail)
}

func TestParseDfOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		wantGB float64
		wantOK bool
	}{
		{
			name: "normal df output",
			output: `     Avail      Size Target
 744488960 5267922944 /`,
			wantGB: 0.69, // ~694MB
			wantOK: true,
		},
		{
			name: "larger disk",
			output: `        Avail         Size Target
 21474836480  42949672960 /`,
			wantGB: 20.0,
			wantOK: true,
		},
		{
			name:   "empty output",
			output: "",
			wantOK: false,
		},
		{
			name:   "header only",
			output: "     Avail      Size Target",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			availGB, _, _ := parseDfOutput(tt.output)
			if tt.wantOK {
				assert.InDelta(t, tt.wantGB, availGB, 0.1, "available GB")
			} else {
				assert.Equal(t, float64(0), availGB)
			}
		})
	}
}

func TestIsNoSpaceError(t *testing.T) {
	tests := []struct {
		errMsg string
		want   bool
	}{
		{"no space left on device", true},
		{"write /var/lib/containerd/...: no space left on device", true},
		{"Error response from daemon: mkdir /var/lib/containerd/...: no space left on device", true},
		{"connection refused", false},
		{"timeout", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.errMsg, func(t *testing.T) {
			assert.Equal(t, tt.want, isNoSpaceError(tt.errMsg))
		})
	}
}

func TestResourceSpec_DiskFromYAML(t *testing.T) {
	yamlData := `
minimum:
  cpu: 2
  memory: 4
  disk: 50
recommended:
  cpu: 4
  memory: 8
  disk: 100
`
	var reqs models.Requirements
	err := yaml.Unmarshal([]byte(yamlData), &reqs)
	require.NoError(t, err)

	assert.Equal(t, 2, reqs.Minimum.CPU)
	assert.Equal(t, 4, reqs.Minimum.RAM)
	assert.Equal(t, 50, reqs.Minimum.Disk)
	assert.Equal(t, 4, reqs.Recommended.CPU)
	assert.Equal(t, 8, reqs.Recommended.RAM)
	assert.Equal(t, 100, reqs.Recommended.Disk)
}

func TestDockerCapabilities_DiskFields(t *testing.T) {
	caps := &models.DockerCapabilities{
		DiskTotalGB: 20.0,
		DiskAvailGB: 15.5,
		DiskMount:   "/",
		LVMDetected: true,
		LVMExtended: false,
	}

	data, err := json.Marshal(caps)
	require.NoError(t, err)

	var decoded models.DockerCapabilities
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.InDelta(t, 20.0, decoded.DiskTotalGB, 0.01)
	assert.InDelta(t, 15.5, decoded.DiskAvailGB, 0.01)
	assert.Equal(t, "/", decoded.DiskMount)
	assert.True(t, decoded.LVMDetected)
	assert.False(t, decoded.LVMExtended)
}

func TestRemoveCommand_PurgeFlag(t *testing.T) {
	f := removeCmd.Flags().Lookup("purge")
	require.NotNil(t, f, "--purge flag should exist")
	assert.Equal(t, "false", f.DefValue)
}

func TestCleanupFiles_Purge(t *testing.T) {
	tmpDir := t.TempDir()

	// Create directories that should be removed
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "deploy", ".terraform", "providers"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "deploy", "main.tf"), []byte("# test"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".stackkit"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".stackkit", "state.yaml"), []byte("status: running"), 0600))

	cleanupFiles(tmpDir, true)

	// All directories should be gone
	_, err := os.Stat(filepath.Join(tmpDir, "deploy"))
	assert.True(t, os.IsNotExist(err), "deploy/ should be removed after purge")

	_, err = os.Stat(filepath.Join(tmpDir, ".stackkit"))
	assert.True(t, os.IsNotExist(err), ".stackkit/ should be removed after purge")
}

func TestAutoDetectComputeTier(t *testing.T) {
	tests := []struct {
		name     string
		cpu      int
		memory   float64
		expected string
	}{
		{"high tier", 8, 16.0, "high"},
		{"high tier large", 16, 64.0, "high"},
		{"standard tier", 4, 8.0, "standard"},
		{"standard tier 6 cpu", 6, 12.0, "standard"},
		{"low tier few cpu", 2, 4.0, "low"},
		{"low tier low ram", 4, 4.0, "low"},
		{"low tier minimal", 1, 1.0, "low"},
		{"boundary high cpu low ram", 8, 8.0, "standard"},
		{"boundary low cpu high ram", 2, 16.0, "low"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := autoDetectComputeTier(tt.cpu, tt.memory)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateTfvarsJSON_TierDriven(t *testing.T) {
	tests := []struct {
		name         string
		tier         string
		wantDokploy  bool
		wantDockge   bool
		wantKuma     bool
		wantTraefik  bool
		wantTinyauth bool
		wantPocketid bool
	}{
		{
			name:         "standard tier",
			tier:         "standard",
			wantDokploy:  true,
			wantDockge:   false,
			wantKuma:     true,
			wantTraefik:  true,
			wantTinyauth: true,
			wantPocketid: true,
		},
		{
			name:         "high tier",
			tier:         "high",
			wantDokploy:  true,
			wantDockge:   false,
			wantKuma:     true,
			wantTraefik:  true,
			wantTinyauth: true,
			wantPocketid: true,
		},
		{
			name:         "low tier",
			tier:         "low",
			wantDokploy:  false,
			wantDockge:   true,
			wantKuma:     true,
			wantTraefik:  true,
			wantTinyauth: true,
			wantPocketid: true,
		},
		{
			name:         "empty tier defaults to standard",
			tier:         "",
			wantDokploy:  true,
			wantDockge:   false,
			wantKuma:     true,
			wantTraefik:  true,
			wantTinyauth: true,
			wantPocketid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setCapabilitiesHome(t, models.ContextLocal)

			spec := &models.StackSpec{
				Name:   "test",
				Domain: "test.local",
				Compute: models.ComputeSpec{
					Tier: tt.tier,
				},
			}

			data, err := generateTfvarsJSON(spec, nil)
			require.NoError(t, err)

			var vars map[string]interface{}
			err = json.Unmarshal(data, &vars)
			require.NoError(t, err)

			// L1/L2 core — always enabled
			assert.Equal(t, tt.wantTraefik, vars["enable_traefik"], "enable_traefik")
			assert.Equal(t, tt.wantTinyauth, vars["enable_tinyauth"], "enable_tinyauth")
			assert.Equal(t, tt.wantPocketid, vars["enable_pocketid"], "enable_pocketid")

			// PAAS — tier-dependent
			assert.Equal(t, tt.wantDokploy, vars["enable_dokploy"], "enable_dokploy")
			assert.Equal(t, tt.wantDockge, vars["enable_dockge"], "enable_dockge")

			// Monitoring — tier-dependent
			assert.Equal(t, tt.wantKuma, vars["enable_uptime_kuma"], "enable_uptime_kuma")

			// Dashboard — always
			assert.Equal(t, true, vars["enable_dashboard"], "enable_dashboard")
		})
	}
}

func TestCleanupFiles_NoPurge(t *testing.T) {
	tmpDir := t.TempDir()

	// Create directories
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "deploy", ".terraform", "providers"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "deploy", "traefik.tf"), []byte("# test"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "deploy", "terraform.tfvars.json"), []byte("{}"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".stackkit"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".stackkit", "state.yaml"), []byte("status: running"), 0600))

	cleanupFiles(tmpDir, false)

	// .terraform should be removed but deploy/ and .stackkit/ should remain
	_, err := os.Stat(filepath.Join(tmpDir, "deploy", ".terraform"))
	assert.True(t, os.IsNotExist(err), ".terraform/ should be removed")

	_, err = os.Stat(filepath.Join(tmpDir, "deploy", "traefik.tf"))
	assert.NoError(t, err, "generated deploy/*.tf files should still exist")

	_, err = os.Stat(filepath.Join(tmpDir, "deploy", "terraform.tfvars.json"))
	assert.NoError(t, err, "generated terraform.tfvars.json should still exist")

	_, err = os.Stat(filepath.Join(tmpDir, ".stackkit", "state.yaml"))
	assert.NoError(t, err, ".stackkit/state.yaml should still exist")
}
