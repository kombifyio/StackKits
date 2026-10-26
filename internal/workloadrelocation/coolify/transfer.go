package coolify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/workloadrelocation"
)

// Engine environment for caller-held secrets (never argv, never printed).
const (
	envArchiveKey  = "COOLIFY_MIRROR_KEY"
	envShareSecret = "COOLIFY_MIRROR_SECRET"
	envSource      = "COOLIFY_MIRROR_SOURCE"
)

// DefaultBackupDir is where send writes its backups (the engine's own folder).
const DefaultBackupDir = "/data/coolify-mirror/backups/kombify-relocate"

// DefaultSharePort is the engine's direct HTTPS share port.
const DefaultSharePort = 8123

// minTransferSecret is the least entropy accepted from the Wallet item.
const minTransferSecret = 16

// TransferSecrets are the per-move secrets both servers derive from the same
// Wallet item: the archive key encrypts the backup file, the share secret
// authenticates the download and pins its TLS certificate.
type TransferSecrets struct {
	archiveKey  string
	shareSecret string
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// DeriveTransferSecrets derives both secrets from a Wallet item value.
func DeriveTransferSecrets(value []byte) (TransferSecrets, error) {
	return DeriveTransferSecretsForOperation(value, "")
}

func DeriveTransferSecretsForOperation(value []byte, operationID string) (TransferSecrets, error) {
	if operationID != "" && !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`).MatchString(operationID) {
		return TransferSecrets{}, errors.New("invalid relocation transfer identity")
	}
	if len(bytes.TrimSpace(value)) < minTransferSecret {
		return TransferSecrets{}, fmt.Errorf("the relocation Wallet item holds less than %d bytes; Techstack must deliver a random transfer secret", minTransferSecret)
	}
	derive := func(label string) string {
		m := hmac.New(sha256.New, bytes.TrimSpace(value))
		m.Write([]byte("kombify.io/stackkit-relocation/" + label))
		if operationID != "" {
			m.Write([]byte("\x00" + operationID))
		}
		return strings.ToLower(b32.EncodeToString(m.Sum(nil)[:16]))
	}
	return TransferSecrets{archiveKey: derive("archive-key"), shareSecret: derive("share-secret")}, nil
}

func (s TransferSecrets) redact() []string { return []string{s.archiveKey, s.shareSecret} }

// ShareCode is the engine's share code for a source address; it carries the
// share secret and only ever travels in the engine's environment.
func (s TransferSecrets) shareCode(hostPort string) string {
	return hostPort + "/" + groupSecret(s.shareSecret)
}

// SendOptions control the source side of a move.
type SendOptions struct {
	ExpectedPlan []byte
	TransferID   string
	Selection    Selection
	File         string // reuse an existing backup made with the same secrets
	OutputDir    string // where a new backup is written
	Host         string // address the destination reaches this server on
	Port         int
	OpenFirewall bool
	TTL          string
	// Drained is the active drain record, if any: drained resources restore
	// running on the destination although they are stopped here.
	Prepared func(json.RawMessage) error
	Drained  string
}

// Send backs up the selection with the derived archive key and shares the
// file until the destination has downloaded it once.
func (r *Runner) Send(ctx context.Context, secrets TransferSecrets, opt SendOptions, out io.Writer) (json.RawMessage, error) {
	var planPath string
	if len(opt.ExpectedPlan) > 0 {
		f, err := os.CreateTemp("", "stackkit-relocation-plan-*.json")
		if err != nil {
			return nil, err
		}
		planPath = f.Name()
		defer os.Remove(planPath)
		if _, err = f.Write(opt.ExpectedPlan); err != nil {
			f.Close()
			return nil, err
		}
		if err = f.Close(); err != nil {
			return nil, err
		}
	}
	file := opt.File
	if file == "" {
		args, err := opt.Selection.args()
		if err != nil {
			return nil, err
		}
		if opt.OutputDir == "" {
			return nil, errors.New("a backup needs an output directory")
		}
		if err := os.MkdirAll(opt.OutputDir, 0o700); err != nil {
			return nil, err
		}
		before, _ := filepath.Glob(filepath.Join(opt.OutputDir, "*.cmb"))
		backup := []string{"backup", "--output", opt.OutputDir}
		if planPath != "" {
			backup = append(backup, "--expected-plan", planPath, "--transfer-id", opt.TransferID)
		}
		if opt.Drained != "" {
			backup = append(backup, "--drained", opt.Drained)
		}
		code, err := r.stream(ctx, secrets, []string{envArchiveKey + "=" + secrets.archiveKey}, out,
			append(backup, args...)...)
		if err != nil {
			return nil, err
		}
		if code != 0 {
			return nil, fmt.Errorf("coolify-mirror backup failed (exit %d)", code)
		}
		if file, err = newFile(before, opt.OutputDir); err != nil {
			return nil, err
		}
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	_, hashErr := io.Copy(h, f)
	closeErr := f.Close()
	if hashErr != nil {
		return nil, hashErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	data := map[string]any{"delivered": false, "archive_name": filepath.Base(file), "transfer_id": opt.TransferID, "plan_sha256": fmt.Sprintf("sha256:%x", sha256.Sum256(opt.ExpectedPlan)), "archive_sha256": fmt.Sprintf("sha256:%x", h.Sum(nil))}
	prepared, _ := json.Marshal(data)
	if opt.Prepared != nil {
		if err := opt.Prepared(prepared); err != nil {
			return nil, err
		}
	}
	port := opt.Port
	if port == 0 {
		port = DefaultSharePort
	}
	args := []string{"serve", file, "--once", "--no-code", "--port", strconv.Itoa(port)}
	if opt.Host != "" {
		args = append(args, "--host", opt.Host)
	}
	if opt.OpenFirewall {
		args = append(args, "--open-firewall")
	}
	if opt.TTL != "" {
		args = append(args, "--ttl", opt.TTL)
	}
	code, err := r.stream(ctx, secrets, []string{envArchiveKey + "=" + secrets.archiveKey, envShareSecret + "=" + secrets.shareSecret}, out, args...)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("coolify-mirror serve ended without a complete download (exit %d)", code)
	}
	data["delivered"] = true
	raw, err := json.Marshal(data)
	return raw, err
}

// ReceiveOptions control the destination side of a move.
type ReceiveOptions struct {
	ReceiptPath  string // node-owned durable engine result, never caller supplied
	TransferID   string
	ExpectedPlan []byte // checked secret-free source closure, never credentials
	From         string // source HOST[:PORT]
	OnConflict   string // copy or skip
	NoStart      bool
	SetDomains   []string
	// Wait is how long to wait for the source's share to accept connections,
	// so both sides of a move can start together; 0 tries once.
	Wait time.Duration
}

// Receive downloads the shared backup from the source and restores it here.
func (r *Runner) Receive(ctx context.Context, secrets TransferSecrets, opt ReceiveOptions, out io.Writer) (json.RawMessage, error) {
	hostPort, err := sourceAddress(opt.From)
	if err != nil {
		return nil, err
	}
	// Flag values are joined with "=": the engine takes the first bare
	// argument as the restore source, which here comes from the environment.
	args := []string{"restore", "--yes"}
	if opt.OnConflict != "" {
		if opt.OnConflict != "copy" && opt.OnConflict != "skip" {
			return nil, errors.New("--on-conflict is copy or skip")
		}
		args = append(args, "--on-conflict="+opt.OnConflict)
	}
	if opt.NoStart {
		args = append(args, "--no-start")
	}
	for _, d := range opt.SetDomains {
		args = append(args, "--set-domain="+d)
	}
	var receiptPath string
	if len(opt.ExpectedPlan) != 0 {
		if opt.NoStart {
			return nil, errors.New("a checked relocation requires verified start")
		}
		dir, err := os.MkdirTemp("", "stackkit-relocation-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		planPath := filepath.Join(dir, "expected-plan.json")
		receiptPath = opt.ReceiptPath
		if receiptPath == "" {
			receiptPath = filepath.Join(dir, "restore-receipt.json")
		} else if err := os.MkdirAll(filepath.Dir(receiptPath), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(planPath, opt.ExpectedPlan, 0o600); err != nil {
			return nil, err
		}
		args = append(args, "--expected-plan="+planPath, "--receipt-file="+receiptPath)
		if opt.TransferID == "" {
			return nil, errors.New("a checked relocation requires the semantic transfer identity")
		}
		args = append(args, "--transfer-id="+opt.TransferID)
	}
	if err := waitForShare(ctx, hostPort, opt.Wait); err != nil {
		return nil, err
	}
	env := []string{envArchiveKey + "=" + secrets.archiveKey, envSource + "=" + secrets.shareCode(hostPort)}
	code, err := r.stream(ctx, secrets, env, out, args...)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("coolify-mirror restore failed (exit %d); inspect retained destination state before a retry", code)
	}
	if receiptPath == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(receiptPath)
	if err != nil {
		return nil, fmt.Errorf("verified restore receipt unavailable: %w", err)
	}
	var receipt struct {
		Format    string                    `json:"format"`
		Tool      workloadrelocation.Engine `json:"tool"`
		Resources []json.RawMessage         `json:"resources"`
	}
	if len(raw) > 1<<20 || json.Unmarshal(raw, &receipt) != nil || receipt.Format != "kombify.io/coolify-relocation-restore/v1" || receipt.Tool != Pinned || len(receipt.Resources) == 0 {
		return nil, errors.New("verified restore receipt differs from pinned relocation engine")
	}
	return json.RawMessage(raw), nil
}

// waitForShare waits until the source's share accepts TCP connections: the
// source backs up first and only then shares.
func waitForShare(ctx context.Context, hostPort string, wait time.Duration) error {
	if wait <= 0 {
		return nil
	}
	deadline := time.Now().Add(wait)
	dialer := net.Dialer{Timeout: 5 * time.Second}
	for {
		conn, err := dialer.DialContext(ctx, "tcp", hostPort)
		if err == nil {
			return conn.Close()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the source share at %s did not come up within %s: %w", hostPort, wait, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func sourceAddress(from string) (string, error) {
	from = strings.TrimSpace(from)
	if from == "" || strings.ContainsAny(from, "/ ") {
		return "", errors.New("--from is the source server's HOST[:PORT] as `stackkit relocate send` reports it")
	}
	if _, _, err := net.SplitHostPort(from); err != nil {
		from = net.JoinHostPort(strings.Trim(from, "[]"), strconv.Itoa(DefaultSharePort))
	}
	return from, nil
}

func newFile(before []string, dir string) (string, error) {
	seen := map[string]bool{}
	for _, f := range before {
		seen[f] = true
	}
	after, err := filepath.Glob(filepath.Join(dir, "*.cmb"))
	if err != nil {
		return "", err
	}
	var fresh []string
	for _, f := range after {
		if !seen[f] {
			fresh = append(fresh, f)
		}
	}
	if len(fresh) != 1 {
		return "", fmt.Errorf("expected one new backup file in %s, found %d", dir, len(fresh))
	}
	return fresh[0], nil
}
