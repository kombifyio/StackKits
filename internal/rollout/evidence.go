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

var secretPair = regexp.MustCompile(`(?i)(token|password|secret|api[_-]?key)=([^\s]+)`)

func Redact(input string) string {
	return secretPair.ReplaceAllString(input, "$1=<redacted>")
}

func ClassifyFailure(input string) string {
	s := strings.ToLower(input)
	switch {
	case strings.Contains(s, "tenant-deployment spec fetch"), strings.Contains(s, "bootstrap token"), strings.Contains(s, "admin returned 401"):
		return "spec_fetch_failed"
	case strings.Contains(s, "docker daemon failed"), strings.Contains(s, "failed to start docker"):
		return "docker_daemon_failed"
	case strings.Contains(s, "init failed"), strings.Contains(s, "provider registry"):
		return "tofu_init_failed"
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
