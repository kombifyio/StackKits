package commands

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/hostcustody"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const paperworkMoveSocket = "/run/homelab-guard/paperwork-move.sock"

var moveIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var moveDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type paperworkMoveNode struct {
	SpaceID       string `json:"space_id"`
	SourceTenant  string `json:"source_tenant"`
	ConnectTenant string `json:"connect_tenant"`
	ServerID      string `json:"server_id"`
	WorkerID      string `json:"worker_id"`
	KeyThumbprint string `json:"guard_key_thumbprint"`
	Generation    int64  `json:"binding_generation"`
}
type paperworkMoveJob struct {
	Binding  struct{ MoveID, SourceTenant, SpaceID, OwnerSubject, DestinationNode string } `json:"binding"`
	Node     paperworkMoveNode                                                             `json:"node"`
	Manifest struct {
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	} `json:"manifest"`
	PlanSHA256      string `json:"plan_sha256"`
	CatalogRevision string `json:"catalog_revision"`
	Grant           string `json:"grant"`
}
type paperworkMoveEvent struct {
	Kind          string `json:"kind"`
	ProofKind     string `json:"proof_kind,omitempty"`
	RequestSHA256 string `json:"request_sha256,omitempty"`
	State         string `json:"state,omitempty"`
	ReceiptSHA256 string `json:"receipt_sha256,omitempty"`
	Manifest      *struct {
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	} `json:"manifest,omitempty"`
	Tables int   `json:"tables,omitempty"`
	Rows   int64 `json:"rows,omitempty"`
	Blobs  int   `json:"blobs,omitempty"`
}
type paperworkMoveStatus struct {
	Format    string              `json:"format"`
	MoveID    string              `json:"move_id"`
	PinSHA256 string              `json:"pin_sha256"`
	ItemRef   string              `json:"wallet_item_ref"`
	TenantRef string              `json:"tenant_ref"`
	State     string              `json:"state"`
	Result    *paperworkMoveEvent `json:"result,omitempty"`
}

func decodePaperworkMove(raw []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("invalid Paperwork move document")
	}
	return nil
}

// The item can be resumed after the one-use delivery was consumed: the Wallet
// already stored it in authenticated encrypted custody. No raw grant is kept
// in the command, status record, Docker argv, process environment or Homelab.
func runCustodiedPaperworkMove(cmd *cobra.Command, workspace, tenant, item string) error {
	if runtime.GOOS != "linux" || os.Getenv("STACKKIT_PAPERWORK_MOVE_ENABLED") != "true" {
		return errors.New("Paperwork move lifecycle is disabled")
	}
	_, material, err := hostcustody.LoadWalletItem(backupcustody.HostStore(workspace), tenant, item)
	if err != nil {
		return errors.New("admitted move item unavailable")
	}
	defer backupcustody.Clear(material)
	if len(material) > 16384 {
		return errors.New("move admission exceeds budget")
	}
	var envelope struct {
		Format string           `json:"format"`
		Job    paperworkMoveJob `json:"job"`
	}
	if decodePaperworkMove(material, &envelope) != nil || envelope.Format != "kombify-paperwork-node-move/1" {
		return errors.New("move item has the wrong purpose")
	}
	job := envelope.Job
	if !moveIDPattern.MatchString(job.Binding.MoveID) || len(job.Grant) != 43 || !moveDigestPattern.MatchString(job.PlanSHA256) || !moveDigestPattern.MatchString(job.CatalogRevision) || !moveDigestPattern.MatchString(job.Manifest.SHA256) || job.Manifest.Size < 1 || job.Manifest.Size > 32<<20 ||
		job.Binding.SourceTenant != job.Binding.OwnerSubject || job.Node.SourceTenant != job.Binding.SourceTenant || job.Node.SpaceID != job.Binding.SpaceID || job.Node.ServerID != job.Binding.DestinationNode || paperworkMoveDeliveryTenant(job.Node.ConnectTenant) != tenant || job.Node.Generation < 1 {
		return errors.New("move item binding is invalid")
	}
	return executePaperworkMove(cmd, workspace, tenant, item, job)
}
func paperworkMoveDeliveryTenant(tenant string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(tenant)))
	return "tenant-" + hex.EncodeToString(sum[:16])
}

func executePaperworkMove(cmd *cobra.Command, workspace, tenant, item string, job paperworkMoveJob) error {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	local := paperworkInstallPath + "/moves/" + job.Binding.MoveID
	if err := tx.MkdirAll(local+"/cache", 0700); err != nil {
		return err
	}
	publicJob := job
	publicJob.Grant = ""
	pinBytes, _ := json.Marshal(publicJob)
	pin := sha256.Sum256(pinBytes)
	state := paperworkMoveStatus{Format: "kombify-paperwork-node-move-status/1", MoveID: job.Binding.MoveID, PinSHA256: hex.EncodeToString(pin[:]), ItemRef: item, TenantRef: tenant, State: "pending"}
	if previous, _, readErr := tx.ReadStableBounded(local+"/status.json", 32768); readErr == nil {
		var old paperworkMoveStatus
		if decodePaperworkMove(previous, &old) != nil || old.Format != state.Format || old.MoveID != state.MoveID || old.PinSHA256 != state.PinSHA256 || old.ItemRef != item || old.TenantRef != tenant {
			return errors.New("move ID already belongs to another immutable job")
		}
		if old.State == "verified" && old.Result != nil && validPaperworkMoveResult(*old.Result, job) {
			return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"paperwork_move": old})
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return errors.New("move status unavailable")
	}
	compose, _, err := tx.ReadStableBounded(paperworkInstallPath+"/compose.yaml", 2<<20)
	if err != nil {
		return errors.New("pinned Paperwork installation unavailable")
	}
	mode, _, err := tx.ReadStableBounded(paperworkInstallPath+"/auth-mode", 16)
	if err != nil || string(mode) != "linked\n" || admitPaperworkInstallCompose(compose, "linked") != nil {
		return errors.New("move requires the admitted linked Paperwork install")
	}
	var topology struct {
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal(compose, &topology) != nil || !paperworkInstallDigestRef(topology.Services["paperwork"].Image) {
		return errors.New("Paperwork runtime image is not pinned")
	}
	configPath := paperworkInstallPath + "/move-runtime/" + job.Binding.MoveID + "/config.json"
	config, info, err := tx.ReadStableBounded(configPath, 16<<20)
	defer backupcustody.Clear(config)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return errors.New("private isolated-stage configuration unavailable")
	}
	view, err := root.View(local)
	if err != nil {
		return err
	}
	save := func() error {
		raw, err := json.Marshal(state)
		if err != nil {
			return err
		}
		_, err = view.WriteAtomic0600("status.json", append(raw, '\n'))
		return err
	}
	if err := save(); err != nil {
		return err
	}
	signalCtx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	// Leave time inside Guard's 12-minute command budget to remove the
	// daemon-owned container and persist an interrupted status.
	ctx, cancel := context.WithTimeout(signalCtx, 11*time.Minute+30*time.Second)
	defer cancel()
	docker, err := exec.LookPath("/usr/bin/docker")
	if err != nil {
		return errors.New("system Docker CLI unavailable")
	}
	installDir := filepath.Join(root.Name(), filepath.FromSlash(paperworkInstallPath))
	project := sha256.Sum256([]byte(installDir))
	// Snapshot the stable read into a private directory outside the workspace.
	// Docker's daemon opens bind sources by name, so reusing the checked
	// workspace pathname would lose the confined handle's identity proof.
	configDir, err := os.MkdirTemp("/run", "stackkit-paperwork-move-")
	if err != nil {
		return errors.New("private move configuration snapshot unavailable")
	}
	defer os.RemoveAll(configDir)
	configFile := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configFile, config, 0600); err != nil {
		return errors.New("private move configuration snapshot unavailable")
	}
	containerName := "stackkit-paperwork-move-" + hex.EncodeToString(project[:8]) + "-" + state.PinSHA256[:16]
	image := topology.Services["paperwork"].Image
	if err := removeOwnedPaperworkMoveContainer(ctx, docker, containerName, state.PinSHA256, image); err != nil {
		return err
	}
	cache := filepath.Join(root.Name(), filepath.FromSlash(local+"/cache"))
	if strings.Contains(cache, ",") {
		return errors.New("Paperwork move workspace path contains a Docker mount separator")
	}
	// The process shares only the installation's private network. It mounts one
	// read-only admitted config and this job's cache, never the Guard state/key.
	process := exec.CommandContext(ctx, docker, "--host", "unix:///var/run/docker.sock", "run", "--rm", "-i", "--pull", "never",
		"--name", containerName, "--label", "io.kombify.paperwork-move.pin="+state.PinSHA256,
		"--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--user", "0:0",
		"--network", "stackkit-paperwork-"+hex.EncodeToString(project[:8])+"_default",
		"--mount", "type=bind,src="+configFile+",dst=/run/paperwork-move/config.json,readonly",
		"--mount", "type=bind,src="+cache+",dst=/run/paperwork-move/cache",
		"--env", "PAPERWORK_MOVE_RUNTIME_CONFIG=/run/paperwork-move/config.json", "--entrypoint", "/app/paperwork-move-runtime", topology.Services["paperwork"].Image, "pull-import")
	process.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	process.Stderr = io.Discard
	in, err := process.StdinPipe()
	if err != nil {
		return err
	}
	out, err := process.StdoutPipe()
	if err != nil {
		return err
	}
	if err := tx.VerifyPathIdentity(); err != nil {
		return err
	}
	if err := process.Start(); err != nil {
		return errors.New("Paperwork move process could not start")
	}
	finished := false
	defer func() {
		_ = in.Close()
		if !finished {
			_ = process.Process.Kill()
			_ = process.Wait()
		}
	}()
	interrupted := func() error {
		// Killing docker run alone leaves its daemon-owned container alive.
		// Use a fresh bounded context, including after caller cancellation.
		_ = in.Close()
		if !finished {
			_ = process.Process.Kill()
			_ = process.Wait()
			finished = true
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		cleanupErr := removeOwnedPaperworkMoveContainer(cleanupCtx, docker, containerName, state.PinSHA256, image)
		state.State = "interrupted"
		state.Result = nil
		_ = save()
		if cleanupErr != nil {
			return errors.New("Paperwork move interrupted; owned container cleanup unresolved; retry the same admitted job")
		}
		return errors.New("Paperwork move interrupted; retry the same admitted job")
	}
	if json.NewEncoder(in).Encode(job) != nil {
		return interrupted()
	}
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 4096), 16384)
	var result *paperworkMoveEvent
	for scanner.Scan() {
		var event paperworkMoveEvent
		if decodePaperworkMove(scanner.Bytes(), &event) != nil {
			return interrupted()
		}
		switch event.Kind {
		case "proof":
			proof, err := requestPaperworkMoveProof(ctx, job, event)
			if err != nil {
				return interrupted()
			}
			if json.NewEncoder(in).Encode(struct {
				Proof string `json:"proof"`
			}{proof}) != nil {
				return interrupted()
			}
		case "progress":
			if event.State != "pulling" && event.State != "importing" {
				return interrupted()
			}
			state.State = event.State
			if save() != nil {
				return interrupted()
			}
		case "result":
			if result != nil || !validPaperworkMoveResult(event, job) {
				return interrupted()
			}
			result = &event
		default:
			return interrupted()
		}
	}
	_ = in.Close()
	err = process.Wait()
	finished = true
	if scanner.Err() != nil || err != nil || result == nil {
		return interrupted()
	}
	state.State = "verified"
	state.Result = result
	if err := save(); err != nil {
		return err
	}
	return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"paperwork_move": state})
}
func validPaperworkMoveResult(event paperworkMoveEvent, job paperworkMoveJob) bool {
	return event.Kind == "result" && event.State == "verified" && event.Manifest != nil && *event.Manifest == job.Manifest &&
		moveDigestPattern.MatchString(event.ReceiptSHA256) && event.ProofKind == "" && event.RequestSHA256 == "" && event.Tables >= 0 && event.Rows >= 0 && event.Blobs >= 0
}

// Removal always addresses the inspected immutable container ID. An occupied
// name with another pin or image belongs to somebody else and is never removed.
func removeOwnedPaperworkMoveContainer(ctx context.Context, docker, name, pin, image string) error {
	run := func(args ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, docker, append([]string{"--host", "unix:///var/run/docker.sock"}, args...)...)
		command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
		command.Stderr = io.Discard
		return command.Output()
	}
	listed, err := run("container", "ls", "--all", "--no-trunc", "--filter", "name=^/"+name+"$", "--format", "{{.ID}}")
	if err != nil {
		return errors.New("move container ownership unavailable")
	}
	id := strings.TrimSpace(string(listed))
	if id == "" {
		return nil
	}
	if !moveDigestPattern.MatchString(id) {
		return errors.New("move container identity is invalid")
	}
	inspected, err := run("container", "inspect", "--format", `{{.Id}} {{.Name}} {{.Config.Image}} {{index .Config.Labels "io.kombify.paperwork-move.pin"}}`, id)
	if err != nil {
		return errors.New("move container ownership unavailable")
	}
	fields := strings.Fields(string(inspected))
	if len(fields) != 4 || fields[0] != id || fields[1] != "/"+name || fields[2] != image || fields[3] != pin {
		return errors.New("move container name is occupied by an unowned container")
	}
	if _, err := run("container", "rm", "--force", id); err != nil {
		return errors.New("owned move container cleanup failed")
	}
	return nil
}

func requestPaperworkMoveProof(ctx context.Context, job paperworkMoveJob, event paperworkMoveEvent) (string, error) {
	path := os.Getenv("PAPERWORK_MOVE_GUARD_SOCKET")
	if path == "" {
		path = paperworkMoveSocket
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", path)
	if err != nil {
		return "", errors.New("bound Guard proof socket unavailable")
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	request := struct {
		Kind   string            `json:"kind"`
		Node   paperworkMoveNode `json:"node"`
		Grant  string            `json:"grant,omitempty"`
		Digest string            `json:"request_sha256,omitempty"`
	}{Kind: event.ProofKind, Node: job.Node, Digest: event.RequestSHA256}
	if event.ProofKind == "move" {
		request.Grant = job.Grant
	} else if event.ProofKind != "pull" || !moveDigestPattern.MatchString(event.RequestSHA256) {
		return "", errors.New("invalid proof request")
	}
	if json.NewEncoder(conn).Encode(request) != nil {
		return "", errors.New("Guard proof request failed")
	}
	if closer, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
	}
	raw, err := io.ReadAll(io.LimitReader(conn, 8193))
	var answer struct {
		Proof string `json:"proof"`
	}
	if err != nil || len(raw) > 8192 || decodePaperworkMove(raw, &answer) != nil || len(answer.Proof) < 1 || len(answer.Proof) > 4096 {
		return "", fmt.Errorf("Guard refused Paperwork proof")
	}
	return answer.Proof, nil
}
