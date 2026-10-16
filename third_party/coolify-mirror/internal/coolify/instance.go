// Package coolify knows where a self-hosted Coolify v4 keeps its state and how
// to talk to its database and its Laravel application.
package coolify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/mtalavi/coolify-mirror/internal/lcrypt"
	"github.com/mtalavi/coolify-mirror/internal/run"
)

// Well-known locations and container names of a standard Coolify install.
const (
	BaseDir        = "/data/coolify"
	SourceDir      = "/data/coolify/source"
	EnvPath        = "/data/coolify/source/.env"
	AppContainer   = "coolify"
	DBContainer    = "coolify-db"
	RedisContainer = "coolify-redis"
	ProxyContainer = "coolify-proxy"
	// LocalServerID is the id of the "localhost" server row on self-hosted Coolify.
	LocalServerID = 0
)

// Instance is a detected, running Coolify installation on this host.
type Instance struct {
	Env        *EnvFile
	Crypt      *lcrypt.Encrypter
	AppKey     string
	DBUser     string
	DBName     string
	Version    string // e.g. 4.3.23
	Image      string
	DockerRoot string
	Hostname   string
	Arch       string

	tablesMu sync.Mutex
	tables   map[string]bool
}

// Detect inspects this host. It fails with a readable reason when this is not a
// Coolify host or when the tool does not run as root.
func Detect(ctx context.Context) (*Instance, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("this tool must run as root (try: sudo ./coolify-mirror)")
	}
	if !run.Exists("docker") {
		return nil, errors.New("docker is not installed on this server")
	}
	if _, err := run.Text(ctx, "docker", "version", "--format", "{{.Server.Version}}"); err != nil {
		return nil, fmt.Errorf("cannot talk to the Docker daemon: %w", err)
	}
	env, err := LoadEnv(EnvPath)
	if err != nil {
		return nil, fmt.Errorf("Coolify is not installed here (%s not readable): %w", EnvPath, err)
	}
	in := &Instance{Env: env}
	in.AppKey = env.Value("APP_KEY", "")
	in.Crypt, err = lcrypt.New(in.AppKey, env.Value("APP_PREVIOUS_KEYS", ""))
	if err != nil {
		return nil, fmt.Errorf("APP_KEY in %s: %w", EnvPath, err)
	}
	in.DBUser = env.Value("DB_USERNAME", "coolify")
	in.DBName = env.Value("DB_DATABASE", "coolify")

	for _, c := range []string{AppContainer, DBContainer} {
		st, err := ContainerState(ctx, c)
		if err != nil {
			return nil, fmt.Errorf("container %q not found - is Coolify installed and started? (%v)", c, err)
		}
		if st != "running" {
			return nil, fmt.Errorf("container %q is %s; start Coolify first", c, st)
		}
	}
	in.Image, _ = run.Text(ctx, "docker", "inspect", "--format", "{{.Config.Image}}", AppContainer)
	in.Version = versionFromImage(in.Image)
	if in.Version == "" {
		in.Version = strings.TrimPrefix(env.Value("COOLIFY_VERSION", env.Value("LATEST_IMAGE", "")), "v")
	}
	if in.Version == "" || in.Version == "latest" {
		if v, err := in.phpVersion(ctx); err == nil {
			in.Version = v
		}
	}
	in.DockerRoot, _ = run.Text(ctx, "docker", "info", "--format", "{{.DockerRootDir}}")
	if in.DockerRoot == "" {
		in.DockerRoot = "/var/lib/docker"
	}
	in.Arch, _ = run.Text(ctx, "docker", "info", "--format", "{{.Architecture}}")
	in.Hostname, _ = os.Hostname()
	return in, nil
}

var tagRe = regexp.MustCompile(`:v?(\d+\.\d+(?:\.\d+)?(?:[-.][0-9A-Za-z.]+)?)$`)

func versionFromImage(img string) string {
	if m := tagRe.FindStringSubmatch(img); m != nil {
		return m[1]
	}
	return ""
}

// ContainerState returns docker's State.Status for a container.
func ContainerState(ctx context.Context, name string) (string, error) {
	return run.Text(ctx, "docker", "inspect", "--format", "{{.State.Status}}", name)
}

func (in *Instance) phpVersion(ctx context.Context) (string, error) {
	var out struct {
		Version string `json:"version"`
	}
	if err := in.PHP(ctx, "version", nil, &out); err != nil {
		return "", err
	}
	return out.Version, nil
}

// PublicIPs returns the IPs Coolify detected for this server (instance_settings),
// falling back to the primary outbound address.
func (in *Instance) PublicIPs(ctx context.Context) (v4, v6 string) {
	var rows []struct {
		V4 *string `json:"public_ipv4"`
		V6 *string `json:"public_ipv6"`
	}
	if err := in.Query(ctx, `SELECT public_ipv4, public_ipv6 FROM instance_settings ORDER BY id LIMIT 1`, &rows); err == nil && len(rows) == 1 {
		if rows[0].V4 != nil {
			v4 = strings.TrimSpace(*rows[0].V4)
		}
		if rows[0].V6 != nil {
			v6 = strings.TrimSpace(*rows[0].V6)
		}
	}
	if v4 == "" {
		v4 = OutboundIP()
	}
	return v4, v6
}

// OutboundIP is the local address used to reach the internet (no packets sent).
func OutboundIP() string {
	c, err := net.Dial("udp4", "1.1.1.1:53")
	if err != nil {
		return ""
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}

// CompareVersions compares Coolify versions like 4.3.23, 4.4-rc.1, 4.0.0-beta.474.
// It returns -1, 0 or 1. Pre-release versions sort before their release.
func CompareVersions(a, b string) int {
	pa, preA := splitVersion(a)
	pb, preB := splitVersion(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case preA == preB:
		return 0
	case preA == "":
		return 1
	case preB == "":
		return -1
	}
	na, nb := trailingNumber(preA), trailingNumber(preB)
	if na != nb {
		if na < nb {
			return -1
		}
		return 1
	}
	return strings.Compare(preA, preB)
}

func splitVersion(v string) ([]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	core, pre, _ := strings.Cut(v, "-")
	var nums []int
	for _, p := range strings.Split(core, ".") {
		n, _ := strconv.Atoi(p)
		nums = append(nums, n)
	}
	return nums, pre
}

func trailingNumber(s string) int {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	n, _ := strconv.Atoi(s[i:])
	return n
}

// jsonUnmarshalNumber decodes JSON keeping numbers as json.Number.
func jsonUnmarshalNumber(b []byte, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	return dec.Decode(v)
}
