//go:build production

package production

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

type stackKitsBaseConfig struct {
	Host       string
	Port       int
	User       string
	Password   string
	HomeDir    string
	ProjectDir string
	AdminEmail string
}

func TestStackKitsBaseLocalReinstall(t *testing.T) {
	cfg := loadStackKitsBaseConfig(t)

	node := Node{
		SSHIP:       cfg.Host,
		SSHPort:     cfg.Port,
		SSHUser:     cfg.User,
		SSHPassword: cfg.Password,
	}

	ssh, err := NewSSHSession(node)
	if err != nil {
		t.Fatalf("SSH connect to stackkitsbase: %v", err)
	}
	defer ssh.Close()

	repoRoot := repoRootFromCaller(t)
	binaryPath := buildLinuxStackKitBinary(t, repoRoot)
	kitArchivePath := buildKitArchive(t, repoRoot, []string{"base", "base-kit", "modules"})

	remoteRoot := filepath.ToSlash(filepath.Join(cfg.HomeDir, ".stackkits-ci"))
	remoteBin := remoteRoot + "/bin/stackkit"
	remoteArchive := remoteRoot + "/stackkits-bundle.tar.gz"

	cleanupRemoteBaseKit(t, ssh, cfg)
	assertBaseKitClean(t, ssh)

	if out, err := ssh.Run(fmt.Sprintf("mkdir -p %s/bin %s", shellQuote(remoteRoot), shellQuote(cfg.ProjectDir))); err != nil {
		t.Fatalf("prepare remote dirs: %v\n%s", err, out)
	}
	if err := ssh.Upload(binaryPath, remoteBin); err != nil {
		t.Fatalf("upload stackkit binary: %v", err)
	}
	if _, err := ssh.Run(fmt.Sprintf("chmod +x %s", shellQuote(remoteBin))); err != nil {
		t.Fatalf("chmod remote binary: %v", err)
	}
	if err := ssh.Upload(kitArchivePath, remoteArchive); err != nil {
		t.Fatalf("upload kit archive: %v", err)
	}
	if out, err := ssh.Run(fmt.Sprintf("rm -rf %s/.stackkits/base %s/.stackkits/base-kit %s/.stackkits/modules && mkdir -p %s/.stackkits && tar -xzf %s -C %s/.stackkits", shellQuote(cfg.HomeDir), shellQuote(cfg.HomeDir), shellQuote(cfg.HomeDir), shellQuote(cfg.HomeDir), shellQuote(remoteArchive), shellQuote(cfg.HomeDir))); err != nil {
		t.Fatalf("extract kit archive: %v\n%s", err, out)
	}

	prepareScript := fmt.Sprintf("cd %s && HOME=%s PATH=%s/bin:$PATH %s --context local prepare --force",
		remoteRoot,
		cfg.HomeDir,
		remoteRoot,
		remoteBin,
	)
	prepareCmd := fmt.Sprintf("sudo -n sh -lc %s || printf '%%s\\n' %s | sudo -S sh -lc %s",
		shellQuote(prepareScript),
		shellQuote(cfg.Password),
		shellQuote(prepareScript),
	)
	if out, exitCode, err := ssh.RunOutput(prepareCmd); err != nil || exitCode != 0 {
		t.Fatalf("stackkit prepare failed: %v (exit=%d)\n%s", err, exitCode, lastLines(out, 80))
	}

	initCmd := fmt.Sprintf("export HOME=%s PATH=%s/bin:$PATH; mkdir -p %s && cd %s && %s --context local init base-kit --non-interactive --force --admin-email %s --domain home.lab",
		shellQuote(cfg.HomeDir),
		shellQuote(remoteRoot),
		shellQuote(cfg.ProjectDir),
		shellQuote(cfg.ProjectDir),
		shellQuote(remoteBin),
		shellQuote(cfg.AdminEmail),
	)
	if out, exitCode, err := ssh.RunOutput(initCmd); err != nil || exitCode != 0 {
		t.Fatalf("stackkit init failed: %v (exit=%d)\n%s", err, exitCode, lastLines(out, 80))
	}

	generateCmd := fmt.Sprintf("export HOME=%s PATH=%s/bin:$PATH; cd %s && %s generate --force",
		shellQuote(cfg.HomeDir),
		shellQuote(remoteRoot),
		shellQuote(cfg.ProjectDir),
		shellQuote(remoteBin),
	)
	if out, exitCode, err := ssh.RunOutput(generateCmd); err != nil || exitCode != 0 {
		t.Fatalf("stackkit generate failed: %v (exit=%d)\n%s", err, exitCode, lastLines(out, 80))
	}

	specContents, err := ssh.Run(fmt.Sprintf("sed -n '1,220p' %s/stack-spec.yaml", shellQuote(cfg.ProjectDir)))
	if err != nil {
		t.Fatalf("read stack-spec.yaml: %v", err)
	}
	if !strings.Contains(specContents, "context: local") {
		t.Fatalf("expected local context in stack-spec.yaml, got:\n%s", specContents)
	}
	if !strings.Contains(specContents, "domain: home.lab") {
		t.Fatalf("expected home.lab domain in stack-spec.yaml, got:\n%s", specContents)
	}
	if !strings.Contains(specContents, "mode: local") {
		t.Fatalf("expected local network mode in stack-spec.yaml, got:\n%s", specContents)
	}

	tfvars, err := ssh.Run(fmt.Sprintf("sed -n '1,220p' %s/deploy/terraform.tfvars.json", shellQuote(cfg.ProjectDir)))
	if err != nil {
		t.Fatalf("read terraform.tfvars.json: %v", err)
	}
	if !strings.Contains(tfvars, "\"domain\": \"home.lab\"") {
		t.Fatalf("expected local home.lab domain in tfvars, got:\n%s", tfvars)
	}
	if !strings.Contains(tfvars, "\"enable_dnsmasq\": true") {
		t.Fatalf("expected dnsmasq enabled in local tfvars, got:\n%s", tfvars)
	}

	applyCmd := fmt.Sprintf("export HOME=%s PATH=%s/bin:$PATH; cd %s && %s apply --auto-approve",
		shellQuote(cfg.HomeDir),
		shellQuote(remoteRoot),
		shellQuote(cfg.ProjectDir),
		shellQuote(remoteBin),
	)
	if out, exitCode, err := ssh.RunOutput(applyCmd); err != nil || exitCode != 0 {
		t.Fatalf("stackkit apply failed: %v (exit=%d)\n%s", err, exitCode, lastLines(out, 120))
	}

	time.Sleep(15 * time.Second)

	statusCmd := fmt.Sprintf("export HOME=%s PATH=%s/bin:$PATH; cd %s && %s status",
		shellQuote(cfg.HomeDir),
		shellQuote(remoteRoot),
		shellQuote(cfg.ProjectDir),
		shellQuote(remoteBin),
	)
	if out, exitCode, err := ssh.RunOutput(statusCmd); err != nil || exitCode != 0 {
		t.Fatalf("stackkit status failed: %v (exit=%d)\n%s", err, exitCode, lastLines(out, 80))
	}

	assertCoreContainersPresent(t, ssh, []string{
		"dnsmasq",
		"traefik",
		"tinyauth",
		"dokploy",
		"dokploy-postgres",
		"dokploy-redis",
		"dashboard",
		"kuma",
		"whoami",
	})
}

func loadStackKitsBaseConfig(t *testing.T) stackKitsBaseConfig {
	t.Helper()

	host := firstEnv("STACKKITSBASE_HOST", "STACKKIT_TESTDEVICE_IP")
	user := firstEnv("STACKKITSBASE_USER", "STACKKIT_TESTDEVICE_USER")
	password := firstEnv("STACKKITSBASE_PASSWORD", "STACKKIT_TESTDEVICE_PASSWORD")
	if host == "" || user == "" || password == "" {
		t.Skip("stackkitsbase credentials not configured")
	}

	port := 22
	if raw := firstEnv("STACKKITSBASE_PORT", "STACKKIT_TESTDEVICE_PORT"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("invalid stackkitsbase port %q: %v", raw, err)
		}
		port = parsed
	}

	homeDir := os.Getenv("STACKKITSBASE_HOME")
	if homeDir == "" {
		homeDir = fmt.Sprintf("/home/%s", user)
	}

	projectDir := os.Getenv("STACKKITSBASE_PROJECT_DIR")
	if projectDir == "" {
		projectDir = homeDir + "/my-homelab"
	}

	adminEmail := os.Getenv("STACKKITSBASE_ADMIN_EMAIL")
	if adminEmail == "" {
		adminEmail = "ci@kombify.io"
	}

	return stackKitsBaseConfig{
		Host:       host,
		Port:       port,
		User:       user,
		Password:   password,
		HomeDir:    homeDir,
		ProjectDir: projectDir,
		AdminEmail: adminEmail,
	}
}

func cleanupRemoteBaseKit(t *testing.T, ssh *SSHSession, cfg stackKitsBaseConfig) {
	t.Helper()

	cmd := fmt.Sprintf(`
set -eu
export HOME=%s
PROJECT_DIR=%s
if [ -x %s ] && [ -d "$PROJECT_DIR" ]; then
  cd "$PROJECT_DIR" && %s remove --auto-approve --force --purge || true
fi
for dir in "$HOME/my-homelab" "$HOME/homelab" "$HOME/stackkitsbase" "$HOME/stackkitsbase-test"; do
  if [ -d "$dir" ]; then
    rm -rf "$dir" || true
  fi
done
rm -rf "$HOME/test-kit" "$HOME/deploy" || true
rm -f "$HOME/stack-spec.yaml" || true
docker rm -f dnsmasq traefik tinyauth pocketid dokploy dokploy-postgres dokploy-redis dashboard vaultwarden kuma whoami jellyfin immich dockge coolify 2>/dev/null || true
docker network rm dokploy-network proxy coolify 2>/dev/null || true
docker volume ls -q | grep -E 'dokploy|kuma|vaultwarden|immich|jellyfin|pocketid|traefik|dnsmasq|dashboard' | xargs -r docker volume rm -f || true
`, shellQuote(cfg.HomeDir), shellQuote(cfg.ProjectDir), shellQuote(cfg.HomeDir+"/.stackkits-ci/bin/stackkit"), shellQuote(cfg.HomeDir+"/.stackkits-ci/bin/stackkit"))

	if out, exitCode, err := ssh.RunOutput(cmd); err != nil || exitCode != 0 {
		t.Fatalf("cleanup old base kit failed: %v (exit=%d)\n%s", err, exitCode, lastLines(out, 80))
	}
}

func assertBaseKitClean(t *testing.T, ssh *SSHSession) {
	t.Helper()

	out, err := ssh.Run("docker ps --format '{{.Names}}' | grep -E '^(dnsmasq|traefik|tinyauth|pocketid|dokploy|dokploy-postgres|dokploy-redis|dashboard|vaultwarden|kuma|whoami|jellyfin|immich|dockge|coolify)$' || true")
	if err != nil {
		t.Fatalf("check running containers after cleanup: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected clean stackkitsbase host after cleanup, still running:\n%s", out)
	}
}

func assertCoreContainersPresent(t *testing.T, ssh *SSHSession, expected []string) {
	t.Helper()

	out, err := ssh.Run("docker ps --format '{{.Names}}\\t{{.Status}}'")
	if err != nil {
		t.Fatalf("docker ps after install: %v", err)
	}
	for _, name := range expected {
		if !strings.Contains(out, name+"\tUp") {
			t.Fatalf("expected container %q to be up, got:\n%s", name, out)
		}
	}
}

func repoRootFromCaller(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func buildLinuxStackKitBinary(t *testing.T, repoRoot string) string {
	t.Helper()

	binaryPath := filepath.Join(t.TempDir(), "stackkit")
	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/stackkit")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build linux stackkit binary: %v\n%s", err, string(out))
	}
	return binaryPath
}

func buildKitArchive(t *testing.T, repoRoot string, relPaths []string) string {
	t.Helper()

	archivePath := filepath.Join(t.TempDir(), "stackkits-bundle.tar.gz")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	defer file.Close()

	gz := gzip.NewWriter(file)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	for _, rel := range relPaths {
		root := filepath.Join(repoRoot, rel)
		if err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			name, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			name = filepath.ToSlash(name)
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = name
			if info.IsDir() {
				header.Name += "/"
			}
			if err := tw.WriteHeader(header); err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		}); err != nil {
			t.Fatalf("archive %s: %v", rel, err)
		}
	}

	return archivePath
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
