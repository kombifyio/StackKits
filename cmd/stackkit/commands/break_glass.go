package commands

// stackkit break-glass: the Owner's recovery surface at the Authority
// (homelab identity target §9). The break-glass bundle is the encrypted
// recovery set of internal/identity: the Authority's identity custody and the
// Pocket ID and step-ca databases, encrypted to the Owner's recovery key.
//
//   setup        create the recovery key and code, show both once, write a set
//   write        write a new set (local copy, --target directory, --backup-target)
//   restore      restore a set onto a replacement Authority (same identity origin)
//   redeem       redeem the recovery code at the console: new Owner passkey link
//   code         replace the recovery code (after it was used)
//   cancel       cancel a pending Owner device recovery
//   status       recovery state without secrets
//   notices      recovery notices for the Owner devices
//   list         list sets in the local bundle directory
//   show-bundle  print the path of a node's set
//
// The bundle directory defaults to /var/lib/stackkit/recovery and can be
// redirected with --dir or STACKKIT_BREAK_GLASS_DIR.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// breakGlassDir is the value supplied via --dir (empty = "use default
// or the env-var override"). Resolved by breakGlassDirOrDefault.
var breakGlassDir string

const (
	// The object prefix starts with an upper-case letter, which no Kopia blob
	// ID does, so Kopia maintenance never lists or collects the set.
	recoverySetObjectPrefix = "StackKitsRecoverySet/"
	recoverySecretMaxInput  = 4 << 10
)

var breakGlassCmd = &cobra.Command{
	Use:   "break-glass",
	Short: "Owner recovery: recovery set, recovery key and recovery code",
	Long: `Owner recovery at the Authority.

The recovery set (the break-glass bundle) holds the Authority's identity
custody: Owner key, CA keys, Pocket ID keys and database, step-ca database,
device registry and device credential issuer key. It is encrypted to the
Owner's recovery key, which is shown once at setup and never stored here.
The separate recovery code registers a new Owner passkey once: at this
console at once (redeem), or from an enrolled Owner device after 24 hours
with notices to every Owner device.

Restoring the set onto a replacement Authority keeps the identity origin,
so existing passkeys keep working.`,
}

var breakGlassListCmd = &cobra.Command{
	Use:   "list",
	Short: "List break-glass bundles on this host",
	Example: `  # List the encrypted recovery sets in /var/lib/stackkit/recovery
  stackkit break-glass list

  # List sets copied to another directory
  stackkit break-glass list --dir ./recovery-copy`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := breakGlassDirOrDefault()
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				cmd.Printf("No break-glass bundles found (directory %s does not exist).\n", dir)
				cmd.Println("Run 'stackkit break-glass setup --owner-approve' on the Authority to create one.")
				return nil
			}
			return fmt.Errorf("read %s: %w", dir, err)
		}
		bundles := []string{}
		for _, e := range entries {
			if !e.IsDir() && strings.HasPrefix(e.Name(), "break-glass-") && strings.HasSuffix(e.Name(), ".age") {
				bundles = append(bundles, e.Name())
			}
		}
		if len(bundles) == 0 {
			cmd.Printf("No break-glass bundles found in %s.\n", dir)
			return nil
		}
		cmd.Printf("Break-glass bundles in %s:\n", dir)
		for _, name := range bundles {
			node := strings.TrimSuffix(strings.TrimPrefix(name, "break-glass-"), ".age")
			cmd.Printf("  %s  (node: %s)\n", name, node)
		}
		return nil
	},
}

var breakGlassShowBundleCmd = &cobra.Command{
	Use:   "show-bundle <node>",
	Short: "Print the path to a node's encrypted bundle",
	Example: `  # Print the bundle path of a node that stackkit break-glass list showed
  stackkit break-glass show-bundle <node>`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		node := args[0]
		if node == "" || strings.ContainsAny(node, "/\\") {
			return fmt.Errorf("invalid node name %q", node)
		}
		path := filepath.Join(breakGlassDirOrDefault(), identity.RecoveryBundleName(node))
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("no bundle found for node %q at %s", node, path)
			}
			return fmt.Errorf("stat %s: %w", path, err)
		}
		// Path on stdout (so the caller can pipe it into age -d / cp / etc.),
		// metadata on stderr so it does not pollute the pipe.
		cmd.Printf("%s\n", path)
		fmt.Fprintf(cmd.ErrOrStderr(), "Size: %d bytes\nModified: %s\n", info.Size(), info.ModTime().Format("2006-01-02 15:04:05"))
		return nil
	},
}

type recoverySetDestinations struct {
	target       string
	backupTarget bool
}

func (d *recoverySetDestinations) flags(command *cobra.Command) {
	command.Flags().StringVar(&d.target, "target", "", "Also copy the set into this directory of the Owner's backup target (mounted disk, NAS)")
	command.Flags().BoolVar(&d.backupTarget, "backup-target", false, "Also upload the set to the S3 backup target bound with 'stackkit backup target import'")
}

func newBreakGlassSetupCommand() *cobra.Command {
	var approved, rotate, asJSON bool
	var destinations recoverySetDestinations
	command := &cobra.Command{
		Use:   "setup",
		Short: "Create the recovery key and code, show both once and write the first recovery set",
		Long: `Create the Owner's recovery key and recovery code on the Authority, print both
exactly once, and write the first recovery set.

Store the recovery key offline and apart from the recovery set: whoever
holds both holds the Authority's identity. StackKits keeps only the public
half of the key, so it can write new sets but never read one. kombify never
receives the key. --rotate replaces an existing key and code; it asks for the
Owner's passkey step-up on an enrolled Owner device in the Hub first.`,
		Example: `  # First setup, with a copy of the set on a mounted backup disk
  stackkit break-glass setup --owner-approve --target /mnt/backup

  # Machine-readable, for the setup flow that shows key and code once
  stackkit break-glass setup --owner-approve --backup-target --json`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !approved {
				return errors.New("break-glass setup requires --owner-approve")
			}
			root := getWorkDir()
			if rotate {
				if err := requireCustodyChangeStepUp(cmd, root, "recovery-rotate", "Replace the recovery key and recovery code"); err != nil {
					return err
				}
			}
			var secrets identity.RecoverySecrets
			if err := withLifecycleMutation(root, "break-glass setup", func() error {
				var err error
				secrets, err = identity.SetupRecovery(root, rotate, time.Now())
				return err
			}); err != nil {
				return err
			}
			written, writeErr := writeRecoverySetEverywhere(cmd.Context(), root, destinations)
			if asJSON {
				data := map[string]any{"recoveryKey": secrets.RecoveryKey, "recoveryCode": secrets.RecoveryCode, "recipient": secrets.Recipient, "set": written}
				if writeErr != nil {
					data["setError"] = writeErr.Error()
				}
				if err := writeCommandResult(cmd, cmd.CommandPath(), data); err != nil {
					return err
				}
				return writeErr
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintln(out, "RECOVERY KEY (shown once; keep it offline and never with the recovery set):")
			_, _ = fmt.Fprintln(out, "  "+secrets.RecoveryKey)
			_, _ = fmt.Fprintln(out, "RECOVERY CODE (shown once; registers a new Owner passkey once):")
			_, _ = fmt.Fprintln(out, "  "+secrets.RecoveryCode)
			if writeErr != nil {
				_, _ = fmt.Fprintf(out, "The recovery set was not written: %v\nRetry with: stackkit break-glass write\n", writeErr)
				return writeErr
			}
			printRecoverySetWrite(cmd, written)
			return nil
		},
	}
	command.Flags().BoolVar(&approved, "owner-approve", false, "Authorize creating (or with --rotate replacing) the recovery key and code")
	command.Flags().BoolVar(&rotate, "rotate", false, "Replace an existing recovery key and code")
	command.Flags().BoolVar(&asJSON, "json", false, "Print a JSON result that contains the recovery key and code")
	destinations.flags(command)
	return command
}

func newBreakGlassWriteCommand() *cobra.Command {
	var asJSON bool
	var destinations recoverySetDestinations
	command := &cobra.Command{
		Use:   "write",
		Short: "Write a new recovery set of the Authority's identity custody",
		Long: `Write a new recovery set: the local copy in the bundle directory and,
when given, a copy in a backup target directory and an upload to the bound S3
backup target (object StackKitsRecoverySet/<site>/<node>.age below its
prefix). Pocket ID and step-ca are paused for the few seconds the databases
are read, so the set is crash-consistent. Write a new set after identity
changes such as new members or devices.`,
		Example: `  # Write the set and upload it to the bound S3 backup target
  stackkit break-glass write --backup-target`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			written, err := writeRecoverySetEverywhere(cmd.Context(), getWorkDir(), destinations)
			if err != nil {
				return err
			}
			if asJSON {
				return writeCommandResult(cmd, cmd.CommandPath(), written)
			}
			printRecoverySetWrite(cmd, written)
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "Print a JSON result")
	destinations.flags(command)
	return command
}

type recoverySetWrite struct {
	BundlePath         string    `json:"bundlePath"`
	TargetPath         string    `json:"targetPath,omitempty"`
	BackupTargetObject string    `json:"backupTargetObject,omitempty"`
	IdentityOrigin     string    `json:"identityOrigin"`
	Node               string    `json:"node"`
	Volumes            []string  `json:"volumes"`
	CreatedAt          time.Time `json:"createdAt"`
}

func printRecoverySetWrite(cmd *cobra.Command, written recoverySetWrite) {
	cmd.Printf("Recovery set for %s (%s) written:\n  %s\n", written.Node, written.IdentityOrigin, written.BundlePath)
	if written.TargetPath != "" {
		cmd.Printf("  %s\n", written.TargetPath)
	}
	if written.BackupTargetObject != "" {
		cmd.Printf("  %s\n", written.BackupTargetObject)
	}
}

func writeRecoverySetEverywhere(ctx context.Context, root string, destinations recoverySetDestinations) (recoverySetWrite, error) {
	var written recoverySetWrite
	err := withLifecycleMutation(root, "break-glass write", func() error {
		volumes, err := recoveryRuntimeVolumes(ctx, root)
		if err != nil {
			return err
		}
		dir := breakGlassDirOrDefault()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		temporary, err := os.CreateTemp(dir, ".break-glass-*.age")
		if err != nil {
			return err
		}
		defer func() { _ = os.Remove(temporary.Name()) }()
		var manifest identity.RecoverySetManifest
		pauseErr := withRecoveryRuntimePaused(ctx, volumes, func() error {
			manifest, err = identity.WriteRecoverySet(root, volumes, temporary, time.Now())
			return err
		})
		if closeErr := temporary.Close(); pauseErr == nil {
			pauseErr = closeErr
		}
		if pauseErr != nil {
			return pauseErr
		}
		name := identity.RecoveryBundleName(manifest.NodeRef)
		written = recoverySetWrite{BundlePath: filepath.Join(dir, name), IdentityOrigin: manifest.IdentityOrigin, Node: manifest.NodeRef, CreatedAt: manifest.CreatedAt, Volumes: []string{}}
		for _, volume := range manifest.Volumes {
			written.Volumes = append(written.Volumes, volume.Name)
		}
		if err := os.Chmod(temporary.Name(), 0o600); err != nil {
			return err
		}
		if err := os.Rename(temporary.Name(), written.BundlePath); err != nil {
			return err
		}
		set, err := os.ReadFile(written.BundlePath) //nolint:gosec // the set this command just wrote
		if err != nil {
			return err
		}
		if destinations.target != "" {
			written.TargetPath = filepath.Join(destinations.target, name)
			if err := writeRecoverySetCopy(written.TargetPath, set); err != nil {
				return fmt.Errorf("copy the recovery set to %s: %w", destinations.target, err)
			}
		}
		if destinations.backupTarget {
			authority, workspace, _, err := inspectBackupTargetAuthority(ctx)
			if err != nil {
				return err
			}
			object, err := backupcustody.PutS3TargetObject(ctx, workspace, authority, recoverySetObjectPrefix+manifest.SiteRef+"/"+manifest.NodeRef+".age", set)
			if err != nil {
				return err
			}
			written.BackupTargetObject = object
		}
		return nil
	})
	return written, err
}

func writeRecoverySetCopy(path string, set []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".break-glass-*.age")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if _, err := temporary.Write(set); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temporary.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

func newBreakGlassRestoreCommand() *cobra.Command {
	var approved, asJSON bool
	var from, keyFile string
	command := &cobra.Command{
		Use:   "restore",
		Short: "Restore a recovery set onto a replacement Authority",
		Long: `Restore a recovery set onto a replacement Authority. Run it in the
workspace directory of the new host before generate and apply: it verifies
the whole set with the recovery key, fills fresh Docker volumes for the
Pocket ID and step-ca databases, and installs the identity custody. The
identity origin stays the same (the Pocket ID host is the passkey relying
party), so existing passkeys keep working. The key is read from --key-file
or standard input, never from an argument.`,
		Example: `  # Restore, then roll the Authority out again from the same StackSpec
  stackkit break-glass restore --from break-glass-main.age --owner-approve
  stackkit generate && stackkit apply`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !approved {
				return errors.New("break-glass restore requires --owner-approve")
			}
			if from == "" {
				return errors.New("break-glass restore requires --from <recovery set>")
			}
			key, err := readRecoverySecret(cmd, keyFile, "Recovery key: ")
			if err != nil {
				return err
			}
			root := getWorkDir()
			var manifest identity.RecoverySetManifest
			err = withLifecycleMutation(root, "break-glass restore", func() error {
				file, err := os.Open(from) //nolint:gosec // the Owner names the set to restore
				if err != nil {
					return err
				}
				defer func() { _ = file.Close() }()
				restore, err := identity.OpenRecoverySet(root, file, key)
				if err != nil {
					return err
				}
				defer func() { _ = restore.Close() }()
				// Every volume must be placeable before custody changes; custody
				// install is repeatable for the same Owner, so a failed volume
				// placement is retried after removing that volume.
				for _, volume := range restore.Manifest.Volumes {
					if err := requireFreshRecoveryVolume(cmd.Context(), volume); err != nil {
						return err
					}
				}
				if err := restore.InstallCustody(); err != nil {
					return err
				}
				for _, volume := range restore.Manifest.Volumes {
					mountpoint, err := freshRecoveryVolume(cmd.Context(), volume)
					if err == nil {
						err = restore.PlaceVolume(volume.Name, mountpoint)
					}
					if err != nil {
						return fmt.Errorf("restore volume %s: %w; remove it (docker volume rm %s) and run the restore again", volume.DockerVolume, err, volume.DockerVolume)
					}
				}
				manifest = restore.Manifest
				return nil
			})
			if err != nil {
				return err
			}
			result := map[string]any{"ownerRef": manifest.OwnerRef, "identityOrigin": manifest.IdentityOrigin, "site": manifest.SiteRef, "node": manifest.NodeRef, "createdAt": manifest.CreatedAt,
				"nextSteps": []string{"stackkit generate", "stackkit apply", "stackkit break-glass code --owner-approve", "stackkit device revoke <device> for devices revoked after the set was written", "stackkit break-glass write"}}
			if asJSON {
				return writeCommandResult(cmd, cmd.CommandPath(), result)
			}
			cmd.Printf("Restored the identity custody of %s from the set of %s.\n", manifest.IdentityOrigin, manifest.CreatedAt.Format(time.RFC3339))
			cmd.Println("Next: stackkit generate && stackkit apply (with the same StackSpec and domain).")
			cmd.Println("Then issue a new recovery code (stackkit break-glass code --owner-approve; the restored one counts as used),")
			cmd.Println("revoke again any device revoked after this set was written, and write a new set (stackkit break-glass write).")
			return nil
		},
	}
	command.Flags().BoolVar(&approved, "owner-approve", false, "Authorize installing the restored identity custody")
	command.Flags().StringVar(&from, "from", "", "Path of the recovery set (break-glass-<node>.age)")
	command.Flags().StringVar(&keyFile, "key-file", "", "Read the recovery key from this file instead of standard input")
	command.Flags().BoolVar(&asJSON, "json", false, "Print a JSON result")
	return command
}

func newBreakGlassRedeemCommand() *cobra.Command {
	var approved, asJSON bool
	var codeFile string
	command := &cobra.Command{
		Use:   "redeem",
		Short: "Redeem the recovery code at this console for a new Owner passkey",
		Long: `Redeem the recovery code at the Authority's console. It yields a Pocket ID
link, valid for 20 minutes, that registers a new passkey for the Owner
account; the code is then used up (replace it with 'stackkit break-glass
code'). A pending Owner device recovery ends. The code is read from
--code-file or standard input, never from an argument.`,
		Example:     `  stackkit break-glass redeem --owner-approve`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !approved {
				return errors.New("break-glass redeem requires --owner-approve")
			}
			code, err := readRecoverySecret(cmd, codeFile, "Recovery code: ")
			if err != nil {
				return err
			}
			root := getWorkDir()
			link, err := identity.RedeemRecoveryCodeAtConsole(cmd.Context(), root, code, identity.LocalRecoveryPasskeyIssuer(root), time.Now())
			if err != nil {
				return err
			}
			if asJSON {
				return writeCommandResult(cmd, cmd.CommandPath(), link)
			}
			cmd.Printf("Open this link within 20 minutes and register a new passkey:\n%s\n", link.URL)
			cmd.Println("Then replace the used recovery code: stackkit break-glass code --owner-approve")
			return nil
		},
	}
	command.Flags().BoolVar(&approved, "owner-approve", false, "Authorize redeeming the recovery code")
	command.Flags().StringVar(&codeFile, "code-file", "", "Read the recovery code from this file instead of standard input")
	command.Flags().BoolVar(&asJSON, "json", false, "Print a JSON result")
	return command
}

func newBreakGlassCodeCommand() *cobra.Command {
	var approved, asJSON bool
	command := &cobra.Command{
		Use:   "code",
		Short: "Replace the recovery code and show the new one once",
		Long: `Replace the recovery code and show the new one once. A custody change: the
Owner approves it with a passkey step-up on an enrolled Owner device in the
Hub before the code changes.`,
		Example:     `  stackkit break-glass code --owner-approve`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !approved {
				return errors.New("break-glass code requires --owner-approve")
			}
			root := getWorkDir()
			if err := requireCustodyChangeStepUp(cmd, root, "recovery-code", "Replace the recovery code"); err != nil {
				return err
			}
			code, err := identity.RotateRecoveryCode(root, time.Now())
			if err != nil {
				return err
			}
			if asJSON {
				return writeCommandResult(cmd, cmd.CommandPath(), map[string]string{"recoveryCode": code})
			}
			cmd.Println("RECOVERY CODE (shown once; registers a new Owner passkey once):")
			cmd.Println("  " + code)
			cmd.Println("Write a new recovery set so it carries this code: stackkit break-glass write")
			return nil
		},
	}
	command.Flags().BoolVar(&approved, "owner-approve", false, "Authorize replacing the recovery code")
	command.Flags().BoolVar(&asJSON, "json", false, "Print a JSON result that contains the new code")
	return command
}

func newBreakGlassCancelCommand() *cobra.Command {
	var approved bool
	command := &cobra.Command{
		Use:         "cancel",
		Short:       "Cancel a pending Owner device recovery",
		Example:     `  stackkit break-glass cancel --owner-approve`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !approved {
				return errors.New("break-glass cancel requires --owner-approve")
			}
			cancelled, err := identity.CancelRecoveryAtConsole(getWorkDir(), time.Now())
			if err != nil {
				return err
			}
			if cancelled {
				cmd.Println("The pending Owner device recovery was cancelled.")
			} else {
				cmd.Println("No Owner device recovery was pending.")
			}
			return nil
		},
	}
	command.Flags().BoolVar(&approved, "owner-approve", false, "Authorize cancelling the pending recovery")
	return command
}

func newBreakGlassStatusCommand() *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:     "status",
		Short:   "Show the recovery state without secrets",
		Example: `  stackkit break-glass status --json`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, err := identity.LoadRecoveryStatus(getWorkDir())
			if err != nil {
				return err
			}
			if asJSON {
				return writeCommandResult(cmd, cmd.CommandPath(), status)
			}
			if !status.Configured {
				cmd.Println("Recovery is not set up. Run: stackkit break-glass setup --owner-approve")
				return nil
			}
			cmd.Printf("Recovery set up %s; recovery code issued %s", status.ConfiguredAt.Format(time.RFC3339), status.CodeIssuedAt.Format(time.RFC3339))
			if status.CodeConsumed {
				cmd.Print(" and used (replace it: stackkit break-glass code --owner-approve)")
			}
			cmd.Println(".")
			if status.Pending != nil {
				cmd.Printf("Owner device recovery pending from device %s, eligible %s.\n", status.Pending.Device, status.Pending.EligibleAt.Format(time.RFC3339))
			}
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "Print a JSON result")
	return command
}

func newBreakGlassNoticesCommand() *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:     "notices",
		Short:   "List the recovery notices addressed to the Owner devices",
		Example: `  stackkit break-glass notices --json`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			notices, err := identity.ListRecoveryNotices(getWorkDir())
			if err != nil {
				return err
			}
			if asJSON {
				return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"notices": notices})
			}
			if len(notices) == 0 {
				cmd.Println("No recovery notices.")
			}
			for _, notice := range notices {
				cmd.Printf("%s  %s  from %s  to %d Owner device(s)\n", notice.CreatedAt.Format(time.RFC3339), notice.Kind, notice.Origin, len(notice.Devices))
			}
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "Print a JSON result")
	return command
}

func init() {
	breakGlassCmd.PersistentFlags().StringVar(&breakGlassDir, "dir", "",
		"Override the bundle directory (default: /var/lib/stackkit/recovery)")
	breakGlassCmd.AddCommand(breakGlassListCmd, breakGlassShowBundleCmd,
		newBreakGlassSetupCommand(), newBreakGlassWriteCommand(), newBreakGlassRestoreCommand(),
		newBreakGlassRedeemCommand(), newBreakGlassCodeCommand(), newBreakGlassCancelCommand(),
		newBreakGlassStatusCommand(), newBreakGlassNoticesCommand())
	rootCmd.AddCommand(breakGlassCmd)
}

// breakGlassDirOrDefault resolves the bundle directory in priority order:
// explicit --dir flag, then STACKKIT_BREAK_GLASS_DIR env var (used by tests
// to redirect into a temp dir), then the system default.
func breakGlassDirOrDefault() string {
	if breakGlassDir != "" {
		return breakGlassDir
	}
	if envDir := os.Getenv("STACKKIT_BREAK_GLASS_DIR"); envDir != "" {
		return envDir
	}
	return identity.DefaultRecoveryBundleDir
}

// readRecoverySecret reads a recovery key or code from a file or standard
// input, hidden when standard input is a terminal, so it never passes
// through an argument, shell history or a process list.
func readRecoverySecret(cmd *cobra.Command, path, prompt string) (string, error) {
	var raw []byte
	var err error
	switch {
	case path != "" && path != "-":
		var file *os.File
		file, err = os.Open(path) //nolint:gosec // the Owner names the private file
		if err != nil {
			return "", err
		}
		defer func() { _ = file.Close() }()
		raw, err = io.ReadAll(io.LimitReader(file, recoverySecretMaxInput+1))
	default:
		if file, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(file.Fd())) {
			_, _ = fmt.Fprint(cmd.ErrOrStderr(), prompt)
			raw, err = term.ReadPassword(int(file.Fd()))
			_, _ = fmt.Fprintln(cmd.ErrOrStderr())
		} else {
			raw, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), recoverySecretMaxInput+1))
		}
	}
	defer clear(raw)
	if err != nil {
		return "", err
	}
	if len(raw) > recoverySecretMaxInput {
		return "", errors.New("the recovery input is too long")
	}
	secret := strings.TrimSpace(string(raw))
	if secret == "" {
		return "", errors.New("no recovery input was given")
	}
	return secret, nil
}

// recoveryRuntimeVolumes finds the identity runtime's Docker volumes: the
// Pocket ID database of the one compose project that runs this workspace's
// identity runtime, and the step-ca database where the kit runs step-ca.
func recoveryRuntimeVolumes(ctx context.Context, root string) ([]identity.RecoveryVolumeSource, error) {
	// Basement runs Pocket ID and step-ca, and both databases are required;
	// the Cloud Kit runs Pocket ID only, under one of two project names.
	projects := []string{"stackkit-basement-core"}
	names := []string{identity.RecoveryVolumePocketID, identity.RecoveryVolumeStepCA}
	required := true
	if _, err := localevidence.LoadBasementRuntimeCustody(root); err != nil {
		projects, names, required = []string{"stackkit-cloud-core", "stackkit-cloud-core-standalone"}, []string{identity.RecoveryVolumePocketID}, false
	}
	client := docker.NewLocalClient()
	var volumes []identity.RecoveryVolumeSource
	for _, project := range projects {
		for _, name := range names {
			info, err := client.InspectVolume(ctx, project+"_"+name)
			if err != nil && required {
				return nil, fmt.Errorf("the identity runtime volume %s_%s is unavailable: %w", project, name, err)
			}
			if err != nil {
				continue
			}
			if info.Driver != "local" || info.Labels["com.docker.compose.project"] != project || info.Mountpoint == "" {
				return nil, fmt.Errorf("the identity runtime volume %s is not a local volume of %s", info.Name, project)
			}
			volumes = append(volumes, identity.RecoveryVolumeSource{Name: name, DockerVolume: info.Name, Dir: info.Mountpoint})
		}
	}
	found := map[string]bool{}
	for _, volume := range volumes {
		found[strings.TrimSuffix(volume.DockerVolume, "_"+volume.Name)] = true
	}
	if len(found) != 1 {
		return nil, errors.New("the Pocket ID database volume of exactly one identity runtime is required; run this on the Authority after apply")
	}
	return volumes, nil
}

// withRecoveryRuntimePaused freezes the Pocket ID and step-ca containers that
// write the given volumes while read runs, so the databases are copied in a
// crash-consistent state, and always unfreezes them afterwards.
func withRecoveryRuntimePaused(ctx context.Context, volumes []identity.RecoveryVolumeSource, read func() error) error {
	var paused []string
	for _, volume := range volumes {
		project := strings.TrimSuffix(volume.DockerVolume, "_"+volume.Name)
		service := "pocketid"
		if volume.Name == identity.RecoveryVolumeStepCA {
			service = "step-ca"
		}
		out, err := recoveryDocker(ctx, "ps", "-q", "--no-trunc", "--filter", "status=running",
			"--filter", "label=com.docker.compose.project="+project, "--filter", "label=com.docker.compose.service="+service)
		if err != nil {
			return err
		}
		paused = append(paused, strings.Fields(string(out))...)
	}
	if len(paused) > 0 {
		// Registered first: a pause that fails part-way still unfreezes the
		// containers it froze.
		defer func() {
			for _, id := range paused {
				_, _ = recoveryDocker(context.WithoutCancel(ctx), "unpause", id)
			}
		}()
		if _, err := recoveryDocker(ctx, append([]string{"pause"}, paused...)...); err != nil {
			return err
		}
	}
	return read()
}

// requireFreshRecoveryVolume refuses a restore onto a volume that already
// holds data or that a container mounts; an absent volume is fine.
func requireFreshRecoveryVolume(ctx context.Context, volume identity.RecoverySetVolume) error {
	info, err := docker.NewLocalClient().InspectVolume(ctx, volume.DockerVolume)
	if err != nil {
		return nil
	}
	if info.Driver != "local" || info.Mountpoint == "" {
		return fmt.Errorf("the volume %s is not a local Docker volume", volume.DockerVolume)
	}
	users, err := recoveryDocker(ctx, "ps", "-a", "-q", "--filter", "volume="+volume.DockerVolume)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(users))) != 0 {
		return fmt.Errorf("the volume %s is mounted by a container; restore onto a fresh Authority", volume.DockerVolume)
	}
	entries, err := os.ReadDir(info.Mountpoint)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("the volume %s already holds data; remove it (docker volume rm %s) and retry", volume.DockerVolume, volume.DockerVolume)
	}
	return nil
}

// freshRecoveryVolume returns the mountpoint of an empty Docker volume for a
// restored set volume, creating it with the compose labels the kit's project
// adopts.
func freshRecoveryVolume(ctx context.Context, volume identity.RecoverySetVolume) (string, error) {
	if err := requireFreshRecoveryVolume(ctx, volume); err != nil {
		return "", err
	}
	project := strings.TrimSuffix(volume.DockerVolume, "_"+volume.Name)
	client := docker.NewLocalClient()
	info, err := client.InspectVolume(ctx, volume.DockerVolume)
	if err != nil {
		if _, createErr := recoveryDocker(ctx, "volume", "create",
			"--label", "com.docker.compose.project="+project, "--label", "com.docker.compose.volume="+volume.Name, volume.DockerVolume); createErr != nil {
			return "", createErr
		}
		if info, err = client.InspectVolume(ctx, volume.DockerVolume); err != nil {
			return "", err
		}
	}
	return info.Mountpoint, nil
}

// recoveryDocker runs one closed docker command against the local daemon only.
func recoveryDocker(ctx context.Context, args ...string) ([]byte, error) {
	out, err := docker.NewLocalClient(docker.WithTimeout(time.Minute)).Output(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("docker %s failed", args[0])
	}
	return out, nil
}
