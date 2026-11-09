package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/hostsecurity"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/spf13/cobra"
)

// Node-to-node transport of one exported application transfer. The archive
// is age-encrypted to the receiving node's own recipient and its SHA-256 is
// bound by the source Owner's signed receipt, so the transport needs no secret
// and carries no authority: the source serves exactly that ciphertext to the
// one admitted peer address, once, and the destination keeps it only when its
// digest matches.
const (
	applicationTransferInboxDir     = ".stackkit/backups/transfer-inbox"
	applicationTransferDefaultPort  = 8124
	applicationTransferPathPrefix   = "/application-transfer/"
	applicationTransferServeAPI     = "stackkit.application-transfer-serve/v1"
	applicationTransferFetchAPI     = "stackkit.application-transfer-fetch/v1"
	applicationTransferWithdrawAPI  = "stackkit.application-transfer-withdraw/v1"
	applicationTransferFirewallNote = "stackkit-transfer-"
)

var applicationTransferSHA256Pattern = regexp.MustCompile(`^(sha256:)?[0-9a-f]{64}$`)

// The portable configuration digest is always the compiler's prefixed form;
// a bare hex value can never equal a projection digest.
var applicationTransferConfigurationDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func configureApplicationTransferTransportCommands() {
	serve := &cobra.Command{Use: "transfer-serve", Short: "Serve one exported transfer archive once to the admitted receiving node", Args: machineAwareNoArgs, RunE: runApplicationTransferServe}
	serve.Flags().String("target", "", "Export directory transfer-export wrote")
	serve.Flags().String("peer", "", "Address of the receiving node; no other client is served")
	serve.Flags().Int("port", applicationTransferDefaultPort, "TCP port of the one-shot share")
	serve.Flags().Duration("ttl", 30*time.Minute, "Stop sharing after this long without a complete download")
	serve.Flags().Bool("open-firewall", false, "Admit the peer on the port in the host firewall while sharing")
	fetch := &cobra.Command{Use: "transfer-fetch", Short: "Download one transfer archive from the source node and keep it only when its digest matches", Args: machineAwareNoArgs, RunE: runApplicationTransferFetch}
	fetch.Flags().String("from", "", "Source HOST:PORT that transfer-serve shares on")
	fetch.Flags().String("archive-sha256", "", "Exact ciphertext digest from the source's signed receipt")
	fetch.Flags().Duration("wait", 2*time.Minute, "Wait this long for the source share to come up")
	fetch.Flags().Int64("max-bytes", 512<<30, "Maximum ciphertext bytes")
	withdraw := &cobra.Command{Use: "transfer-withdraw", Short: "Withdraw an unused receiving admission and restore the intent its admit replaced", Args: machineAwareNoArgs, RunE: runApplicationTransferWithdraw}
	withdraw.Flags().String("nonce", "", "Nonce of the receiving admission")
	withdraw.Flags().Bool("owner-approve", false, "Approve withdrawing the receiving admission")
	backupCmd.AddCommand(serve, fetch, withdraw)
}

func runApplicationTransferServe(cmd *cobra.Command, _ []string) error {
	target, _ := cmd.Flags().GetString("target")
	peerText, _ := cmd.Flags().GetString("peer")
	port, _ := cmd.Flags().GetInt("port")
	ttl, _ := cmd.Flags().GetDuration("ttl")
	openFirewall, _ := cmd.Flags().GetBool("open-firewall")
	peer := net.ParseIP(strings.Trim(peerText, "[]"))
	if target == "" || peer == nil || peer.IsUnspecified() || peer.IsMulticast() || port < 1024 || port > 65535 || ttl <= 0 || ttl > 2*time.Hour {
		return errors.New("transfer-serve requires --target, one exact --peer address, a port 1024-65535 and a TTL up to 2h")
	}
	raw, err := os.ReadFile(filepath.Join(target, "application-transfer-receipt.json"))
	if err != nil {
		return err
	}
	var receipt backuplifecycle.ApplicationTransferExportReceipt
	if err = json.Unmarshal(raw, &receipt); err != nil {
		return err
	}
	if receipt.Export.Manifest.ApplicationTransfer == nil {
		return errors.New("export directory holds no application transfer")
	}
	key, err := localevidence.LoadOwnerKey(getWorkDir())
	if err != nil {
		return err
	}
	t, err := backuplifecycle.VerifyApplicationTransferExportReceipt(receipt, key.OwnerRef, localevidence.ApplicationTransferFingerprint(key.Public()), receipt.Export.Manifest.ApplicationTransfer.ID, receipt.Export.ArchiveSHA256)
	if err != nil {
		return err
	}
	digest := trimSHA256(receipt.Export.ArchiveSHA256)
	archive := filepath.Join(target, backuplifecycle.EmergencyArchive)
	size, actual, err := applicationTransferFileDigest(archive)
	if err != nil {
		return err
	}
	if actual != digest {
		return errors.New("export ciphertext differs from its signed receipt")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	if openFirewall {
		closeFirewall, firewallErr := openApplicationTransferFirewall(cmd.Context(), peer, port, digest)
		if firewallErr != nil {
			_ = listener.Close()
			return firewallErr
		}
		defer func() {
			if e := closeFirewall(); e != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "transfer-serve: firewall rule cleanup failed: %v\n", e)
			}
		}()
	}
	delivered := make(chan struct{})
	var once sync.Once
	server := &http.Server{ReadHeaderTimeout: 30 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, splitErr := net.SplitHostPort(r.RemoteAddr)
		remote := net.ParseIP(host)
		if splitErr != nil || remote == nil || !remote.Equal(peer) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != applicationTransferPathPrefix+digest {
			http.NotFound(w, r)
			return
		}
		file, openErr := os.Open(archive)
		if openErr != nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		written, copyErr := io.Copy(w, file)
		if copyErr == nil && written == size {
			once.Do(func() { close(delivered) })
		}
	})}
	go func() { _ = server.Serve(listener) }()
	ctx, cancel := context.WithTimeout(cmd.Context(), ttl)
	defer cancel()
	var serveErr error
	select {
	case <-delivered:
	case <-ctx.Done():
		serveErr = fmt.Errorf("transfer share ended after %s without a complete download", ttl)
	}
	shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	_ = server.Shutdown(shutdown)
	if serveErr != nil {
		return serveErr
	}
	return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"apiVersion": applicationTransferServeAPI, "transferId": t.ID, "archiveSha256": "sha256:" + digest, "bytes": size, "peer": peer.String(), "delivered": true})
}

func runApplicationTransferFetch(cmd *cobra.Command, _ []string) error {
	from, _ := cmd.Flags().GetString("from")
	expected, _ := cmd.Flags().GetString("archive-sha256")
	wait, _ := cmd.Flags().GetDuration("wait")
	maxBytes, _ := cmd.Flags().GetInt64("max-bytes")
	if !applicationTransferSHA256Pattern.MatchString(expected) || maxBytes <= 0 {
		return errors.New("transfer-fetch requires --archive-sha256 sha256:<64 hex> and a positive --max-bytes")
	}
	host, port, err := net.SplitHostPort(from)
	if err != nil || net.ParseIP(strings.Trim(host, "[]")) == nil {
		return errors.New("transfer-fetch requires --from <ip>:<port>")
	}
	digest := trimSHA256(expected)
	workspace := getWorkDir()
	relative := applicationTransferInboxDir + "/" + digest + ".age"
	if size, actual, e := applicationTransferFileDigest(filepath.Join(workspace, filepath.FromSlash(relative))); e == nil && actual == digest {
		return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"apiVersion": applicationTransferFetchAPI, "archive": relative, "archiveSha256": "sha256:" + digest, "bytes": size})
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), wait+backupLongOperationTimeout)
	defer cancel()
	address := net.JoinHostPort(strings.Trim(host, "[]"), port)
	deadline := time.Now().Add(wait)
	for {
		conn, dialErr := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", address)
		if dialErr == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the source share at %s did not come up within %s: %w", address, wait, dialErr)
		}
		time.Sleep(2 * time.Second)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+applicationTransferPathPrefix+digest, nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Transport: &http.Transport{Proxy: nil, DisableCompression: true}}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("source share answered HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxBytes {
		return errors.New("transfer archive exceeds --max-bytes")
	}
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
	if err = tx.MkdirAll(applicationTransferInboxDir, 0o700); err != nil {
		return err
	}
	partial, err := tx.CreatePrivateDirectoryAt(applicationTransferInboxDir, digest[:16]+"-partial-")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = tx.RemoveTree(partial)
		}
	}()
	hash := sha256.New()
	var written int64
	if err = tx.WriteFileExclusiveStream(partial+"/archive", 0o600, func(writer io.Writer) error {
		n, copyErr := io.Copy(io.MultiWriter(writer, hash), io.LimitReader(response.Body, maxBytes+1))
		written = n
		return copyErr
	}); err != nil {
		return err
	}
	if written > maxBytes {
		return errors.New("transfer archive exceeds --max-bytes")
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.New("downloaded ciphertext differs from the signed receipt digest")
	}
	if _, err = tx.Rename(partial+"/archive", relative); err != nil {
		return err
	}
	if err = tx.RemoveTree(partial); err != nil {
		return err
	}
	keep = true
	return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"apiVersion": applicationTransferFetchAPI, "archive": relative, "archiveSha256": "sha256:" + digest, "bytes": written})
}

// applicationTransferArchiveFile names the archive the operator passed or the
// one transfer-fetch kept for this exact digest.
func applicationTransferArchiveFile(workspace, explicit, digest string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if !applicationTransferSHA256Pattern.MatchString(digest) {
		return "", errors.New("--archive-sha256 must be sha256:<64 hex>")
	}
	path := filepath.Join(workspace, filepath.FromSlash(applicationTransferInboxDir), trimSHA256(digest)+".age")
	if _, err := os.Lstat(path); err != nil {
		return "", errors.New("no fetched transfer archive for this digest; run transfer-fetch or pass --archive")
	}
	return path, nil
}

func applicationTransferFileDigest(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

// openApplicationTransferFirewall admits exactly the peer on the share port in
// the StackKits Home firewall table (or ufw when that is the host policy) and
// returns the removal of that one rule.
func openApplicationTransferFirewall(ctx context.Context, peer net.IP, port int, digest string) (func() error, error) {
	comment := applicationTransferFirewallNote + digest[:12]
	family := "ip"
	if peer.To4() == nil {
		family = "ip6"
	}
	if exec.CommandContext(ctx, "nft", "list", "chain", "inet", hostsecurity.HomeFirewallTable, hostsecurity.HomeFirewallChain).Run() == nil {
		if out, err := exec.CommandContext(ctx, "nft", "insert", "rule", "inet", hostsecurity.HomeFirewallTable, hostsecurity.HomeFirewallChain,
			family, "saddr", peer.String(), "tcp", "dport", strconv.Itoa(port), "accept", "comment", `"`+comment+`"`).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("admit the receiving node in the host firewall: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return func() error {
			out, err := exec.Command("nft", "-a", "list", "chain", "inet", hostsecurity.HomeFirewallTable, hostsecurity.HomeFirewallChain).Output()
			if err != nil {
				return err
			}
			for _, line := range strings.Split(string(out), "\n") {
				if !strings.Contains(line, `comment "`+comment+`"`) {
					continue
				}
				index := strings.LastIndex(line, "# handle ")
				if index < 0 {
					continue
				}
				handle := strings.TrimSpace(line[index+len("# handle "):])
				if e := exec.Command("nft", "delete", "rule", "inet", hostsecurity.HomeFirewallTable, hostsecurity.HomeFirewallChain, "handle", handle).Run(); e != nil {
					return e
				}
			}
			return nil
		}, nil
	}
	if out, err := exec.CommandContext(ctx, "ufw", "status").Output(); err == nil && strings.Contains(strings.ToLower(string(out)), "status: active") {
		rule := []string{"allow", "from", peer.String(), "to", "any", "port", strconv.Itoa(port), "proto", "tcp", "comment", comment}
		if out, err := exec.CommandContext(ctx, "ufw", rule...).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("admit the receiving node in ufw: %v: %s", err, strings.TrimSpace(string(out)))
		}
		// Remove only the numbered rules that carry this share's comment, never
		// an identical rule the operator kept.
		return func() error {
			for {
				out, err := exec.Command("ufw", "status", "numbered").Output()
				if err != nil {
					return err
				}
				number := ""
				for _, line := range strings.Split(string(out), "\n") {
					if strings.Contains(line, "# "+comment) && strings.HasPrefix(strings.TrimSpace(line), "[") {
						number = strings.TrimSpace(strings.Trim(strings.Fields(strings.TrimSpace(line))[0], "[]"))
						if number == "" && len(strings.Fields(line)) > 1 {
							number = strings.Trim(strings.Fields(strings.TrimSpace(line))[1], "]")
						}
						break
					}
				}
				if number == "" {
					return nil
				}
				if err := exec.Command("ufw", "--force", "delete", number).Run(); err != nil {
					return err
				}
			}
		}, nil
	}
	// No StackKits table and no active ufw: the host admits the port already.
	return func() error { return nil }, nil
}

func applicationTransferAdmissionMarker(nonce, state string) (string, error) {
	if _, err := applicationTransferIdentityPath(nonce); err != nil {
		return "", err
	}
	return ".stackkit/backups/transfer-admissions/" + nonce + "-" + state + ".json", nil
}

func runApplicationTransferWithdraw(cmd *cobra.Command, _ []string) error {
	nonce, _ := cmd.Flags().GetString("nonce")
	approved, _ := cmd.Flags().GetBool("owner-approve")
	if !approved {
		return errors.New("transfer-withdraw requires --owner-approve")
	}
	consumed, err := applicationTransferAdmissionMarker(nonce, "consumed")
	if err != nil {
		return err
	}
	withdrawn, _ := applicationTransferAdmissionMarker(nonce, "withdrawn")
	workspace := getWorkDir()
	err = withLifecycleMutation(workspace, "backup transfer-withdraw", func() error {
		root, e := confinedfs.Open(workspace)
		if e != nil {
			return e
		}
		defer root.Close()
		tx, e := root.BeginTransaction()
		if e != nil {
			return e
		}
		defer tx.Close()
		if _, _, e = tx.ReadStable(".stackkit/backups/transfer-admissions/" + nonce + ".json"); e != nil {
			return errors.New("this node has not issued that receiving admission")
		}
		if _, _, e = tx.ReadStable(consumed); e == nil {
			return errors.New("the admission was consumed by a transfer preparation; use transfer-abort with the source receipt")
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if _, _, e = tx.ReadStable(withdrawn); errors.Is(e, os.ErrNotExist) {
			raw, _ := json.Marshal(map[string]string{"apiVersion": applicationTransferWithdrawAPI, "nonce": nonce, "withdrawnAt": time.Now().UTC().Format(time.RFC3339)})
			return tx.WriteFileExclusive(withdrawn, raw, 0o600)
		} else if e != nil {
			return e
		}
		return nil
	})
	if err != nil {
		return err
	}
	restored, err := restoreApplicationTransferAdmittedIntent(cmd.Context(), workspace, nonce)
	if err != nil {
		return err
	}
	cleanupApplicationTransferCustody(cmd, workspace, nonce, "")
	return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"apiVersion": applicationTransferWithdrawAPI, "nonce": nonce, "withdrawn": true, "intentRestored": restored})
}

func applicationTransferAdmissionWithdrawn(workspace, nonce string) (bool, error) {
	marker, err := applicationTransferAdmissionMarker(nonce, "withdrawn")
	if err != nil {
		return false, err
	}
	_, err = os.Lstat(filepath.Join(workspace, filepath.FromSlash(marker)))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
