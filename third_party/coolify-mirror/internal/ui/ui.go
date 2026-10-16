// Package ui is the interactive terminal interface (arrow keys and enter;
// space ticks several lines in a list). Everything it does is also available as plain
// commands (see main.go), which share the same engine.
package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
	"github.com/mtalavi/coolify-mirror/internal/engine"

	"github.com/charmbracelet/huh"
)

// LogPath is shown to the user when something fails.
var LogPath string

const (
	actBackupSelected = "backup"
	actBackupFull     = "full"
	actRestore        = "restore"
	actShare          = "share"
	actFiles          = "files"
	actGuide          = "guide"
	actQuit           = "quit"
)

// menuOption is one line of the main menu: a short name and what it does.
func menuOption(name, what, value string) huh.Option[string] {
	return huh.NewOption(pad(name, 26)+" "+sMuted.Render(what), value)
}

// mainOption is a line of the main menu with its symbol in front.
func mainOption(icon, name, what, value string) huh.Option[string] {
	return huh.NewOption(pad(icon+"  "+name, 29)+" "+sMuted.Render(what), value)
}

// Run starts the interactive menu.
func Run(ctx context.Context) error {
	Banner()
	fmt.Println()
	in, err := checkServer(ctx)
	if err != nil {
		return err
	}

	for {
		action := ""
		err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("What do you want to do?").
				Description("Old server: back up and share · new server: restore with the code").
				Options(
					mainOption("↑", "Back up apps", "pick projects: everything in them moves together", actBackupSelected),
					mainOption("⇑", "Back up the whole server", "Coolify itself, its settings and every project", actBackupFull),
					mainOption("↓", "Restore a backup", "type the share code from the other server", actRestore),
					mainOption("↔", "Share a saved backup", "send a backup made earlier to another server", actShare),
					mainOption("≡", filesMenuName, "see and delete old backups (all or some), safety copies", actFiles),
					mainOption("?", "How it works", "step-by-step guide · "+strings.TrimPrefix(engine.SiteURL, "https://"), actGuide),
					mainOption("×", "Quit", "", actQuit),
				).
				Value(&action),
		)).WithTheme(theme()).WithKeyMap(keys()).WithShowHelp(true).RunWithContext(ctx)
		if errors.Is(err, huh.ErrUserAborted) || action == actQuit {
			return nil
		}
		if err != nil {
			return err
		}
		switch action {
		case actBackupSelected:
			err = backupSelected(ctx, in)
		case actBackupFull:
			err = backupFull(ctx, in)
		case actRestore:
			err = restoreFlow(ctx, in)
			// A full restore swaps the APP_KEY: detect again for further actions.
			if nin, derr := coolify.Detect(ctx); derr == nil {
				in = nin
			}
		case actShare:
			err = shareExisting(ctx, in)
		case actFiles:
			err = savedFiles(ctx)
		case actGuide:
			printGuide()
		}
		if errors.Is(err, errNotRestored) {
			printNotRestored()
			fmt.Println()
			continue
		}
		if errors.Is(err, errShown) {
			fmt.Println()
			continue
		}
		if err != nil {
			if errors.Is(err, huh.ErrUserAborted) || errors.Is(err, errBack) {
				fmt.Println(sMuted.Render("  (back to the menu)"))
				fmt.Println()
				continue
			}
			if errors.Is(err, context.Canceled) && ctx.Err() != nil {
				return err
			}
			showError(err)
		}
		fmt.Println()
	}
}

// RunRestore restores a share code, link or file interactively (the menu's
// restore, without the menu).
func RunRestore(ctx context.Context, source string) error {
	Banner()
	fmt.Println()
	in, err := checkServer(ctx)
	if err != nil {
		return err
	}
	err = restoreSource(ctx, in, source)
	switch {
	case errors.Is(err, errNotRestored):
		printNotRestored()
		return errors.New("not restored")
	case errors.Is(err, huh.ErrUserAborted), errors.Is(err, errBack):
		return errors.New("not restored")
	case errors.Is(err, errShown):
		return errors.New("restored, but not everything is running - see above")
	case err != nil:
		showError(err)
		return err
	}
	return nil
}

// checkServer detects Coolify, checks that this version of coolify-mirror
// works with it and whether a newer release exists, and prints the result.
func checkServer(ctx context.Context) (*coolify.Instance, error) {
	newer := make(chan string, 1)
	go func() { newer <- engine.NewerRelease(ctx) }()
	var compat *engine.Compat
	in, err := withSpinner(ctx, "Checking this server", func(ctx context.Context) (*coolify.Instance, error) {
		in, err := coolify.Detect(ctx)
		if err == nil {
			compat = engine.CheckCompat(ctx, in)
		}
		return in, err
	})
	if err != nil {
		return nil, err
	}
	if msg := engine.RecoverPaused(ctx); msg != "" {
		fmt.Println("  " + sWarn.Render("! "+msg))
	}
	v4, _ := in.PublicIPs(ctx)
	state := sOK.Render("✓ tested with this version")
	switch {
	case len(compat.Blockers) > 0:
		state = sErr.Render("✗ not supported")
	case compat.Failed != "":
		state = sErr.Render("✗ restore not supported")
	case compat.Status != "tested":
		state = sWarn.Render("not tested with this version yet")
	}
	fmt.Println(rule("This server"))
	fmt.Println("  " + sBold.Render("Coolify "+in.Version) + sMuted.Render(fmt.Sprintf(" on %s (%s) · ", in.Hostname, v4)) + state)
	for _, b := range compat.RestoreBlockers() {
		fmt.Println("  " + sErr.Render("✗ "+b))
	}
	for _, w := range compat.Warnings {
		fmt.Println("  " + sWarn.Render("! "+w))
	}
	if u := usageLine(); u != "" {
		fmt.Println(u)
	}
	select {
	case v := <-newer:
		if v != "" {
			fmt.Println("  " + sAccent.Render("★ coolify-mirror "+v+" is available") + sMuted.Render(" - update with: sudo coolify-mirror update"))
		}
	case <-time.After(1500 * time.Millisecond):
	}
	fmt.Println()
	return in, nil
}

// printGuide explains a move from start to end.
func printGuide() {
	step := func(n, title string) string { return sAccent.Render(n) + "  " + sBold.Render(title) }
	cmd := func(s string) string { return "     " + sLink.Render(s) }
	note := func(s string) string { return "     " + sMuted.Render(s) }
	lines := []string{
		sTitle.Render("How a move works"),
		"",
		step("1", "On the OLD server - make the backup"),
		cmd("sudo coolify-mirror"),
		note("→ Back up apps → ↑/↓ to the project, enter"),
		note("  everything in the project moves together (apps, databases, domains)"),
		note("  several projects: tick each with space, then enter"),
		note("  one app only: the last line, Pick single apps instead"),
		note("→ Ready to back up: check the list → Start the backup → share through port 443"),
		note("→ a share code and one command for the new server are shown"),
		"",
		step("2", "On the NEW server - restore"),
		note("run the one command the old server showed:"),
		cmd("curl -fsSL …/install.sh | sudo sh -s restore 1.2.3.4/abcd-efgh-…"),
		note("or: sudo coolify-mirror → Restore a backup → type the code"),
		note("it downloads, verifies, restores, starts and checks every app"),
		note("domains can be changed at the end; nothing changes before you answer Yes"),
		"",
		step("3", "Switch"),
		note("check the apps in the new Coolify, then point the domains' DNS to the new server"),
		note("turn off scheduled tasks / backups on the old server"),
		"",
		step("4", "Free the disk space - on both servers"),
		note("sudo coolify-mirror → " + filesMenuName),
		note("→ All backups: delete all of them at once, or tick only some"),
		note("→ Everything kept here: also the safety copies of restores and the logs"),
		note("old server: the backup file · new server: safety copies, once everything works"),
		"",
		sMuted.Render("Keys: ↑/↓ move · enter choose · space tick · / search · esc back (in the menu: quit) · ctrl+c stop"),
		sMuted.Render("Without the menu: coolify-mirror help"),
		sMuted.Render("Guide with a screenshot of every screen: ") + sAccent.Render(engine.SiteURL),
	}
	fmt.Println(boxed(sBox, strings.Join(lines, "\n")))
}

func printNotRestored() {
	fmt.Println(boxed(sErrBox, sWarn.Render("Not restored")+"\n"+
		"Nothing was changed on this server: the restore was cancelled before it started.\n"+
		sMuted.Render("To restore, start the restore again and answer Yes.")))
}

var (
	errBack        = errors.New("back")
	errNotRestored = errors.New("restore cancelled")
	errShown       = errors.New("problem already shown")
)

func showError(err error) {
	msg := err.Error()
	if LogPath != "" {
		msg += "\n\n" + sMuted.Render("Details: "+LogPath)
	}
	fmt.Println(boxed(sErrBox, sErr.Render("Failed")+"\n"+wrap(msg, termWidth()-8)))
}

func wrap(s string, w int) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		for len([]rune(line)) > w {
			r := []rune(line)
			cut := w
			for i := w; i > w/2; i-- {
				if r[i] == ' ' {
					cut = i
					break
				}
			}
			out = append(out, string(r[:cut]))
			line = strings.TrimLeft(string(r[cut:]), " ")
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func confirm(ctx context.Context, title, desc string, def bool) (bool, error) {
	ok := def
	err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(title).Description(desc).Affirmative("Yes").Negative("No").Value(&ok),
	)).WithTheme(theme()).WithKeyMap(keys()).RunWithContext(ctx)
	return ok, err
}

func isRoot() bool { return os.Geteuid() == 0 }
