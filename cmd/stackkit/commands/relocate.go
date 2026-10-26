package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/workloadrelocation"
	relocatecoolify "github.com/kombifyio/stackkits/internal/workloadrelocation/coolify"
	"github.com/spf13/cobra"
)

type relocateOptions struct {
	platform   string
	engine     string
	full       bool
	all        bool
	projects   []string
	domains    []string
	uuids      []string
	noDeps     bool
	images     string
	outputPath string
	planPath   string
	json       bool

	tenantRef    string
	secretRef    string
	transferID   string
	ownerApprove bool
	file         string
	host         string
	port         int
	openFirewall bool
	ttl          string
	from         string
	onConflict   string
	noStart      bool
	setDomains   []string
	resumePath   string
	undo         bool
	wait         time.Duration
}

// newRelocateCommand moves PaaS workloads between two servers' platforms.
// Each subcommand acts only on the server it runs on: plan on the source,
// check on the destination.
func newRelocateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:         "relocate",
		Short:       "Move PaaS workloads to another server: plan, check, drain, send, receive",
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	}
	command.AddCommand(newRelocatePlanCommand(), newRelocateCheckCommand(), newRelocateDrainCommand(), newRelocateSendCommand(), newRelocateReceiveCommand(), newRelocateStatusCommand(), newRelocateAbortCommand())
	return command
}

func newRelocatePlanCommand() *cobra.Command {
	options := &relocateOptions{}
	command := &cobra.Command{
		Use:   "plan",
		Short: "Inventory what a move would take from this server (read-only, secret-free JSON)",
		Long: "Runs on the source server. Writes the relocation plan: resources, volumes, host paths, images,\n" +
			"database dumps and sizes, without credentials. Hand it to `stackkit relocate check` on the destination.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireCoolifyPlatform(options.platform); err != nil {
				return err
			}
			runner, err := relocatecoolify.Locate(cmd.Context(), options.engine)
			if err != nil {
				return err
			}
			plan, err := runner.Plan(cmd.Context(), options.selection())
			if err != nil {
				return err
			}
			if options.json {
				return writeCommandResult(cmd, cmd.CommandPath(), json.RawMessage(plan))
			}
			if options.outputPath == "" || options.outputPath == "-" {
				_, err = cmd.OutOrStdout().Write(append(plan, '\n'))
				return err
			}
			return os.WriteFile(options.outputPath, append(plan, '\n'), 0o600)
		},
	}
	addRelocateCommonFlags(command, options)
	addRelocateSelectionFlags(command, options)
	command.Flags().StringVarP(&options.outputPath, "output", "o", "", "Write the plan to a file instead of stdout")
	addRelocateJSONFlag(command, options)
	return command
}

func newRelocateCheckCommand() *cobra.Command {
	options := &relocateOptions{}
	command := &cobra.Command{
		Use:   "check",
		Short: "Check on this server whether a relocation plan can land here (read-only)",
		Long: "Runs on the destination server with a plan from `stackkit relocate plan`. Reports blockers\n" +
			"(Coolify version, disk space, host dependencies, a non-empty target for a full move) and warnings.\n" +
			"Exits non-zero while any blocker remains.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireCoolifyPlatform(options.platform); err != nil {
				return err
			}
			if strings.TrimSpace(options.planPath) == "" {
				return fmt.Errorf("--plan is required (a file from `stackkit relocate plan`, or - for stdin)")
			}
			plan, err := readRelocationPlan(cmd.InOrStdin(), options.planPath)
			if err != nil {
				return err
			}
			runner, err := relocatecoolify.Locate(cmd.Context(), options.engine)
			if err != nil {
				return err
			}
			result, err := runner.Check(cmd.Context(), plan)
			if err != nil {
				return err
			}
			if options.json {
				status := "success"
				if !result.Ready {
					status = "failed"
				}
				if err := writeCommandResultStatus(cmd, cmd.CommandPath(), status, result); err != nil {
					return err
				}
			} else {
				printRelocationCheck(cmd.OutOrStdout(), result)
			}
			if !result.Ready {
				return fmt.Errorf("relocation blocked on this server: %s", strings.Join(result.Blockers, "; "))
			}
			return nil
		},
	}
	addRelocateCommonFlags(command, options)
	command.Flags().StringVar(&options.planPath, "plan", "", "Relocation plan file from the source server, or - for stdin")
	addRelocateJSONFlag(command, options)
	return command
}

func addRelocateSelectionFlags(command *cobra.Command, options *relocateOptions) {
	flags := command.Flags()
	flags.BoolVar(&options.full, "full", false, "Whole Coolify (destination must be an empty Coolify)")
	flags.BoolVar(&options.all, "all", false, "Every resource on this server")
	flags.StringSliceVar(&options.projects, "project", nil, "Whole projects (name, name/environment or a domain)")
	flags.StringSliceVar(&options.domains, "domain", nil, "Resources serving these domains")
	flags.StringSliceVar(&options.uuids, "uuid", nil, "Resources by Coolify uuid")
	flags.BoolVar(&options.noDeps, "no-deps", false, "Do not add databases the selected resources depend on")
	flags.StringVar(&options.images, "images", "", "Docker images to carry: apps, all or none (engine default apps)")
}

func (o *relocateOptions) selection() relocatecoolify.Selection {
	return relocatecoolify.Selection{Full: o.full, All: o.all, Projects: o.projects, Domains: o.domains,
		UUIDs: o.uuids, NoDeps: o.noDeps, Images: o.images}
}

func addRelocateTransferFlags(command *cobra.Command, options *relocateOptions) {
	command.Flags().StringVar(&options.transferID, "transfer-id", "", "One semantic move identity; isolates transfer keys even when a Wallet item is reused")
	command.Flags().StringVar(&options.tenantRef, "tenant-ref", "", "Wallet tenant of the transfer secret")
	command.Flags().StringVar(&options.secretRef, "secret-ref", "", "Wallet item holding this move's transfer secret (delivered to both servers)")
	command.Flags().BoolVar(&options.ownerApprove, "owner-approve", false, "Confirm the owner approved this move")
}

// relocationSecrets reads the Wallet item and derives the move's secrets; the
// item value is cleared before returning.
func relocationSecrets(options *relocateOptions) (relocatecoolify.TransferSecrets, error) {
	if !options.ownerApprove {
		return relocatecoolify.TransferSecrets{}, fmt.Errorf("moving workloads changes this server; pass --owner-approve")
	}
	value, err := workloadrelocation.LoadTransferSecret(getWorkDir(), options.tenantRef, options.secretRef)
	if err != nil {
		return relocatecoolify.TransferSecrets{}, err
	}
	defer workloadrelocation.Clear(value)
	return relocatecoolify.DeriveTransferSecretsForOperation(value, options.transferID)
}

func newRelocateDrainCommand() *cobra.Command {
	options := &relocateOptions{}
	command := &cobra.Command{
		Use:   "drain",
		Short: "Stop the planned applications and services on this server before the final send",
		Long: "Runs on the source server with its plan from `stackkit relocate plan`. Stops the plan's applications\n" +
			"and services through Coolify; databases keep running, so the following `relocate send` takes a\n" +
			"consistent backup with no writer left and the destination still starts everything. Nothing is\n" +
			"deleted. `--undo` starts again exactly what the drain stopped (the rollback of a move).",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireCoolifyPlatform(options.platform); err != nil {
				return err
			}
			if !options.ownerApprove {
				return fmt.Errorf("draining stops workloads on this server; pass --owner-approve")
			}
			record := relocatecoolify.DrainRecordPath(getWorkDir(), options.transferID)
			var plan []byte
			if !options.undo {
				if strings.TrimSpace(options.planPath) == "" {
					return fmt.Errorf("--plan is required (the file `stackkit relocate plan` wrote on this server)")
				}
				var err error
				if plan, err = readRelocationPlan(cmd.InOrStdin(), options.planPath); err != nil {
					return err
				}
			}
			runner, err := relocatecoolify.Locate(cmd.Context(), options.engine)
			if err != nil {
				return err
			}
			var result json.RawMessage
			if options.undo && options.resumePath != "" {
				return runManagedRelocationResume(cmd, options, runner)
			}
			if options.undo {
				result, err = runner.Undrain(cmd.Context(), record, options.transferID)
			} else {
				result, err = runner.Drain(cmd.Context(), plan, record, options.transferID)
			}
			if err != nil {
				return err
			}
			if options.json {
				return writeCommandResult(cmd, cmd.CommandPath(), result)
			}
			_, err = cmd.OutOrStdout().Write(append(result, '\n'))
			return err
		},
	}
	addRelocateCommonFlags(command, options)
	flags := command.Flags()
	flags.StringVar(&options.planPath, "plan", "", "Relocation plan made on this server, or - for stdin")
	flags.StringVar(&options.resumePath, "managed-resume", "", "Managed current destination-abort observation envelope, or - for stdin")
	flags.BoolVar(&options.undo, "undo", false, "Start again what the active drain stopped")
	flags.BoolVar(&options.ownerApprove, "owner-approve", false, "Confirm the owner approved stopping these workloads")
	flags.StringVar(&options.transferID, "transfer-id", "", "Semantic move identity bound to the source drain")
	addRelocateJSONFlag(command, options)
	return command
}

// activeDrainRecord returns this workspace's drain record when a drain is
// active, so the backup restores drained resources as running.
func activeDrainRecord(transferID string) string {
	record := relocatecoolify.DrainRecordPath(getWorkDir(), transferID)
	if _, err := os.Stat(record); err != nil {
		return ""
	}
	return record
}

func newRelocateSendCommand() *cobra.Command {
	options := &relocateOptions{}
	command := &cobra.Command{
		Use:   "send",
		Short: "Back up the selection and share it once with the destination server",
		Long: "Runs on the source server. Backs up the selected Coolify resources, encrypted with a key derived\n" +
			"from the move's Wallet item, and shares the file over pinned HTTPS until the destination has\n" +
			"downloaded it once. The source keeps running; nothing is stopped or removed here.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireCoolifyPlatform(options.platform); err != nil {
				return err
			}
			secrets, err := relocationSecrets(options)
			if err != nil {
				return err
			}
			runner, err := relocatecoolify.Locate(cmd.Context(), options.engine)
			if err != nil {
				return err
			}
			drained := activeDrainRecord(options.transferID)
			var plan []byte
			if options.planPath != "" {
				plan, err = readRelocationPlan(cmd.InOrStdin(), options.planPath)
				if err != nil {
					return err
				}
			}
			if options.transferID != "" {
				prior, err := workloadrelocation.LoadReceipt(getWorkDir(), options.transferID, "sent", plan)
				if err == nil {
					if _, err := runner.Observe(cmd.Context(), prior.Data, "sent", getWorkDir()); err != nil {
						return err
					}
					return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"platform": options.platform, "delivered": true, "drained": true, "receipt": prior})
				}
				if !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			if options.transferID != "" {
				if prepared, err := workloadrelocation.LoadReceipt(getWorkDir(), options.transferID, "prepared", plan); err == nil {
					if _, err := runner.Observe(cmd.Context(), prepared.Data, "prepared", getWorkDir()); err != nil {
						return err
					}
					var v struct {
						ArchiveName string `json:"archive_name"`
					}
					if json.Unmarshal(prepared.Data, &v) != nil || filepath.Base(v.ArchiveName) != v.ArchiveName {
						return errors.New("invalid retained prepared archive")
					}
					options.file = filepath.Join(relocatecoolify.DefaultBackupDir, v.ArchiveName)
				} else if !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			data, err := runner.Send(cmd.Context(), secrets, relocatecoolify.SendOptions{
				ExpectedPlan: plan, TransferID: options.transferID,
				Prepared: func(raw json.RawMessage) error {
					if options.transferID == "" {
						return nil
					}
					_, err := workloadrelocation.StoreReceipt(getWorkDir(), options.transferID, "prepared", plan, raw)
					return err
				},
				Selection: options.selection(), File: options.file, OutputDir: options.outputPath,
				Host: options.host, Port: options.port, OpenFirewall: options.openFirewall, TTL: options.ttl,
				Drained: drained,
			}, options.progress(cmd))
			if err != nil {
				return err
			}
			var signed any
			if options.transferID != "" {
				receipt, err := workloadrelocation.StoreReceipt(getWorkDir(), options.transferID, "sent", plan, data)
				if err != nil {
					return err
				}
				signed = receipt
			}
			if !options.json {
				return nil
			}
			return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{
				"platform": options.platform, "delivered": true, "drained": drained != "",
				"receipt": signed,
			})
		},
	}
	addRelocateCommonFlags(command, options)
	addRelocateSelectionFlags(command, options)
	addRelocateTransferFlags(command, options)
	flags := command.Flags()
	flags.StringVar(&options.file, "file", "", "Share an existing backup made with the same Wallet item instead of a new one")
	flags.StringVar(&options.planPath, "plan", "", "Checked source plan, or - for stdin")
	flags.StringVarP(&options.outputPath, "output", "o", relocatecoolify.DefaultBackupDir, "Directory for the new backup")
	flags.StringVar(&options.host, "host", "", "Address the destination reaches this server on (default: detected)")
	flags.IntVar(&options.port, "port", relocatecoolify.DefaultSharePort, "HTTPS port of the share")
	flags.BoolVar(&options.openFirewall, "open-firewall", false, "Open the share port in ufw while sharing")
	flags.StringVar(&options.ttl, "ttl", "2h", "Stop sharing after this long without a complete download")
	addRelocateJSONFlag(command, options)
	return command
}

func newRelocateReceiveCommand() *cobra.Command {
	options := &relocateOptions{}
	command := &cobra.Command{
		Use:   "receive",
		Short: "Download the shared backup from the source server and restore it here",
		Long: "Runs on the destination server while `stackkit relocate send` shares. Downloads over pinned HTTPS,\n" +
			"verifies, and restores into this Coolify. A failure after the import commits retains an uncertain\n" +
			"destination outcome; use relocation status before choosing recovery.\n" +
			"Run `stackkit relocate check` with the plan first.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireCoolifyPlatform(options.platform); err != nil {
				return err
			}
			secrets, err := relocationSecrets(options)
			if err != nil {
				return err
			}
			runner, err := relocatecoolify.Locate(cmd.Context(), options.engine)
			if err != nil {
				return err
			}
			var plan []byte
			if options.planPath != "" {
				plan, err = readRelocationPlan(cmd.InOrStdin(), options.planPath)
				if err != nil {
					return err
				}
			}
			if options.transferID != "" {
				prior, err := workloadrelocation.LoadReceipt(getWorkDir(), options.transferID, "received", plan)
				if err == nil {
					if _, err := runner.Observe(cmd.Context(), prior.Data, "received", getWorkDir()); err != nil {
						return err
					}
					return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"platform": options.platform, "restored": true, "started": true, "verification": prior.Data, "receipt": prior})
				}
				if !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			receipt, err := runner.Receive(cmd.Context(), secrets, relocatecoolify.ReceiveOptions{
				ReceiptPath:  relocationEngineReceiptPath(options.transferID),
				ExpectedPlan: plan,
				TransferID:   options.transferID,
				From:         options.from, OnConflict: options.onConflict, NoStart: options.noStart, SetDomains: options.setDomains,
				Wait: options.wait,
			}, options.progress(cmd))
			if err != nil {
				return err
			}
			var signed any
			if options.transferID != "" {
				stored, err := workloadrelocation.StoreReceipt(getWorkDir(), options.transferID, "received", plan, receipt)
				if err != nil {
					return err
				}
				signed = stored
			}
			if !options.json {
				return nil
			}
			return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{
				"platform": options.platform, "restored": true, "started": !options.noStart,
				"onConflict": options.onConflict, "setDomains": nonNilStrings(options.setDomains),
				"verification": receipt,
				"receipt":      signed,
			})
		},
	}
	addRelocateCommonFlags(command, options)
	addRelocateTransferFlags(command, options)
	flags := command.Flags()
	flags.StringVar(&options.planPath, "plan", "", "Checked source relocation plan, or - for stdin; binds the archive and emits verified resource observations")
	flags.StringVar(&options.from, "from", "", "Source server HOST[:PORT] as `relocate send` reports it")
	flags.StringVar(&options.onConflict, "on-conflict", "copy", "A resource that already exists here: copy or skip")
	flags.BoolVar(&options.noStart, "no-start", false, "Restore without starting the resources")
	flags.StringArrayVar(&options.setDomains, "set-domain", nil, "Replace a restored domain, OLD=NEW (repeatable)")
	flags.DurationVar(&options.wait, "wait", 0, "Wait this long for the source's share to come up before downloading")
	addRelocateJSONFlag(command, options)
	return command
}

func relocationEngineReceiptPath(transferID string) string {
	if transferID == "" {
		return ""
	}
	return filepath.Join(getWorkDir(), ".stackkit", "relocation", "engine-receipts", transferID+".json")
}

func addRelocateCommonFlags(command *cobra.Command, options *relocateOptions) {
	command.PreRunE = func(_ *cobra.Command, _ []string) error {
		if options.transferID != "" && !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`).MatchString(options.transferID) {
			return errors.New("invalid relocation transfer identity")
		}
		return nil
	}
	command.Flags().StringVar(&options.platform, "platform", string(workloadrelocation.PlatformCoolify), "PaaS platform to relocate (coolify)")
	command.Flags().StringVar(&options.engine, "engine", "", "Path to the pinned relocation engine (default: "+relocatecoolify.DefaultEnginePath+", $"+relocatecoolify.EngineEnv+" or PATH)")
}

// addRelocateJSONFlag gives a relocate command the machine-readable
// stackkit.command-result/v1 output Techstack consumes.
func addRelocateJSONFlag(command *cobra.Command, options *relocateOptions) {
	command.Flags().BoolVar(&options.json, "json", false, "Emit stackkit.command-result/v1 JSON (engine progress goes to stderr)")
}

// progress is where engine progress goes: stdout for a person, stderr when
// stdout carries the JSON result.
func (o *relocateOptions) progress(cmd *cobra.Command) io.Writer {
	if o.json {
		return cmd.ErrOrStderr()
	}
	return cmd.OutOrStdout()
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func requireCoolifyPlatform(platform string) error {
	if workloadrelocation.Platform(platform) != workloadrelocation.PlatformCoolify {
		return fmt.Errorf("relocation supports --platform coolify; %q has no relocation engine yet", platform)
	}
	return nil
}

func readRelocationPlan(stdin io.Reader, path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(io.LimitReader(stdin, 64<<20))
	}
	return os.ReadFile(path) // #nosec G304 -- operator-supplied plan file
}

func printRelocationCheck(out io.Writer, result *workloadrelocation.Check) {
	verdict := "ready: the move can land on this server"
	if !result.Ready {
		verdict = "blocked: the move cannot land on this server yet"
	}
	_, _ = fmt.Fprintf(out, "%s (%s %s %s)\n", verdict, result.Engine.Name, result.Engine.Version, result.Engine.ForkRevision)
	for _, b := range result.Blockers {
		_, _ = fmt.Fprintf(out, "  blocker: %s\n", b)
	}
	for _, w := range result.Warnings {
		_, _ = fmt.Fprintf(out, "  warning: %s\n", w)
	}
}
