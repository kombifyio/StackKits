package hostpreflight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/rollout"
)

// remediationStepTimeout bounds one step. A fix that hangs is worse than one
// that fails: the operator is left with a half-changed host and no message.
const remediationStepTimeout = 90 * time.Second

// remediationDetailLimit bounds what a step may report back. Command output is
// evidence, not a log stream, and it travels into the ledger.
const remediationDetailLimit = 512

// StepRecord is what one declared change actually did.
type StepRecord struct {
	Kind   string   `json:"kind"`
	Target string   `json:"target"`
	Argv   []string `json:"argv,omitempty"`
	Status string   `json:"status"`
	Detail string   `json:"detail,omitempty"`
}

// Record is the account of one remediation: what was declared, what ran, and
// what the host looked like before and after.
//
// The before/after pair is the point. A fix that reports success while the
// condition it targeted is unchanged is the same lie as a green checkmark over
// a failed rollout, so the check is re-measured rather than assumed.
type Record struct {
	ID         string       `json:"id"`
	Title      string       `json:"title"`
	AppliesTo  string       `json:"appliesTo"`
	Status     string       `json:"status"`
	StartedAt  time.Time    `json:"startedAt"`
	FinishedAt time.Time    `json:"finishedAt"`
	Steps      []StepRecord `json:"steps,omitempty"`
	Before     *Check       `json:"before,omitempty"`
	After      *Check       `json:"after,omitempty"`
	Reason     string       `json:"reason,omitempty"`
}

// ErrResolutionNotExecutable is returned for a resolution this host or this
// process may not carry out. It is a refusal, not a failure: nothing ran.
var ErrResolutionNotExecutable = errors.New("resolution cannot be carried out here")

// Executable reports whether ApplyResolution could run this resolution now,
// and why not when it could not. It performs no probing and changes nothing,
// so a caller can list honest options before asking for consent.
func Executable(resolution Resolution) (bool, string) {
	switch {
	case resolution.Mode != ModeApply:
		return false, "this is advice: the change is outside what StackKits may do for you"
	case runtime.GOOS != "linux":
		return false, "host remediation applies to Linux hosts only"
	case resolution.RequiresRoot && os.Geteuid() != 0:
		return false, "run this again as root (sudo stackkit host remediate --apply " + resolution.ID + " --yes)"
	case !supportsDistribution(resolution, hostDistributionIDs()):
		return false, "this fix installs packages for " + strings.Join(resolution.Distributions, " and ") + " hosts only; " + strings.Join(resolution.Guidance, " ")
	}
	return true, ""
}

// ApplyResolution carries out one resolution and returns the account of it.
//
// Files are written before commands so a service restart observes the
// configuration it is being restarted for. A failed step stops the rest: the
// remaining steps assume the earlier ones took effect, and running them anyway
// is how a partially changed host becomes an unexplainable one.
func ApplyResolution(ctx context.Context, resolution Resolution) (Record, error) {
	record := Record{
		ID: resolution.ID, Title: resolution.Title, AppliesTo: resolution.AppliesTo,
		StartedAt: time.Now().UTC(),
	}
	executable, reason := Executable(resolution)
	if !executable {
		record.Status, record.Reason, record.FinishedAt = "refused", reason, time.Now().UTC()
		return record, fmt.Errorf("%w: %s", ErrResolutionNotExecutable, reason)
	}

	record.Status = "applied"
	var failure error
	for _, change := range resolution.Files {
		step, err := applyFileChange(change)
		record.Steps = append(record.Steps, step)
		if err != nil {
			failure = err
			break
		}
	}
	if failure == nil {
		steps, err := runResolutionCommands(ctx, resolution, hostCommandOutput)
		record.Steps = append(record.Steps, steps...)
		failure = err
	}
	if failure != nil {
		record.Status = "failed"
		record.Reason = boundedRemediationDetail(failure.Error())
	}
	record.FinishedAt = time.Now().UTC()
	return record, failure
}

// applyFileChange performs one declared edit, keeping a backup when asked.
func applyFileChange(change FileChange) (StepRecord, error) {
	step := StepRecord{Kind: "file", Target: change.Path, Status: "applied"}
	mode := os.FileMode(change.Mode)
	if mode == 0 {
		mode = 0o644
	}
	existing, readErr := os.ReadFile(change.Path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		step.Status, step.Detail = "failed", boundedRemediationDetail(readErr.Error())
		return step, fmt.Errorf("read %s: %w", change.Path, readErr)
	}

	next, skip, err := nextFileContent(change, existing, readErr == nil)
	if err != nil {
		step.Status, step.Detail = "failed", boundedRemediationDetail(err.Error())
		return step, err
	}
	if skip {
		step.Status, step.Detail = "skipped", "already present"
		return step, nil
	}
	if change.Backup && readErr == nil {
		backup := change.Path + ".stackkit-backup"
		if err := os.WriteFile(backup, existing, mode); err != nil {
			step.Status, step.Detail = "failed", boundedRemediationDetail(err.Error())
			return step, fmt.Errorf("back up %s: %w", change.Path, err)
		}
		step.Detail = "previous content kept at " + filepath.Base(backup)
	}
	if err := os.MkdirAll(filepath.Dir(change.Path), 0o755); err != nil {
		step.Status, step.Detail = "failed", boundedRemediationDetail(err.Error())
		return step, fmt.Errorf("create directory for %s: %w", change.Path, err)
	}
	if err := os.WriteFile(change.Path, next, mode); err != nil {
		step.Status, step.Detail = "failed", boundedRemediationDetail(err.Error())
		return step, fmt.Errorf("write %s: %w", change.Path, err)
	}
	return step, nil
}

// nextFileContent computes the file's new bytes, reporting skip when the change
// is already in place. A merge preserves every key it does not own, because the
// file belongs to the host, not to StackKits.
func nextFileContent(change FileChange, existing []byte, exists bool) ([]byte, bool, error) {
	switch {
	case change.Merge != nil:
		document := map[string]any{}
		if exists && len(strings.TrimSpace(string(existing))) > 0 {
			if err := json.Unmarshal(existing, &document); err != nil {
				return nil, false, fmt.Errorf("%s is not valid JSON; refusing to overwrite it", change.Path)
			}
		}
		mergeJSONObjects(document, change.Merge)
		encoded, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			return nil, false, err
		}
		return append(encoded, '\n'), false, nil
	case change.Append != "":
		if exists && change.AppendUnlessPresent != "" && strings.Contains(string(existing), change.AppendUnlessPresent) {
			return nil, true, nil
		}
		next := existing
		if len(next) > 0 && !strings.HasSuffix(string(next), "\n") {
			next = append(next, '\n')
		}
		return append(next, []byte(change.Append)...), false, nil
	case change.Content != "":
		if exists && string(existing) == change.Content {
			return nil, true, nil
		}
		return []byte(change.Content), false, nil
	}
	return nil, false, fmt.Errorf("file change for %s declares no content", change.Path)
}

// mergeJSONObjects folds the declared keys into the document. A nested object
// is merged key by key rather than replaced, so declaring runtimes.runsc keeps
// the host's other runtimes and declaring log-opts.max-size keeps its other
// log options; every other value takes the declared one.
func mergeJSONObjects(document, declared map[string]any) {
	for key, value := range declared {
		nested, declaredIsObject := value.(map[string]any)
		existing, existingIsObject := document[key].(map[string]any)
		if declaredIsObject && existingIsObject {
			mergeJSONObjects(existing, nested)
			continue
		}
		document[key] = value
	}
}

// commandOutput runs one argv and returns its combined output.
type commandOutput func(ctx context.Context, argv []string) ([]byte, error)

func hostCommandOutput(ctx context.Context, argv []string) ([]byte, error) {
	return exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
}

// runResolutionCommands runs the declared commands in order and stops at the
// first failure. A command naming NVIDIAServerOpenBranch runs only after the
// branch was resolved on this host, which is recorded as a step of its own.
func runResolutionCommands(ctx context.Context, resolution Resolution, run commandOutput) ([]StepRecord, error) {
	timeout := remediationStepTimeout
	if resolution.StepTimeoutSeconds > 0 {
		timeout = time.Duration(resolution.StepTimeoutSeconds) * time.Second
	}
	var steps []StepRecord
	branch := ""
	for _, argv := range resolution.Commands {
		if slices.ContainsFunc(argv, func(arg string) bool { return strings.Contains(arg, NVIDIAServerOpenBranch) }) {
			if branch == "" {
				step, resolved, err := resolveNVIDIAServerOpenBranch(ctx, run, timeout)
				steps = append(steps, step)
				if err != nil {
					return steps, err
				}
				branch = resolved
			}
			expanded := make([]string, len(argv))
			for index, arg := range argv {
				expanded[index] = strings.ReplaceAll(arg, NVIDIAServerOpenBranch, branch)
			}
			argv = expanded
		}
		step, err := runRemediationCommand(ctx, argv, run, timeout)
		steps = append(steps, step)
		if err != nil {
			return steps, err
		}
	}
	return steps, nil
}

// resolveNVIDIAServerOpenBranch asks ubuntu-drivers which GPGPU drivers the
// archive offers for this kernel and picks the newest server-open branch.
func resolveNVIDIAServerOpenBranch(ctx context.Context, run commandOutput, timeout time.Duration) (StepRecord, string, error) {
	argv := []string{"ubuntu-drivers", "list", "--gpgpu"}
	step := StepRecord{Kind: "resolve", Target: NVIDIAServerOpenBranch, Argv: argv, Status: "applied"}
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := run(bounded, argv)
	if err != nil {
		step.Status, step.Detail = "failed", boundedRemediationDetail(string(output))
		return step, "", fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	branch, err := NewestNVIDIAServerOpenBranch(output, nvidiaDriverFloor)
	if err != nil {
		step.Status, step.Detail = "failed", err.Error()
		return step, "", err
	}
	step.Detail = "nvidia:" + branch + "-server-open"
	return step, branch, nil
}

var nvidiaServerOpenPattern = regexp.MustCompile(`\bnvidia-(?:driver|headless(?:-no-dkms)?)-([0-9]+)-server-open\b`)

// NewestNVIDIAServerOpenBranch picks the newest server-open branch from
// `ubuntu-drivers list --gpgpu` output whose kernel modules come prebuilt
// ("kernel modules provided by linux-modules-nvidia-..."): a DKMS-only branch
// would compile on the host and fail under Secure Boot without an enrolled
// key. A newest branch below floor is refused rather than installed.
func NewestNVIDIAServerOpenBranch(output []byte, floor int) (string, error) {
	newest := 0
	for _, line := range strings.Split(string(output), "\n") {
		match := nvidiaServerOpenPattern.FindStringSubmatch(line)
		if match == nil || !strings.Contains(line, "linux-modules-nvidia-") {
			continue
		}
		if branch, err := strconv.Atoi(match[1]); err == nil && branch > newest {
			newest = branch
		}
	}
	switch {
	case newest == 0:
		return "", errors.New("ubuntu-drivers offers no NVIDIA server-open driver with prebuilt kernel modules for this kernel; use a supported Ubuntu kernel (generic or HWE) or install the driver by hand")
	case newest < floor:
		return "", fmt.Errorf("the newest NVIDIA server-open driver this host offers is %d, older than the required %d; update the Ubuntu release or install the driver by hand", newest, floor)
	}
	return strconv.Itoa(newest), nil
}

// runRemediationCommand runs one closed argv under a deadline.
func runRemediationCommand(ctx context.Context, argv []string, run commandOutput, timeout time.Duration) (StepRecord, error) {
	step := StepRecord{Kind: "command", Status: "applied"}
	if len(argv) == 0 {
		return step, errors.New("remediation command is empty")
	}
	step.Target, step.Argv = argv[0], append([]string(nil), argv...)
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := run(bounded, argv)
	if detail := boundedRemediationDetail(string(output)); detail != "" {
		step.Detail = detail
	}
	if err != nil {
		step.Status = "failed"
		return step, fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return step, nil
}

// boundedRemediationDetail redacts and truncates. What a host command prints is
// outside our control, so it is bounded before it is kept anywhere.
func boundedRemediationDetail(input string) string {
	trimmed := strings.TrimSpace(rollout.Redact(input))
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) <= remediationDetailLimit {
		return trimmed
	}
	return string(runes[:remediationDetailLimit]) + "..."
}

// FindCheck returns the named check from a report.
func FindCheck(report Report, id string) *Check {
	for index := range report.Checks {
		if report.Checks[index].ID == id {
			found := report.Checks[index]
			return &found
		}
	}
	return nil
}
