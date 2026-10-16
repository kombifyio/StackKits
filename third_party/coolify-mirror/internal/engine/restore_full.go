package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
	"github.com/mtalavi/coolify-mirror/internal/dbx"
	"github.com/mtalavi/coolify-mirror/internal/docker"
	"github.com/mtalavi/coolify-mirror/internal/lcrypt"
	"github.com/mtalavi/coolify-mirror/internal/run"

	"golang.org/x/crypto/ssh"
)

// envKeepTarget are .env keys that belong to this server's Coolify containers
// (database / redis / realtime credentials, installed version) and are kept.
var envKeepTarget = map[string]bool{
	"APP_ID": true, "APP_KEY": true, "APP_PREVIOUS_KEYS": true,
	"DB_USERNAME": true, "DB_PASSWORD": true, "DB_DATABASE": true, "DB_HOST": true, "DB_PORT": true,
	"REDIS_HOST": true, "REDIS_PASSWORD": true, "REDIS_PORT": true,
	"PUSHER_APP_ID": true, "PUSHER_APP_KEY": true, "PUSHER_APP_SECRET": true,
	"LATEST_IMAGE": true, "COOLIFY_VERSION": true, "REGISTRY_URL": true,
	"DOCKER_ADDRESS_POOL_BASE": true, "DOCKER_ADDRESS_POOL_SIZE": true,
	"ROOT_USERNAME": true, "ROOT_USER_EMAIL": true, "ROOT_USER_PASSWORD": true,
}

// FullCheck lists problems found before a full restore.
type FullCheck struct {
	// Blockers forbid the restore: version mismatch, a non-empty target,
	// not enough disk space.
	Blockers []string
	// CoolifyWarnings come from Coolify's own transfer bundle validation.
	CoolifyWarnings []string
	ArchProblem     string   // different CPU architecture
	ExistingCount   int      // resources on this server that will be forgotten
	RemoteServers   []string // remote servers the restored Coolify will manage
	// ExistingVolumes already exist here with the same name as a restored
	// volume; they get the backup's data and their current data is set aside.
	ExistingVolumes []string
}

// CheckFull inspects the target before a full restore.
func CheckFull(ctx context.Context, in *coolify.Instance, f *Fetched) (*FullCheck, error) {
	c := &FullCheck{}
	c.Blockers = PreflightFull(ctx, in, f)
	c.CoolifyWarnings = BundleWarnings(ctx, in, f)
	if a := f.Manifest.Source.Arch; a != "" && in.Arch != "" && a != in.Arch {
		c.ArchProblem = fmt.Sprintf("the backup comes from a %s server and this one is %s: application images cannot be reused, Coolify will rebuild them", a, in.Arch)
	}
	if rs, err := in.ListResources(ctx); err == nil {
		c.ExistingCount = len(rs)
	}
	for _, v := range f.Manifest.Volumes {
		if ex, _ := docker.InspectVolume(ctx, v.Name); ex != nil && !v.External {
			c.ExistingVolumes = append(c.ExistingVolumes, v.Name)
		}
	}
	for _, r := range f.Manifest.Resources {
		if !r.Local() && r.ServerName != "" {
			seen := false
			for _, s := range c.RemoteServers {
				seen = seen || s == r.ServerName
			}
			if !seen {
				c.RemoteServers = append(c.RemoteServers, r.ServerName)
			}
		}
	}
	return c, nil
}

// ApplyFull replaces this server's Coolify with the backup: database, .env key,
// SSH keys, proxy configuration, every volume and directory, and images.
func ApplyFull(ctx context.Context, in *coolify.Instance, f *Fetched, pr *Progress) (rep *RestoreReport, err error) {
	defer func() { pr.End(err) }()
	unlock, err := Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := blockersErr(PreflightFull(ctx, in, f)); err != nil {
		return nil, err
	}
	start := time.Now()
	man := f.Manifest
	stamp := time.Now().Format("20060102-150405")
	safety := filepath.Join(HomeDir, "pre-restore-"+stamp)

	stSafety := pr.Add("Safety copy of this server's Coolify database and .env", 0)
	stStop := pr.Add("Stop Coolify", 0)
	stDB := pr.Add("Restore Coolify database", 0)
	pathSteps := map[string]*Step{}
	for _, p := range man.Paths {
		pathSteps[p.Path] = pr.Add("Files  "+p.Path, p.Size)
	}
	volSteps := map[string]*Step{}
	for _, v := range man.Volumes {
		volSteps[v.Name] = pr.Add("Volume "+v.Name, v.Size)
	}
	imgSteps := map[int]*Step{}
	for i, im := range man.Images {
		imgSteps[i] = pr.Add(fmt.Sprintf("Images (%d)", len(im.Refs)), im.Size)
	}
	stEnv := pr.Add("Install the source APP_KEY", 0)
	stSSH := pr.Add("Authorize Coolify's SSH key for this server", 0)
	stStart := pr.Add("Start Coolify", 0)
	stDeps := pr.Add("Check build and runtime dependencies", 0)

	if man.Source.Arch != "" && in.Arch != "" && man.Source.Arch != in.Arch {
		for i := range imgSteps {
			imgSteps[i].SkipStep("other CPU architecture")
			delete(imgSteps, i)
		}
	}

	dumpSteps := map[string]*Step{}
	for _, d := range man.Dumps {
		dumpSteps[d.Container] = pr.Add("Load database dump "+d.Container, d.Size)
	}
	var dumps []pendingDump
	defer func() {
		for _, d := range dumps {
			_ = os.Remove(d.file)
		}
	}()

	// 1. Safety copy.
	stSafety.Begin(safety)
	if err = os.MkdirAll(safety, 0o700); err != nil {
		stSafety.Fail(err)
		return nil, err
	}
	dumpFile, err := os.OpenFile(filepath.Join(safety, "coolify.dump"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		stSafety.Fail(err)
		return nil, err
	}
	err = in.Dump(ctx, dumpFile)
	dumpFile.Close()
	if err != nil {
		stSafety.Fail(err)
		return nil, err
	}
	oldEnv, err := os.ReadFile(coolify.EnvPath)
	if err != nil {
		stSafety.Fail(err)
		return nil, err
	}
	_ = os.WriteFile(filepath.Join(safety, "env"), oldEnv, 0o600)
	stSafety.Finish(safety)

	// 2. Stop the Coolify application (database and redis keep running) and
	// this server's current resource containers. They are only removed once
	// the restore has succeeded; a failed restore starts them again.
	stStop.Begin("")
	_ = docker.Stop(ctx, coolify.AppContainer)
	// Sentinel carries this server's token; Coolify recreates it with the
	// matching token when it starts (otherwise its metrics pushes are rejected).
	_ = docker.Remove(ctx, "coolify-sentinel")
	var oldAll, oldRunning []string
	if cs, e := docker.Containers(ctx, "label=coolify.type"); e == nil {
		for _, c := range cs {
			oldAll = append(oldAll, c.ID)
			if c.State == "running" {
				oldRunning = append(oldRunning, c.ID)
			}
		}
	}
	if len(oldRunning) > 0 {
		stStop.SetDetail(fmt.Sprintf("stopping %d resource container(s)", len(oldRunning)))
		run.Logf("stopping current resource containers: %v", oldRunning)
		if e := docker.Stop(ctx, oldRunning...); e != nil {
			pr.Warn("could not stop some containers: %v", e)
		}
	}
	stStop.Finish(fmt.Sprintf("%d resource container(s) stopped", len(oldRunning)))

	dbRestored := false
	envWritten := false
	stackRestarted := false
	undo := newUndo()
	defer func() {
		if err == nil {
			return
		}
		rctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		// Put replaced directories and volume data back.
		if errs := undo.rollback(); len(errs) > 0 {
			pr.Warn("rollback of files was incomplete: %v", errors.Join(errs...))
		}
		// Roll back the database and .env so this Coolify keeps working.
		if dbRestored {
			if df, e := os.Open(filepath.Join(safety, "coolify.dump")); e == nil {
				if e := in.RestoreDump(rctx, df); e != nil {
					pr.Warn("could not roll back the database: %v (safety copy: %s)", e, safety)
				}
				df.Close()
			}
		}
		if envWritten {
			_ = os.WriteFile(coolify.EnvPath, oldEnv, 0o600)
		}
		// The coolify container may already run with the new .env: recreate it.
		if stackRestarted {
			if e := restartStack(rctx, in); e != nil {
				pr.Warn("could not restart Coolify: %v", e)
			}
		} else {
			_ = docker.Start(rctx, coolify.AppContainer)
		}
		if len(oldRunning) > 0 {
			_ = docker.Start(rctx, oldRunning...)
		}
		pr.Warn("the restore was rolled back; this server's previous Coolify state is back (safety copy: %s)", safety)
	}()

	var sourceEnv []byte
	xp := extractPlan{
		AsideDir: filepath.Join(safety, "replaced"),
		PathTarget: func(p PathEntry) (string, string) {
			if p.Path == filepath.Join(coolify.BaseDir, "ssh") {
				return p.Path, pathMerge
			}
			if strings.HasPrefix(p.Path, coolify.BaseDir+"/") {
				return p.Path, pathReplace
			}
			return p.Path, pathReplace
		},
		VolumeTarget: func(v VolumeEntry) (VolumeEntry, bool) { return v, true },
		PathStep:     func(p PathEntry) *Step { return pathSteps[p.Path] },
		VolStep:      func(v VolumeEntry) *Step { return volSteps[v.Name] },
		ImageStep:    func(i int) *Step { return imgSteps[i] },
		Dumps:        &dumps,
		OnEntry: func(h *tar.Header, r io.Reader) (bool, error) {
			switch h.Name {
			case entryDump:
				stDB.Begin("pg_restore")
				dbRestored = true
				if err := in.RestoreDump(ctx, r); err != nil {
					stDB.Fail(err)
					if strings.Contains(err.Error(), "unsupported version") {
						return true, fmt.Errorf("the backup's database comes from a newer PostgreSQL than this Coolify uses - upgrade this Coolify first (%w)", err)
					}
					return true, fmt.Errorf("restore database: %w", err)
				}
				stDB.Finish("")
				return true, nil
			case entryEnv:
				b, err := io.ReadAll(io.LimitReader(r, 1<<20))
				sourceEnv = b
				return true, err
			case entryCustomCompose:
				b, err := io.ReadAll(io.LimitReader(r, 1<<20))
				if err != nil {
					return true, err
				}
				dst := filepath.Join(coolify.SourceDir, "docker-compose.custom.yml")
				if _, e := os.Stat(dst); e == nil {
					pr.Warn("kept this server's docker-compose.custom.yml (the source had one too)")
					return true, nil
				}
				return true, os.WriteFile(dst, b, 0o600)
			case entryDockerConfig:
				b, err := io.ReadAll(io.LimitReader(r, 4<<20))
				if err != nil {
					return true, err
				}
				if err := mergeDockerConfig(b); err != nil {
					pr.Warn("docker registry logins were not merged: %v", err)
				}
				return true, nil
			}
			return false, nil
		},
	}
	if err = extractArchive(ctx, f, xp, pr, undo); err != nil {
		return nil, err
	}
	if !dbRestored {
		err = errors.New("the backup contains no Coolify database")
		return nil, err
	}
	if err = loadDumps(ctx, dumps, func(s string) string { return s }, dumpSteps); err != nil {
		return nil, err
	}
	dumps = nil
	builderNotes, err := restoreBuilders(man, undo)
	if err != nil {
		return nil, fmt.Errorf("install buildx builders: %w", err)
	}

	// 3. APP_KEY: the restored secrets are encrypted with the source key.
	stEnv.Begin("")
	src := coolify.ParseEnv(sourceEnv)
	srcKey, _ := src.Get("APP_KEY")
	if _, e := lcrypt.ParseKey(srcKey); e != nil {
		err = fmt.Errorf("the backup has no valid APP_KEY: %w", e)
		stEnv.Fail(err)
		return nil, err
	}
	env, err := coolify.LoadEnv(coolify.EnvPath)
	if err != nil {
		stEnv.Fail(err)
		return nil, err
	}
	targetKey, _ := env.Get("APP_KEY")
	prev := []string{}
	if p, ok := src.Get("APP_PREVIOUS_KEYS"); ok && p != "" {
		prev = append(prev, strings.Split(p, ",")...)
	}
	if targetKey != "" && targetKey != srcKey {
		prev = append(prev, targetKey)
	}
	changed := []string{}
	for _, k := range src.Keys() {
		if envKeepTarget[k] {
			continue
		}
		v, _ := src.Get(k)
		if old, ok := env.Get(k); !ok || old != v {
			env.Set(k, v)
			changed = append(changed, k)
		}
	}
	env.Set("APP_KEY", srcKey)
	if len(prev) > 0 {
		env.Set("APP_PREVIOUS_KEYS", strings.Join(uniq(prev), ","))
	}
	if _, err = env.Save(); err != nil {
		stEnv.Fail(err)
		return nil, err
	}
	envWritten = true
	run.Logf("env keys taken from the source: %v", changed)
	stEnv.Finish("APP_KEY set")

	// 4. SSH: Coolify reaches this host over SSH with the restored localhost key.
	stSSH.Begin("")
	srcCrypt, _ := lcrypt.New(srcKey, strings.Join(prev, ","))
	if note, e := authorizeLocalKey(ctx, in, srcCrypt); e != nil {
		pr.Warn("could not authorize the localhost SSH key: %v - validate the server in Coolify afterwards", e)
		stSSH.Fail(e)
	} else {
		stSSH.Finish(note)
	}

	// 5. Start Coolify with the new key; flush stale queue jobs first.
	stStart.Begin("recreating containers")
	flushRedis(ctx, in)
	stackRestarted = true
	if err = restartStack(ctx, in); err != nil {
		stStart.Fail(err)
		return nil, err
	}
	stStart.SetDetail("waiting until healthy")
	if err = waitHealthy(ctx, coolify.AppContainer, 6*time.Minute); err != nil {
		stStart.Fail(err)
		return nil, err
	}
	if err = failpoint("full-after-restart"); err != nil {
		stStart.Fail(err)
		return nil, err
	}
	// Committed. The stopped containers of the replaced configuration are
	// removed only now, so every earlier failure could start them again.
	if len(oldAll) > 0 {
		if e := docker.Remove(ctx, oldAll...); e != nil {
			pr.Warn("could not remove some old containers: %v", e)
		}
	}
	// The proxy may still run with this server's previous configuration.
	restartProxy(ctx, in, pr)
	stStart.Finish("healthy")

	stDeps.Begin("")
	problems := checkHostDeps(ctx, man, func(s string) string { return s }, func(string) bool { return false })
	if len(problems) > 0 {
		stDeps.Fail(fmt.Errorf("%d missing", len(problems)))
	} else {
		stDeps.Finish(fmt.Sprintf("%d checked", len(man.HostDeps)))
	}

	notes := append([]string{"a safety copy of the previous Coolify database and .env is in " + safety + " (delete it from the menu, 'Saved files & disk space', once everything works)"}, builderNotes...)
	if av := undo.asideVolumes(); len(av) > 0 {
		notes = append(notes, "previous data of volumes that already existed is kept in: "+strings.Join(av, ", ")+" (delete them from the menu, 'Saved files & disk space', once everything works)")
	}
	return &RestoreReport{Resources: fullResources(ctx, in, man), Notes: notes, Problems: problems, AsideDir: safety, Duration: time.Since(start)}, nil
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// authorizeLocalKey adds the public key of the restored "localhost" private key
// to the authorized_keys of the configured user.
func authorizeLocalKey(ctx context.Context, in *coolify.Instance, srcCrypt *lcrypt.Encrypter) (string, error) {
	var rows []struct {
		User string `json:"user"`
		Key  string `json:"private_key"`
	}
	if err := in.Query(ctx, fmt.Sprintf(`SELECT s."user", pk.private_key FROM servers s JOIN private_keys pk ON pk.id = s.private_key_id WHERE s.id = %d`, coolify.LocalServerID), &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", errors.New("the localhost server has no private key")
	}
	pem := rows[0].Key
	if lcrypt.LooksEncrypted(pem) {
		plain, err := srcCrypt.DecryptString(pem)
		if err != nil {
			return "", err
		}
		pem = strings.TrimSpace(decodeMaybeSerialized(plain))
	}
	signer, err := ssh.ParsePrivateKey([]byte(pem + "\n"))
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}
	pub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	username := rows[0].User
	if username == "" {
		username = "root"
	}
	u, err := user.Lookup(username)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(u.HomeDir, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	file := filepath.Join(dir, "authorized_keys")
	cur, _ := os.ReadFile(file)
	if bytes.Contains(cur, []byte(pub)) {
		return "already authorized", nil
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	line := pub + " coolify-mirror-restored-localhost-key\n"
	if len(cur) > 0 && !bytes.HasSuffix(cur, []byte("\n")) {
		line = "\n" + line
	}
	if _, err := f.WriteString(line); err != nil {
		return "", err
	}
	var uid, gid int
	fmt.Sscan(u.Uid, &uid)
	fmt.Sscan(u.Gid, &gid)
	_ = os.Chown(dir, uid, gid)
	_ = os.Chown(file, uid, gid)
	return "added for user " + username, nil
}

func decodeMaybeSerialized(b []byte) string {
	s := string(b)
	if strings.HasPrefix(s, "s:") {
		if i := strings.Index(s, ":\""); i > 0 && strings.HasSuffix(s, "\";") {
			return s[i+2 : len(s)-2]
		}
	}
	return s
}

func mergeDockerConfig(src []byte) error {
	const path = "/root/.docker/config.json"
	var in map[string]any
	if err := json.Unmarshal(src, &in); err != nil {
		return err
	}
	cur := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &cur); err != nil {
			return err
		}
	}
	srcAuths, _ := in["auths"].(map[string]any)
	if len(srcAuths) == 0 {
		return nil
	}
	dst, _ := cur["auths"].(map[string]any)
	if dst == nil {
		dst = map[string]any{}
	}
	for k, v := range srcAuths {
		if _, ok := dst[k]; !ok {
			dst[k] = v
		}
	}
	cur["auths"] = dst
	out, err := json.MarshalIndent(cur, "", "\t")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

func flushRedis(ctx context.Context, in *coolify.Instance) {
	// The password travels over stdin (argv is visible in `ps`).
	pass := in.Env.Value("REDIS_PASSWORD", "")
	script := `REDISCLI_AUTH="$(cat)"; export REDISCLI_AUTH; redis-cli --no-auth-warning FLUSHALL`
	if _, err := run.Do(ctx, run.Spec{Name: "docker", Args: []string{"exec", "-i", coolify.RedisContainer, "sh", "-c", script},
		Stdin: strings.NewReader(pass)}); err != nil {
		run.Logf("redis flush: %v", err)
	}
}

// restartStack recreates Coolify's containers like the official upgrade script.
func restartStack(ctx context.Context, in *coolify.Instance) error {
	env, err := coolify.LoadEnv(coolify.EnvPath)
	if err != nil {
		return err
	}
	files := []string{"-f", filepath.Join(coolify.SourceDir, "docker-compose.yml"), "-f", filepath.Join(coolify.SourceDir, "docker-compose.prod.yml")}
	for _, extra := range []string{"docker-compose.custom.yml", "docker-compose.postgres-upgrade.yml"} {
		if _, err := os.Stat(filepath.Join(coolify.SourceDir, extra)); err == nil {
			files = append(files, "-f", filepath.Join(coolify.SourceDir, extra))
		}
	}
	latest := env.Value("LATEST_IMAGE", in.Version)
	registry := env.Value("REGISTRY_URL", "docker.io")
	compose := append([]string{"compose", "--env-file", coolify.EnvPath}, files...)
	compose = append(compose, "up", "-d", "--remove-orphans", "--force-recreate")
	extraEnv := []string{"LATEST_IMAGE=" + latest, "REGISTRY_URL=" + registry}
	if _, err := run.Do(ctx, run.Spec{Name: "docker", Args: []string{"compose", "version"}}); err == nil {
		_, err := run.Do(ctx, run.Spec{Name: "docker", Args: compose, Env: extraEnv})
		return err
	}
	// No compose plugin on the host: use Coolify's helper image like upgrade.sh does.
	helper, _ := run.Text(ctx, "sh", "-c", "docker images --format '{{.Repository}}:{{.Tag}}' | grep coolify-helper | head -n1")
	if helper == "" {
		return errors.New("docker compose is not available and no coolify-helper image was found")
	}
	cmd := "LATEST_IMAGE=" + latest + " REGISTRY_URL=" + registry + " docker " + strings.Join(compose, " ")
	_, err = run.Output(ctx, "docker", "run", "--rm", "-v", "/data/coolify/source:/data/coolify/source",
		"-v", "/var/run/docker.sock:/var/run/docker.sock", helper, "bash", "-c", cmd)
	return err
}

func waitHealthy(ctx context.Context, container string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		st, _ := coolify.ContainerState(ctx, container)
		h := docker.Health(ctx, container)
		if st == "running" && (h == "healthy" || h == "") {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("%s did not become healthy within %s (check: docker logs %s)", container, timeout, container)
}

func restartProxy(ctx context.Context, in *coolify.Instance, pr *Progress) {
	var rows []struct {
		Network string `json:"network"`
	}
	if err := in.Query(ctx, fmt.Sprintf("SELECT network FROM standalone_dockers WHERE server_id = %d", coolify.LocalServerID), &rows); err == nil {
		for _, r := range rows {
			if err := docker.EnsureNetwork(ctx, r.Network); err != nil {
				pr.Warn("network %s: %v", r.Network, err)
			}
		}
	}
	composeFile := filepath.Join(coolify.BaseDir, "proxy", "docker-compose.yml")
	if _, err := os.Stat(composeFile); err == nil {
		if _, err := run.Output(ctx, "docker", "compose", "-f", composeFile, "up", "-d", "--force-recreate", "--remove-orphans"); err != nil {
			pr.Warn("could not restart the proxy with the restored configuration: %v", err)
		}
		return
	}
	var out struct {
		Started bool `json:"started"`
	}
	if err := in.PHP(ctx, "ensure_proxy", nil, &out); err != nil {
		pr.Warn("proxy: %v", err)
	}
}

// fullResources lists the restored resources on this server, each application
// with its last successful deployment (restored with the database), so Docker
// Compose applications start from the restored images instead of a rebuild.
func fullResources(ctx context.Context, in *coolify.Instance, man *Manifest) []dbx.PlannedResource {
	var deps []struct {
		App    string  `json:"application_id"`
		Commit *string `json:"commit"`
		UUID   string  `json:"deployment_uuid"`
	}
	if err := in.Query(ctx, `SELECT DISTINCT ON (application_id) application_id, commit, deployment_uuid
FROM application_deployment_queues WHERE status = 'finished' AND pull_request_id = 0
ORDER BY application_id, created_at DESC, id DESC`, &deps); err != nil {
		run.Logf("last deployments: %v", err)
	}
	last := map[string]int{}
	for i, d := range deps {
		last[d.App] = i
	}
	var out []dbx.PlannedResource
	for _, r := range man.Resources {
		if !r.Local() {
			continue
		}
		pl := dbx.PlannedResource{Resource: r, SourceUUID: r.UUID, WasRunning: r.Running() || len(man.Runtime[r.UUID]) > 0, Expect: man.Runtime[r.UUID]}
		if i, ok := last[strconv.FormatInt(r.ID, 10)]; ok && r.Table == "applications" {
			if c := deps[i].Commit; c != nil {
				pl.Commit = *c
			}
			pl.DeploymentUUID = deps[i].UUID
		}
		out = append(out, pl)
	}
	return out
}
