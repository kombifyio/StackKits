package engine

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
	"github.com/mtalavi/coolify-mirror/internal/dbx"
	"github.com/mtalavi/coolify-mirror/internal/docker"
	"github.com/mtalavi/coolify-mirror/internal/run"
)

// A resource is reported as running only after its containers stayed up
// (healthy where they have a healthcheck, one-shot jobs finished with exit 0)
// for stableWindow without restarting, the services that ran on the source
// are all there, and each of its domains answers through the local proxy.
var (
	stableWindow  = 30 * time.Second
	settleTimeout = 6 * time.Minute // after Coolify finished deploying
	routeTimeout  = 2 * time.Minute
)

// StartResult is the outcome of starting one resource.
type StartResult struct {
	Resource dbx.PlannedResource
	OK       bool
	// Stopped: deliberately not started (stopped on the source, or its
	// domain is used by another resource here).
	Stopped bool
	Message string
}

type startItem struct {
	res        dbx.PlannedResource
	rebuild    bool // force a fresh build (redeploy check)
	step       *Step
	deployUUID string
	done       bool
	ok         bool
	msg        string
	deadline   time.Time
	settled    bool // Coolify finished its part (deployment / start request)
	started    time.Time

	stableSince time.Time
	restarts    map[string]int
	routeSince  time.Time
	problem     string // last thing that was wrong (for the timeout message)
	direct      bool   // started from the restored images, without a build
	note        string // why Coolify had to build it after all
}

// startEnv is what the checks need besides the resource.
type startEnv struct {
	in         *coolify.Instance
	proxyCheck bool // a proxy runs here: domains are probed through it
}

// StartResources asks Coolify to start/deploy the restored resources (databases
// first, then services, then applications) and waits until they run.
// Resources that were stopped on the source stay stopped.
func StartResources(ctx context.Context, in *coolify.Instance, resources []dbx.PlannedResource, pr *Progress) (out []StartResult, err error) {
	defer func() { pr.End(err) }()
	unlock, err := Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	stServer := pr.Add("Check that Coolify can use this server", 0)
	stProxy := pr.Add("Check the proxy", 0)
	var dbs, svcs, apps []*startItem
	for _, r := range resources {
		it := &startItem{res: r}
		if r.Hold != "" {
			s := pr.Add("Start  "+r.Name, 0)
			s.SkipStep("domain in use")
			out = append(out, StartResult{Resource: r, OK: true, Stopped: true, Message: r.Hold})
			continue
		}
		if !r.WasRunning {
			s := pr.Add("Start  "+r.Name, 0)
			s.SkipStep("was stopped on the source")
			out = append(out, StartResult{Resource: r, OK: true, Stopped: true, Message: "left stopped (it was stopped on the source)"})
			continue
		}
		it.step = pr.Add(fmt.Sprintf("Start  %s (%s)", r.Name, r.Label()), 0)
		switch {
		case r.IsDatabase():
			dbs = append(dbs, it)
		case r.Table == "services":
			svcs = append(svcs, it)
		default:
			apps = append(apps, it)
		}
	}
	stServer.Begin("")
	if err := in.EnsureLocalServer(ctx); err != nil {
		stServer.Fail(err)
		return nil, err
	}
	stServer.Finish("ready")
	stProxy.Begin("")
	env := &startEnv{in: in}
	var px struct {
		Type    string `json:"proxy_type"`
		Started bool   `json:"started"`
	}
	if err := in.PHP(ctx, "ensure_proxy", nil, &px); err != nil {
		stProxy.Fail(err)
		pr.Warn("proxy check failed: %v", err)
	} else {
		if px.Started {
			stProxy.Finish(px.Type + " started")
		} else {
			stProxy.Finish(px.Type + " running")
		}
		st, _ := coolify.ContainerState(ctx, coolify.ProxyContainer)
		env.proxyCheck = st == "running" && !strings.EqualFold(px.Type, "none")
	}

	if err := dispatch(ctx, in, append(dbs, svcs...)); err != nil {
		return nil, err
	}
	waitAll(ctx, env, dbs, 8*time.Minute)
	if err := dispatch(ctx, in, apps); err != nil {
		return nil, err
	}
	waitAll(ctx, env, append(svcs, apps...), 30*time.Minute)

	for _, group := range [][]*startItem{dbs, svcs, apps} {
		for _, it := range group {
			out = append(out, StartResult{Resource: it.res, OK: it.ok, Message: it.msg})
		}
	}
	return out, nil
}

// NeedsBuild reports applications that Coolify builds itself (from git or a
// Dockerfile); a restored image lets them start without a build, so only a
// rebuild proves that the next deploy works on this server.
func NeedsBuild(r dbx.PlannedResource) bool {
	return r.Table == "applications" && r.BuildPack != "" && r.BuildPack != "dockerimage"
}

// VerifyRedeploy rebuilds the restored applications that Coolify builds
// itself, from the same commit and without the build cache, and checks again
// that they run: proof that the next deploy on this server needs nothing from
// the source. Resources that are not running are skipped.
func VerifyRedeploy(ctx context.Context, in *coolify.Instance, resources []dbx.PlannedResource, pr *Progress) (out []StartResult, err error) {
	defer func() { pr.End(err) }()
	unlock, err := Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	env := &startEnv{in: in}
	if st, _ := coolify.ContainerState(ctx, coolify.ProxyContainer); st == "running" {
		env.proxyCheck = true
	}
	var items []*startItem
	for _, r := range resources {
		if !NeedsBuild(r) || r.Hold != "" || !r.WasRunning {
			continue
		}
		items = append(items, &startItem{res: r, rebuild: true, step: pr.Add("Rebuild and redeploy  "+r.Name, 0)})
	}
	if err := dispatch(ctx, in, items); err != nil {
		return nil, err
	}
	waitAll(ctx, env, items, 45*time.Minute)
	for _, it := range items {
		msg := it.msg
		if it.ok {
			msg = "rebuilt on this server and " + msg
		}
		out = append(out, StartResult{Resource: it.res, OK: it.ok, Message: msg})
	}
	return out, nil
}

func dispatch(ctx context.Context, in *coolify.Instance, items []*startItem) error {
	if len(items) == 0 {
		return nil
	}
	var req []map[string]string
	var queued []*startItem
	for _, it := range items {
		if it.res.Table == "applications" && it.res.BuildPack == "dockercompose" && !it.rebuild {
			it.step.Begin("starting from the restored images")
			it.started = time.Now()
			dep, reason := directComposeStart(ctx, in, it.res.UUID, it.res.Commit, it.res.Expect)
			if reason == "" {
				it.direct, it.deployUUID, it.settled = true, dep, true
				it.deadline = time.Now().Add(settleTimeout)
				it.step.SetDetail("started from the restored images")
				continue
			}
			it.note = "Coolify built it on this server: " + reason
			run.Logf("%s: %s", it.res.Name, it.note)
		}
		queued = append(queued, it)
		m := map[string]string{"uuid": it.res.UUID, "table": it.res.Table}
		switch {
		case it.res.IsDatabase():
			m["type"] = "database"
		case it.res.Table == "services":
			m["type"] = "service"
		default:
			m["type"] = "application"
			if c := it.res.Commit; c != "" && c != "HEAD" {
				m["commit"] = c
			}
			if it.rebuild {
				m["force_rebuild"] = "1"
			}
		}
		req = append(req, m)
		it.step.Begin("asking Coolify")
		it.started = time.Now()
	}
	if len(req) == 0 {
		return nil
	}
	items = queued
	var res struct {
		Items []struct {
			UUID       string `json:"uuid"`
			OK         bool   `json:"ok"`
			Error      string `json:"error"`
			Deployment string `json:"deployment_uuid"`
			Message    string `json:"message"`
		} `json:"items"`
	}
	if err := in.PHP(ctx, "start", req, &res); err != nil {
		for _, it := range items {
			it.step.Fail(err)
			it.done, it.msg = true, err.Error()
		}
		return nil
	}
	byUUID := map[string]*startItem{}
	for _, it := range items {
		byUUID[it.res.UUID] = it
	}
	for _, r := range res.Items {
		it := byUUID[r.UUID]
		if it == nil {
			continue
		}
		if !r.OK {
			msg := r.Error
			if msg == "" {
				msg = r.Message
			}
			it.step.Fail(fmt.Errorf("%s", msg))
			it.done, it.msg = true, msg
			continue
		}
		it.deployUUID = r.Deployment
		it.step.SetDetail("starting")
	}
	for _, it := range items {
		if it.done {
			continue
		}
		if it.res.Table == "applications" && it.deployUUID == "" {
			it.done, it.msg = true, "Coolify did not queue a deployment"
			it.step.Fail(fmt.Errorf("%s", it.msg))
			continue
		}
		// Services and databases are started synchronously by Coolify; an
		// application settles when its deployment finishes.
		it.settled = it.res.Table != "applications"
		it.deadline = time.Now().Add(settleTimeout)
	}
	return nil
}

func waitAll(ctx context.Context, env *startEnv, items []*startItem, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		pending := 0
		for _, it := range items {
			if it.done {
				continue
			}
			pending++
			check(ctx, env, it)
			if it.done {
				continue
			}
			now := time.Now()
			if (it.settled && now.After(it.deadline)) || now.After(deadline) {
				it.done, it.ok = true, false
				it.msg = "not running properly after " + HumanDuration(time.Since(it.started))
				if it.problem != "" {
					it.msg = it.problem + " - " + it.msg
				}
				it.msg += " - check it in Coolify"
				if it.note != "" {
					it.msg += " (" + it.note + ")"
				}
				it.step.Fail(fmt.Errorf("%s", it.msg))
			}
		}
		if pending == 0 || ctx.Err() != nil {
			return
		}
		time.Sleep(3 * time.Second)
	}
}

// container states
const (
	stateUp       = iota // running; healthy or without a healthcheck
	stateStarting        // running (or created), healthcheck not decided yet
	stateDone            // a one-shot job that finished with exit code 0
	stateBad             // exited with an error, unhealthy, restarting, paused…
)

func containerState(d docker.Details) int {
	s := d.State
	switch {
	case s.Status == "running" && s.Running:
		switch d.HealthStatus() {
		case "", "healthy":
			return stateUp
		case "starting":
			return stateStarting
		}
		return stateBad
	case s.Status == "created":
		return stateStarting
	case s.Status == "exited" && s.ExitCode == 0:
		// Only a container that is not meant to be restarted is a finished job
		// (migrations, init steps); a stopped long-running service is not.
		switch d.HostConfig.RestartPolicy.Name {
		case "", "no", "on-failure":
			return stateDone
		}
	}
	return stateBad
}

func describeState(d docker.Details) string {
	s := d.State
	switch {
	case s.Status == "running" && s.Running:
		if h := d.HealthStatus(); h != "" && h != "healthy" {
			return h
		}
		return "running"
	case s.Status == "exited":
		return fmt.Sprintf("exited (%d)", s.ExitCode)
	}
	return s.Status
}

func serviceName(d docker.Details) string {
	s := d.Config.Labels["com.docker.compose.service"]
	if name := strings.TrimPrefix(d.Name, "/"); s == "" || s == name {
		return stableService(name)
	}
	return s
}

// deploySuffix is the deployment time Coolify appends to container (and,
// for single-container applications, service) names: <name>-<12 digits>
// before Coolify 4.4, <name>-<YYYYMMDD>T<HHMMSS> since.
var deploySuffix = regexp.MustCompile(`-(\d{12}|\d{8}T\d{6})$`)

// stableService removes the per-deployment suffix, so the same service has
// the same name on the source and after a restore.
func stableService(s string) string { return deploySuffix.ReplaceAllString(s, "") }

// resourceContainers inspects the containers of a resource (preview
// deployments are not part of the mirror).
func resourceContainers(ctx context.Context, uuid string) ([]docker.Details, error) {
	cs, err := docker.Containers(ctx, "label=com.docker.compose.project="+uuid)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, c := range cs {
		if pr := c.Label("coolify.pullRequestId"); pr == "" || pr == "0" {
			ids = append(ids, c.ID)
		}
	}
	return docker.Inspect(ctx, ids...)
}

// judge summarizes containers: ready when every container is up or a
// finished job and every expected service is present.
func judge(ds []docker.Details, expect []string) (ready bool, detail string) {
	if len(ds) == 0 {
		return false, "waiting for containers"
	}
	// have: service names as they are. generated: single-container apps,
	// whose service is named like the container (<name>-<deploy time>), by the
	// name without the deploy time, which changes with every deployment.
	have, generated := map[string]bool{}, map[string]bool{}
	var bad, starting []string
	for _, d := range ds {
		svc := d.Config.Labels["com.docker.compose.service"]
		name := strings.TrimPrefix(d.Name, "/")
		if svc == "" || svc == name {
			generated[stableService(name)] = true
			svc = name
		}
		have[svc] = true
		switch containerState(d) {
		case stateBad:
			bad = append(bad, serviceName(d)+" "+describeState(d))
		case stateStarting:
			starting = append(starting, serviceName(d)+" "+describeState(d))
		}
	}
	var missing []string
	for _, s := range expect {
		// A Compose service may itself be called worker-20261008T010203: only
		// generated names are compared without the deploy time.
		if !have[s] && !(deploySuffix.MatchString(s) && generated[stableService(s)]) {
			missing = append(missing, stableService(s))
		}
	}
	sort.Strings(bad)
	switch {
	case len(bad) > 0:
		return false, strings.Join(bad, ", ")
	case len(missing) > 0:
		return false, "service(s) not created: " + strings.Join(missing, ", ")
	case len(starting) > 0:
		return false, strings.Join(starting, ", ")
	}
	return true, ""
}

func check(ctx context.Context, env *startEnv, it *startItem) {
	if !it.settled {
		st, _ := env.in.Scalar(ctx, "SELECT status FROM application_deployment_queues WHERE deployment_uuid = "+coolify.SQLString(it.deployUUID))
		switch st {
		case "queued":
			it.step.SetDetail("deployment queued")
			return
		case "in_progress":
			it.step.SetDetail("deploying")
			return
		case "failed", "cancelled-by-user":
			it.done, it.ok = true, false
			it.msg = "deployment " + st + " - open the deployment log in Coolify"
			if it.note != "" {
				it.msg += " (" + it.note + ")"
			}
			it.step.Fail(fmt.Errorf("%s", it.msg))
			return
		case "finished":
			it.settled = true
			it.deadline = time.Now().Add(settleTimeout)
		default:
			it.step.SetDetail("deployment " + st)
			return
		}
	}
	ds, err := resourceContainers(ctx, it.res.UUID)
	if err != nil {
		it.step.SetDetail("waiting for containers")
		return
	}
	ready, detail := judge(ds, it.res.Expect)
	if !ready {
		it.stableSince, it.routeSince = time.Time{}, time.Time{}
		if detail != "waiting for containers" {
			it.problem = detail
		}
		it.step.SetDetail(detail)
		return
	}
	attachProxy(ctx, ds)
	counts := map[string]int{}
	for _, d := range ds {
		counts[d.ID] = d.RestartCount
	}
	if it.stableSince.IsZero() || restarted(it.restarts, counts) {
		if !it.stableSince.IsZero() {
			it.problem = "a container restarted"
		}
		it.stableSince, it.restarts, it.routeSince = time.Now(), counts, time.Time{}
		it.step.SetDetail("containers up, watching")
		return
	}
	if up := time.Since(it.stableSince); up < stableWindow {
		it.step.SetDetail(fmt.Sprintf("containers up for %ds", int(up.Seconds())))
		return
	}
	if env.proxyCheck && len(it.res.Domains) > 0 {
		if it.routeSince.IsZero() {
			it.routeSince = time.Now()
		}
		if bad := checkRoutes(ctx, it.res.Domains); len(bad) > 0 {
			it.problem = strings.Join(bad, "; ")
			if time.Since(it.routeSince) > routeTimeout {
				it.done, it.ok = true, false
				it.msg = it.problem
				it.step.Fail(fmt.Errorf("%s", it.msg))
				return
			}
			it.step.SetDetail(it.problem)
			return
		}
	}
	it.done, it.ok = true, true
	total := len(ds)
	it.msg = fmt.Sprintf("running (%d container%s stable", total, plural(total))
	if env.proxyCheck && len(it.res.Domains) > 0 {
		it.msg += ", domains answer through the proxy"
	}
	it.msg += ")"
	it.step.Finish(strings.TrimPrefix(it.msg, "running "))
	switch {
	case it.direct:
		it.msg += " - started from the restored images, without a build"
	case it.note != "":
		it.msg += " - " + it.note
	}
}

func restarted(before, now map[string]int) bool {
	if len(before) != len(now) {
		return true
	}
	for id, n := range now {
		if b, ok := before[id]; !ok || n > b {
			return true
		}
	}
	return false
}

// attachProxy makes sure the proxy shares a network with every container it
// routes to. Coolify connects it itself, but "docker network connect … ||
// true" can fail silently right after a network is created (the domain then
// answers 502/504).
func attachProxy(ctx context.Context, ds []docker.Details) {
	px, err := docker.Inspect(ctx, coolify.ProxyContainer)
	if err != nil || len(px) == 0 {
		return
	}
	on := map[string]bool{}
	for _, n := range px[0].Networks() {
		on[n] = true
	}
	for _, d := range ds {
		if d.Config.Labels["traefik.enable"] != "true" && d.Config.Labels["caddy"] == "" {
			continue
		}
		want := d.Config.Labels["traefik.docker.network"]
		var nets []string
		if want != "" {
			nets = []string{want}
		} else {
			for _, n := range d.Networks() {
				if n != "bridge" && n != "host" && n != "none" {
					nets = append(nets, n)
				}
			}
		}
		shared := false
		for _, n := range nets {
			shared = shared || on[n]
		}
		if shared || len(nets) == 0 {
			continue
		}
		if err := docker.Connect(ctx, nets[0], coolify.ProxyContainer); err != nil {
			run.Logf("connect proxy to %s: %v", nets[0], err)
			continue
		}
		on[nets[0]] = true
	}
}

// checkRoutes sends a request for each domain through the local proxy and
// returns the domains where the proxy answers instead of the application.
func checkRoutes(ctx context.Context, domains []string) []string {
	var bad []string
	for _, d := range domains {
		if p := probeRoute(ctx, d); p != "" {
			bad = append(bad, coolify.Host(d)+": "+p)
		}
	}
	return bad
}

// probeRoute returns "" when the request reached the application.
func probeRoute(ctx context.Context, domain string) string {
	target, resolve, ok := probeTarget(domain)
	if !ok {
		return ""
	}
	// -k: the certificate may not be issued yet (DNS still points to the old
	// server); only the routing on this host is checked, nothing is sent.
	out, err := run.Text(ctx, "curl", "-sS", "-k", "--max-time", "15", "-o", "-", "-w", "\n%{http_code}",
		"--resolve", resolve, target)
	if err != nil && out == "" {
		return "the proxy does not answer (" + firstLine(err.Error()) + ")"
	}
	body, codeText := out, out
	if i := strings.LastIndexByte(out, '\n'); i >= 0 {
		body, codeText = out[:i], out[i+1:]
	}
	code, _ := strconv.Atoi(strings.TrimSpace(codeText))
	return proxyError(code, body)
}

// probeTarget turns a Coolify domain into the URL to request through the
// local proxy and the matching curl --resolve value. A port in a Coolify
// domain is the container port the proxy forwards to; the proxy itself
// always listens on 80/443.
func probeTarget(domain string) (target, resolve string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(domain))
	if err != nil || u.Hostname() == "" {
		return "", "", false
	}
	scheme, port := "https", "443"
	if u.Scheme == "http" {
		scheme, port = "http", "80"
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	host := u.Hostname()
	return scheme + "://" + host + ":" + port + path, host + ":" + port + ":127.0.0.1", true
}

// proxyError recognizes the answers the proxy gives when it cannot hand the
// request to the application.
func proxyError(code int, body string) string {
	b := strings.TrimSpace(body)
	switch {
	case code == 0:
		return "no answer"
	case code == 503 && strings.Contains(strings.ToLower(b), "no available server"):
		return "the proxy has no healthy container for it (503 no available server)"
	case code == 404 && b == "404 page not found":
		return "the proxy has no route for it (404)"
	case code == 502 || code == 504:
		return fmt.Sprintf("the proxy cannot reach the application (%d)", code)
	}
	return ""
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// Verdict returns "" when the restore may be called a success, otherwise
// what is wrong: resources that do not run properly, resources held back by
// a domain clash, or dependencies a later deploy needs.
func Verdict(failed, held int, problems []string) string {
	var parts []string
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d resource(s) are not running properly", failed))
	}
	if held > 0 {
		parts = append(parts, fmt.Sprintf("%d resource(s) were not started because their domain is used here", held))
	}
	if len(problems) > 0 {
		parts = append(parts, fmt.Sprintf("%d build/runtime dependency problem(s)", len(problems)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Restored, but NOT operational: " + strings.Join(parts, "; ") + " - see above and the Coolify dashboard"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// DashboardLink builds the Coolify UI link of a resource.
func DashboardLink(base string, r dbx.PlannedResource) string {
	base = strings.TrimRight(base, "/")
	kind := "application"
	switch {
	case r.Table == "services":
		kind = "service"
	case r.IsDatabase():
		kind = "database"
	}
	return fmt.Sprintf("%s/project/%s/environment/%s/%s/%s", base, r.ProjectUUID, r.EnvironmentUUID, kind, r.UUID)
}
