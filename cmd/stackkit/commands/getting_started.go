package commands

import (
	"fmt"
	"io"
	"strings"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/spf13/cobra"
)

// Group the existing commands only after every command has been registered.
func configureCommandGroups() {
	if len(rootCmd.Groups()) > 0 {
		return
	}
	rootCmd.AddGroup(
		&cobra.Group{ID: "start", Title: "Set up your services:"},
		&cobra.Group{ID: "care", Title: "Check, maintain and recover:"},
		&cobra.Group{ID: "reference", Title: "Advanced tools and reference:"},
	)
	for _, command := range rootCmd.Commands() {
		if command.GroupID != "" {
			continue
		}
		command.GroupID = "reference"
		switch command.Name() {
		case "init", "validate", "generate", "plan", "prepare", "apply", "setup":
			command.GroupID = "start"
		case "status", "verify", "logs", "backup", "upgrade", "drift", "service", "remove", "break-glass":
			command.GroupID = "care"
		}
	}
	rootCmd.SetHelpCommandGroupID("reference")
	rootCmd.SetCompletionCommandGroupID("reference")
}

// Shell examples must preserve a path as one literal argument, including spaces.
func shellArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// Render the accepted projection without deriving another readiness state.
func printApplicationGuidance(out io.Writer, app applicationlifecycle.ApplicationExperience) {
	_, _ = fmt.Fprintf(out, "\n  %s\n", app.WorkloadRef)
	for _, entry := range []struct {
		label string
		axis  applicationlifecycle.ExperienceAxis
	}{
		{"Installation", app.Installed}, {"Reachability", app.Reachable},
		{"Account setup", app.Setup}, {"Ready to use", app.Usable},
		{"Recovery", app.Recoverable},
	} {
		_, _ = fmt.Fprintf(out, "    %s: %s (%s)\n", entry.label, entry.axis.Status, entry.axis.Freshness)
		if entry.axis.Reason != "" {
			_, _ = fmt.Fprintf(out, "      %s\n", entry.axis.Reason)
		}
	}
	if app.URL != "" {
		label := "Configured address; check reachability before opening"
		if app.Reachable.Status == applicationlifecycle.AxisVerified && app.Reachable.Freshness == applicationlifecycle.FreshnessLive {
			label = "Open application"
		}
		_, _ = fmt.Fprintf(out, "    %s: %s\n", label, app.URL)
	}
	for index, action := range app.NextActions {
		label := "Then"
		if index == 0 {
			label = "Next"
		}
		_, _ = fmt.Fprintf(out, "    %s: %s\n", label, action)
	}
	if app.SetupAction != nil && app.Setup.Status != applicationlifecycle.AxisVerified && app.Setup.Status != applicationlifecycle.AxisNotRequired {
		if app.SetupAction.GuideURL != "" {
			_, _ = fmt.Fprintf(out, "    Setup guide: %s\n", app.SetupAction.GuideURL)
		}
		_, _ = fmt.Fprintf(out, "    Setup options: stackkit setup --help\n")
	}
	_, _ = fmt.Fprintln(out, "    Evidence and details: stackkit status --json")
}
