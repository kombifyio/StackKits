package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/logging"
	"github.com/spf13/cobra"
)

var (
	logsJSON      bool
	logsDecisions bool
	logsErrors    bool
	logsTiming    bool
)

var logsCmd = &cobra.Command{
	Use:   "logs [run-id]",
	Short: "View structured deploy logs",
	Long: `View and filter structured deploy logs from previous runs.

By default, shows the most recent log in human-readable format.
Use filters to narrow down to specific event types.

Examples:
  stackkit logs                     Show latest log (human-readable)
  stackkit logs --json              Show latest log as raw JSON-Lines
  stackkit logs --decisions         Show only decision events
  stackkit logs --errors            Show only errors and warnings
  stackkit logs --timing            Show timing summary
  stackkit logs list                List all available log files`,
	Args: cobra.MaximumNArgs(1),
	RunE: runLogs,
}

var logsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available log files",
	RunE:  runLogsList,
}

func init() {
	logsCmd.Flags().BoolVar(&logsJSON, "json", false, "Output raw JSON-Lines")
	logsCmd.Flags().BoolVar(&logsDecisions, "decisions", false, "Show only decision events")
	logsCmd.Flags().BoolVar(&logsErrors, "errors", false, "Show only errors and warnings")
	logsCmd.Flags().BoolVar(&logsTiming, "timing", false, "Show timing summary")

	logsCmd.AddCommand(logsListCmd)
	rootCmd.AddCommand(logsCmd)
}

func runLogs(cmd *cobra.Command, args []string) error {
	logDir := getLogDir()

	var logPath string
	var err error

	if len(args) > 0 {
		// Specific run ID
		runID := args[0]
		if !strings.HasSuffix(runID, ".jsonl") {
			runID += ".jsonl"
		}
		logPath = logDir + "/" + runID
		if _, statErr := os.Stat(logPath); os.IsNotExist(statErr) {
			return fmt.Errorf("log file not found: %s", logPath)
		}
	} else {
		logPath, err = logging.LatestLogFile(logDir)
		if err != nil {
			return fmt.Errorf("no logs found: %w", err)
		}
	}

	entries, err := logging.ReadLogFile(logPath)
	if err != nil {
		return fmt.Errorf("failed to read log: %w", err)
	}

	if len(entries) == 0 {
		printInfo("Log file is empty: %s", logPath)
		return nil
	}

	// Apply filters
	filtered := entries
	if logsDecisions {
		filtered = filterByPrefix(entries, "decision.", "spec.loaded", "init.choices", "init.spec_created")
	} else if logsErrors {
		filtered = filterByLevel(entries, "ERROR", "WARN")
	}

	if logsTiming {
		printTimingSummary(filtered)
		return nil
	}

	// Print header
	runID := strings.TrimSuffix(strings.TrimPrefix(logPath, logDir+"/"), ".jsonl")
	printInfo("Log: %s (%d events)", runID, len(filtered))
	fmt.Println()

	if logsJSON {
		for _, entry := range filtered {
			fmt.Println(string(entry.RawJSON))
		}
		return nil
	}

	// Human-readable output
	for _, entry := range filtered {
		logging.FormatEntryHuman(os.Stdout, entry)
	}

	return nil
}

func runLogsList(cmd *cobra.Command, args []string) error {
	logDir := getLogDir()
	files, err := logging.ListLogFiles(logDir)
	if err != nil {
		return fmt.Errorf("failed to list logs: %w", err)
	}

	if len(files) == 0 {
		printInfo("No log files found in %s", logDir)
		return nil
	}

	printInfo("Available logs (%d):", len(files))
	for _, f := range files {
		runID := strings.TrimSuffix(f, ".jsonl")
		fmt.Printf("  %s\n", runID)
	}
	return nil
}

func filterByPrefix(entries []logging.LogEntry, prefixes ...string) []logging.LogEntry {
	var result []logging.LogEntry
	for _, e := range entries {
		for _, prefix := range prefixes {
			if strings.HasPrefix(e.Msg, prefix) || e.Msg == prefix {
				result = append(result, e)
				break
			}
		}
	}
	return result
}

func filterByLevel(entries []logging.LogEntry, levels ...string) []logging.LogEntry {
	var result []logging.LogEntry
	for _, e := range entries {
		for _, level := range levels {
			if e.Level == level {
				result = append(result, e)
				break
			}
		}
	}
	return result
}

func printTimingSummary(entries []logging.LogEntry) {
	printInfo("Timing Summary:")
	fmt.Println()

	// Find events with elapsed_ms
	var lastElapsed float64
	for _, e := range entries {
		elapsed, ok := e.Fields["elapsed_ms"].(float64)
		if !ok {
			continue
		}

		// Show phase markers
		switch {
		case strings.HasSuffix(e.Msg, ".complete"),
			strings.HasSuffix(e.Msg, ".success"),
			strings.HasSuffix(e.Msg, ".failed"),
			e.Msg == "tofu.init",
			e.Msg == "tofu.apply",
			e.Msg == "apply.start",
			e.Msg == "generate.complete",
			e.Msg == "remove.complete":

			phaseDuration := elapsed - lastElapsed
			fmt.Printf("  %-30s %8.1fs  (total: %.1fs)\n", e.Msg, phaseDuration/1000, elapsed/1000)
			lastElapsed = elapsed
		}
	}
}
