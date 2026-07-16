// Package rollout records rollout manifests and functional evidence.
package rollout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Metadata struct {
	RunID              string            `json:"runId"`
	StackKit           string            `json:"stackkit,omitempty"`
	TenantDeploymentID string            `json:"tenantDeploymentId,omitempty"`
	TenantID           string            `json:"tenantId,omitempty"`
	Environment        string            `json:"environment,omitempty"`
	Provider           string            `json:"provider,omitempty"`
	Labels             map[string]string `json:"labels,omitempty"`
}

type Event struct {
	Time         time.Time         `json:"time"`
	Phase        string            `json:"phase"`
	Status       string            `json:"status"`
	Severity     string            `json:"severity,omitempty"`
	Message      string            `json:"message,omitempty"`
	FailureClass string            `json:"failureClass,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
}

type Summary struct {
	Status       string   `json:"status"`
	FailureClass string   `json:"failureClass,omitempty"`
	Message      string   `json:"message,omitempty"`
	Artifacts    []string `json:"artifacts,omitempty"`
}

type Recorder struct {
	root   string
	runID  string
	events *os.File
}

func NewRecorder(stackkitDir string, meta Metadata) (*Recorder, error) {
	runID := strings.TrimSpace(meta.RunID)
	if runID == "" {
		runID = time.Now().UTC().Format("20060102-150405")
	}
	runDir := filepath.Join(stackkitDir, "runs", runID)
	if err := os.MkdirAll(runDir, 0750); err != nil {
		return nil, err
	}
	meta.RunID = runID
	if err := writeJSON(filepath.Join(runDir, "metadata.json"), meta); err != nil {
		return nil, err
	}
	events, err := os.OpenFile(filepath.Join(runDir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	return &Recorder{root: runDir, runID: runID, events: events}, nil
}

func (r *Recorder) RunID() string {
	if r == nil {
		return ""
	}
	return r.runID
}

func (r *Recorder) Root() string {
	if r == nil {
		return ""
	}
	return r.root
}

func (r *Recorder) Event(event Event) {
	if r == nil || r.events == nil {
		return
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	if event.Attributes != nil {
		for k, v := range event.Attributes {
			event.Attributes[k] = Redact(v)
		}
	}
	event.Message = Redact(event.Message)
	data, err := json.Marshal(event)
	if err == nil {
		_, _ = r.events.Write(append(data, '\n'))
	}
}

func (r *Recorder) Close(summary Summary) error {
	if r == nil {
		return nil
	}
	if r.events != nil {
		_ = r.events.Close()
		r.events = nil
	}
	summary.Message = Redact(summary.Message)
	return writeJSON(filepath.Join(r.root, "summary.json"), summary)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

// secretPair matches a secret-bearing field in both shell/query shape
// (key=value) and JSON shape ("key": "value"): the separator (= or :) and any
// surrounding quotes are captured in group 1 so the key and shape survive while
// the value is redacted. Field names cover the tokens, platform credentials,
// and bootstrap material that flow through rollout events.
var secretPair = regexp.MustCompile(`(?i)("?\b(?:token|password|passwd|secret|api[_-]?key|api[_-]?secret|client[_-]?secret|bootstrap[_-]?token|admin[_-]?token|access[_-]?token|refresh[_-]?token|encryption[_-]?key|jwt[_-]?secret|private[_-]?key|signing[_-]?secret)"?\s*[:=]\s*"?)([^"\s,}]+)`)

// bearerToken redacts `Authorization: Bearer <token>` headers, which carry a
// secret with no key= or "key": shape.
var bearerToken = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/-]+=*`)

func Redact(input string) string {
	out := secretPair.ReplaceAllString(input, `${1}<redacted>`)
	out = bearerToken.ReplaceAllString(out, `${1}<redacted>`)
	return out
}

func ClassifyFailure(input string) string {
	s := strings.ToLower(input)
	switch {
	case strings.Contains(s, "tenant-deployment spec fetch"), strings.Contains(s, "bootstrap token"), strings.Contains(s, "admin returned 401"):
		return "spec_fetch_failed"
	case strings.Contains(s, "cloud_init_timeout"):
		return "cloud_init_timeout"
	case strings.Contains(s, "apt_lock_timeout"):
		return "apt_lock_timeout"
	case strings.Contains(s, "apt_process_timeout"):
		return "apt_process_timeout"
	case strings.Contains(s, "unattended_upgrade_timeout"):
		return "unattended_upgrade_timeout"
	case strings.Contains(s, "apt_wait"), strings.Contains(s, "apt/dpkg lock"), strings.Contains(s, "timed out waiting for apt"), strings.Contains(s, "unattended-upgr"):
		return "apt_wait_timeout"
	case strings.Contains(s, "target.inspect_failed"):
		return "target_inspect_failed"
	case strings.Contains(s, "docker daemon failed"), strings.Contains(s, "failed to start docker"):
		return "docker_daemon_failed"
	case strings.Contains(s, "failed to install docker"), strings.Contains(s, "docker install"):
		return "docker_install_failed"
	case strings.Contains(s, "terramate"):
		return "terramate_prepare_failed"
	case strings.Contains(s, "init failed"), strings.Contains(s, "provider registry"):
		return "tofu_init_failed"
	case strings.Contains(s, "opentofu binary"), strings.Contains(s, "packaged opentofu"):
		return "opentofu_prepare_failed"
	case strings.Contains(s, "apply failed"), strings.Contains(s, "opentofu apply"), strings.Contains(s, "deployment failed"):
		return "tofu_apply_failed"
	case strings.Contains(s, "verify failed"):
		return "verify_failed"
	case strings.Contains(s, "platform app"):
		return "platform_app_failed"
	default:
		return "unknown_failure"
	}
}
