// Package coolify relocates Coolify workloads with the pinned coolify-mirror
// fork in third_party/coolify-mirror. StackKits runs the engine as a separate
// process on the server it is installed on and accepts only the pinned build.
package coolify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/workloadrelocation"
)

// Pinned engine build. Keep in step with third_party/coolify-mirror
// (internal/engine/manifest.go and KOMBIFY-FORK.md).
var Pinned = workloadrelocation.Engine{
	Name:           "coolify-mirror",
	Version:        "1.9.3",
	UpstreamCommit: "f46fa093644268759fc0802e7d098e9e45dee984",
	ForkRevision:   "kombify.5",
}

// Engine document formats the adapter accepts.
const (
	planFormat  = "kombify.io/coolify-relocation-plan/v1"
	checkFormat = "kombify.io/coolify-relocation-check/v1"
)

// EngineEnv overrides where the engine binary is looked up.
const EngineEnv = "STACKKIT_COOLIFY_MIRROR"

// DefaultEnginePath is where StackKits installs the engine on a node.
const DefaultEnginePath = "/usr/local/lib/stackkit/coolify-mirror"

// exitNotReady is the engine's exit code for a blocked check.
const exitNotReady = 3

// maxDocument bounds what is read from the engine.
const maxDocument = 64 << 20

// Selection chooses what to relocate (mirrors the engine's backup flags).
type Selection struct {
	Full     bool
	All      bool
	Projects []string
	Domains  []string
	UUIDs    []string
	NoDeps   bool
	Images   string
}

func (s Selection) args() ([]string, error) {
	var out []string
	if s.Full {
		if s.All || len(s.Projects)+len(s.Domains)+len(s.UUIDs) > 0 {
			return nil, errors.New("--full moves the whole Coolify; it takes no resource selection")
		}
		out = append(out, "--full")
	} else {
		if !s.All && len(s.Projects)+len(s.Domains)+len(s.UUIDs) == 0 {
			return nil, errors.New("select what to relocate: --project, --domain, --uuid, --all or --full")
		}
		if s.All {
			out = append(out, "--all")
		}
		for _, f := range []struct {
			flag   string
			values []string
		}{{"--project", s.Projects}, {"--domain", s.Domains}, {"--uuid", s.UUIDs}} {
			if len(f.values) > 0 {
				out = append(out, f.flag, strings.Join(f.values, ","))
			}
		}
		if s.NoDeps {
			out = append(out, "--no-deps")
		}
	}
	if s.Images != "" {
		out = append(out, "--images", s.Images)
	}
	return out, nil
}

// Runner is the pinned engine on this server.
type Runner struct {
	path string
}

// Locate finds the engine (explicit path, EngineEnv, DefaultEnginePath, then
// PATH) and refuses any build other than the pinned one.
func Locate(ctx context.Context, explicit string) (*Runner, error) {
	path, err := lookup(explicit)
	if err != nil {
		return nil, err
	}
	r := &Runner{path: path}
	out, _, code, err := r.run(ctx, nil, "version", "--json")
	if err != nil || code != 0 {
		return nil, fmt.Errorf("coolify-mirror at %s did not report its build (exit %d): %w", path, code, errOr(err))
	}
	var got workloadrelocation.Engine
	if err := json.Unmarshal(out, &got); err != nil {
		return nil, fmt.Errorf("coolify-mirror at %s is not the kombify fork (no build identity): %w", path, err)
	}
	if got != Pinned {
		return nil, fmt.Errorf("coolify-mirror at %s is %s %s (%s, upstream %s) but this StackKits release pins %s %s (upstream %s); install the engine shipped with this StackKits release",
			path, got.Version, got.ForkRevision, got.Name, short(got.UpstreamCommit), Pinned.Version, Pinned.ForkRevision, short(Pinned.UpstreamCommit))
	}
	return r, nil
}

// enginePaths lists where an installed StackKits release keeps the engine:
// ../lib/stackkit beside the running CLI (install.sh, also for non-root
// installs), then the system default.
func enginePaths() []string {
	var paths []string
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		paths = append(paths, filepath.Join(filepath.Dir(exe), "..", "lib", "stackkit", "coolify-mirror"))
	}
	return append(paths, DefaultEnginePath)
}

func lookup(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if p := strings.TrimSpace(os.Getenv(EngineEnv)); p != "" {
		return p, nil
	}
	for _, candidate := range enginePaths() {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	if p, err := exec.LookPath("coolify-mirror"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("the Coolify relocation engine is not installed: expected %s, %s or coolify-mirror on PATH; it ships in the Linux StackKits release archive", strings.Join(enginePaths(), ", "), EngineEnv)
}

// Plan inventories the selection on this (source) server and returns the
// engine's plan document unchanged, ready to hand to Check on the destination.
func (r *Runner) Plan(ctx context.Context, sel Selection) (json.RawMessage, error) {
	args, err := sel.args()
	if err != nil {
		return nil, err
	}
	out, stderr, code, err := r.run(ctx, nil, append([]string{"plan", "--json"}, args...)...)
	if err != nil {
		return nil, err
	}
	if code != 0 && code != exitNotReady {
		return nil, engineFailure("plan", code, stderr)
	}
	var head struct {
		Format string                    `json:"format"`
		Tool   workloadrelocation.Engine `json:"tool"`
	}
	if err := json.Unmarshal(out, &head); err != nil || head.Format != planFormat {
		return nil, fmt.Errorf("coolify-mirror plan returned no %s document", planFormat)
	}
	if head.Tool != Pinned {
		return nil, errors.New("coolify-mirror plan was made by another build than the pinned engine")
	}
	return json.RawMessage(out), nil
}

// Check verifies on this (destination) server whether plan can be restored.
// A blocked move is a valid result (Ready false), not an error.
func (r *Runner) Check(ctx context.Context, plan []byte) (*workloadrelocation.Check, error) {
	var head struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(plan, &head); err != nil || head.Format != planFormat {
		return nil, fmt.Errorf("not a Coolify relocation plan (%s); make it with `stackkit relocate plan` on the source server", planFormat)
	}
	out, stderr, code, err := r.run(ctx, plan, "check", "--json", "--plan", "-")
	if err != nil {
		return nil, err
	}
	if code != 0 && code != exitNotReady {
		return nil, engineFailure("check", code, stderr)
	}
	var doc struct {
		Format   string                    `json:"format"`
		Tool     workloadrelocation.Engine `json:"tool"`
		Ready    bool                      `json:"ready"`
		Blockers []string                  `json:"blockers"`
		Warnings []string                  `json:"warnings"`
	}
	if err := json.Unmarshal(out, &doc); err != nil || doc.Format != checkFormat {
		return nil, fmt.Errorf("coolify-mirror check returned no %s document", checkFormat)
	}
	if doc.Ready != (code == 0) || doc.Ready != (len(doc.Blockers) == 0) {
		return nil, errors.New("coolify-mirror check returned an inconsistent verdict")
	}
	return &workloadrelocation.Check{Format: workloadrelocation.CheckFormat, Platform: workloadrelocation.PlatformCoolify,
		RestoreReceiptFormat: "kombify.io/coolify-relocation-restore/v1",
		RecoveryFormat:       "stackkit.relocation-recovery/v1",
		PlanSHA256:           fmt.Sprintf("sha256:%x", sha256.Sum256(plan)),
		Engine:               doc.Tool, Ready: doc.Ready, Blockers: nonNil(doc.Blockers), Warnings: nonNil(doc.Warnings), Detail: json.RawMessage(out)}, nil
}

func (r *Runner) run(ctx context.Context, stdin []byte, args ...string) (stdout []byte, stderr string, code int, err error) {
	cmd := exec.CommandContext(ctx, r.path, args...) // #nosec G204 -- pinned engine, fixed argument vocabulary
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &limitWriter{w: &out, n: maxDocument}
	cmd.Stderr = &limitWriter{w: &errBuf, n: 64 << 10}
	runErr := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		return out.Bytes(), errBuf.String(), 0, nil
	case errors.As(runErr, &exitErr):
		return out.Bytes(), errBuf.String(), exitErr.ExitCode(), nil
	default:
		return nil, "", -1, fmt.Errorf("cannot run coolify-mirror at %s: %w", r.path, runErr)
	}
}

func engineFailure(step string, code int, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if i := strings.LastIndex(msg, "ERROR:"); i >= 0 {
		msg = strings.TrimSpace(msg[i+len("ERROR:"):])
	}
	if msg == "" {
		msg = "no detail"
	}
	return fmt.Errorf("coolify-mirror %s failed (exit %d): %s", step, code, firstLine(msg))
}

type limitWriter struct {
	w io.Writer
	n int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.n {
		return 0, errors.New("coolify-mirror output exceeds its limit")
	}
	l.n -= int64(len(p))
	return l.w.Write(p)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func errOr(err error) error {
	if err != nil {
		return err
	}
	return errors.New("no build identity")
}

// stream runs a long engine step with secrets in its environment and its
// progress copied to out with every secret value replaced.
func (r *Runner) stream(ctx context.Context, secrets TransferSecrets, env []string, out io.Writer, args ...string) (int, error) {
	cmd := exec.CommandContext(ctx, r.path, args...) // #nosec G204 -- pinned engine, fixed argument vocabulary
	cmd.Env = append(os.Environ(), env...)
	w := &redactingWriter{w: out, secrets: secrets.redact()}
	cmd.Stdout, cmd.Stderr = w, w
	runErr := cmd.Run()
	w.flush()
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		return 0, nil
	case errors.As(runErr, &exitErr):
		return exitErr.ExitCode(), nil
	default:
		return -1, fmt.Errorf("cannot run coolify-mirror at %s: %w", r.path, runErr)
	}
}

// redactingWriter forwards whole lines with secret values masked, so a secret
// split across two writes is still caught.
type redactingWriter struct {
	w       io.Writer
	secrets []string
	buf     []byte
}

func (r *redactingWriter) Write(p []byte) (int, error) {
	r.buf = append(r.buf, p...)
	for {
		i := bytes.IndexByte(r.buf, '\n')
		if i < 0 {
			break
		}
		r.emit(r.buf[:i+1])
		r.buf = r.buf[i+1:]
	}
	if len(r.buf) > 64<<10 {
		r.flush()
	}
	return len(p), nil
}

func (r *redactingWriter) flush() {
	if len(r.buf) > 0 {
		r.emit(r.buf)
		r.buf = nil
	}
}

func (r *redactingWriter) emit(line []byte) {
	s := string(line)
	for _, secret := range r.secrets {
		if secret == "" {
			continue
		}
		s = strings.ReplaceAll(s, secret, "[redacted]")
		// The share code prints the secret in dash-separated groups.
		if grouped := groupSecret(secret); grouped != secret {
			s = strings.ReplaceAll(s, grouped, "[redacted]")
		}
	}
	_, _ = io.WriteString(r.w, s)
}

func groupSecret(s string) string {
	var g []string
	for len(s) > 4 {
		g, s = append(g, s[:4]), s[4:]
	}
	return strings.Join(append(g, s), "-")
}
