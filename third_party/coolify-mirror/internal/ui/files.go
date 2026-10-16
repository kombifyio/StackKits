package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mtalavi/coolify-mirror/internal/engine"

	"github.com/charmbracelet/huh"
)

// filesMenuName is how the other screens refer to the saved files screen.
const filesMenuName = "Saved files & disk space"

const (
	filesBackups  = "backups"
	filesAll      = "all"
	filesBack     = "back"
	allOfThemLine = "\x00all" // the "all of them" line at the top of the backups list
)

// savedFiles shows what coolify-mirror keeps on this server (backups,
// downloads, safety copies of restores, logs) and deletes what the user picks:
// all backups at once, some of them, or anything else kept here.
func savedFiles(ctx context.Context) error {
	list, err := withSpinner(ctx, "Reading what coolify-mirror keeps on this server", func(ctx context.Context) ([]engine.StoredFile, error) {
		return engine.ListStored(ctx)
	})
	if err != nil {
		return err
	}
	fmt.Println(boxed(sBox, storedSummary(list)))
	if len(list) == 0 {
		return nil
	}

	backups := engine.ReadyBackups(list)
	backupsWhat := "none kept here"
	if len(backups) > 0 {
		backupsWhat = fmt.Sprintf("%d backup(s) · %s - delete all of them or pick some", len(backups), engine.HumanBytes(engine.StoredTotal(backups)))
	}
	what := filesBackups
	err = huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("What do you want to clean up?").
			Options(
				menuOption("All backups", backupsWhat, filesBackups),
				menuOption("Everything kept here", fmt.Sprintf("%d item(s) · %s - backups, safety copies, logs…", len(list), engine.HumanBytes(engine.StoredTotal(list))), filesAll),
				menuOption("Back", "", filesBack),
			).
			Value(&what),
	)).WithTheme(theme()).WithKeyMap(keys()).WithShowHelp(true).RunWithContext(ctx)
	if err != nil {
		return err
	}
	switch what {
	case filesBack:
		return errBack
	case filesBackups:
		if len(backups) == 0 {
			fmt.Println(sMuted.Render("  No backups are kept on this server - nothing to delete there."))
			return nil
		}
		return pickAndDelete(ctx, "Which backups should be deleted?", "backup(s)", backups, true)
	}
	return pickAndDelete(ctx, "What should be deleted?", "item(s)", list, false)
}

// pickAndDelete lists items, lets the user pick (with an "all of them" line
// on top when withAll), confirms and deletes.
func pickAndDelete(ctx context.Context, title, noun string, items []engine.StoredFile, withAll bool) error {
	byName := map[string]engine.StoredFile{}
	var usable []engine.StoredFile // not in use
	for _, f := range items {
		byName[f.Name] = f
		if f.Busy == "" {
			usable = append(usable, f)
		}
	}
	desc := "enter = the highlighted line · space = tick several, then enter · ctrl+a all · esc back"
	var opts []huh.Option[string]
	if withAll && len(items) > 1 {
		opts = append(opts, huh.NewOption(allOfThemLabel(items, usable), allOfThemLine))
		desc = "first line = all of them · enter = the highlighted line · space = tick several, then enter · esc back"
	}
	for _, f := range items {
		opts = append(opts, huh.NewOption(storedLabel(f), f.Name))
	}
	height := listHeight(len(opts), desc)

	var chosen []string
	ms := huh.NewMultiSelect[string]()
	// pick turns the ticked lines (or the highlighted one) into what is deleted.
	pick := func(v []string) ([]engine.StoredFile, error) {
		if len(v) == 0 {
			if h, ok := ms.Hovered(); ok {
				v = []string{h}
			}
		}
		want := map[string]bool{}
		for _, n := range v {
			if n == allOfThemLine {
				if len(usable) == 0 {
					return nil, errors.New("all of them are in use right now - see [in use]")
				}
				for _, f := range usable {
					want[f.Name] = true
				}
				continue
			}
			if f := byName[n]; f.Busy != "" {
				return nil, fmt.Errorf("%s is in use: %s", f.Name, f.Busy)
			}
			want[n] = true
		}
		var out []engine.StoredFile
		for _, f := range items {
			if want[f.Name] {
				out = append(out, f)
			}
		}
		return out, nil
	}
	ms.Title(title).
		Description(desc).
		Options(opts...).
		Height(height).
		Value(&chosen).
		Validate(func(v []string) error {
			_, err := pick(v)
			return err
		})
	if err := huh.NewForm(huh.NewGroup(ms)).WithTheme(theme()).WithKeyMap(listKeys()).WithShowHelp(true).RunWithContext(ctx); err != nil {
		return err
	}
	todo, err := pick(chosen)
	if err != nil {
		return err
	}
	if len(todo) == 0 {
		return errBack
	}

	// At most 10 lines (plus every one with a warning), so the question
	// and Yes/No stay on the screen.
	var lines []string
	var more []engine.StoredFile
	for _, f := range todo {
		warns := engine.DeleteWarnings(f)
		if len(lines) >= 10 && len(warns) == 0 {
			more = append(more, f)
			continue
		}
		date := ""
		if !f.ModTime.IsZero() {
			date = f.ModTime.Format("2006-01-02 15:04") + "  "
		}
		lines = append(lines, fmt.Sprintf("• %-11s %s  %s%s", engine.KindLabel(f.Kind), sBold.Render(fmt.Sprintf("%9s", engine.HumanBytes(f.Size))), date, f.About))
		for _, w := range warns {
			lines = append(lines, sWarn.Render("  ! "+w))
		}
	}
	if len(more) > 0 {
		lines = append(lines, sMuted.Render(fmt.Sprintf("… and %d more (%s)", len(more), engine.HumanBytes(engine.StoredTotal(more)))))
	}
	if kept := len(items) - len(usable); kept > 0 && len(todo) == len(usable) && withAll {
		lines = append(lines, sMuted.Render(fmt.Sprintf("%d in use right now - left as they are.", kept)))
	}
	lines = append(lines, sMuted.Render("This cannot be undone."))
	ok, err := confirm(ctx, fmt.Sprintf("Delete %d %s and free %s?", len(todo), noun, engine.HumanBytes(engine.StoredTotal(todo))),
		strings.Join(lines, "\n"), false)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println(sMuted.Render("  Nothing was deleted."))
		return nil
	}
	return deleteStored(ctx, todo)
}

// deleteStored deletes the picked files one by one and shows what was freed.
func deleteStored(ctx context.Context, todo []engine.StoredFile) error {
	var freed int64
	var failed []string
	for _, f := range todo {
		_, err := withSpinner(ctx, "Deleting "+f.Name, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, engine.DeleteStored(ctx, f)
		})
		if err != nil {
			failed = append(failed, f.Name)
			continue
		}
		freed += f.Size
	}
	free, _ := engine.DiskSpace(engine.HomeDir)
	head := sOK.Render("Freed "+engine.HumanBytes(freed)) + sMuted.Render(" · "+engine.HumanBytes(free)+" free on the disk now")
	if len(failed) > 0 {
		head += "\n" + sErr.Render(fmt.Sprintf("%d item(s) were not deleted (see above)", len(failed)))
	}
	fmt.Println(boxed(sBox, head))
	if len(failed) > 0 {
		return errShown
	}
	return nil
}

// allOfThemLabel is the "all of them" line, in the columns of storedLabel.
func allOfThemLabel(items, usable []engine.StoredFile) string {
	l := fmt.Sprintf("%-11s %9s  all %d backups in this list", "ALL", engine.HumanBytes(engine.StoredTotal(usable)), len(usable))
	if kept := len(items) - len(usable); kept > 0 {
		l += fmt.Sprintf(" (%d in use are left)", kept)
	}
	return truncate(l, termWidth()-10)
}

// storedSummary: what is kept here, per kind, and the free disk space.
func storedSummary(list []engine.StoredFile) string {
	free, total := engine.DiskSpace(engine.HomeDir)
	disk := sMuted.Render(fmt.Sprintf("Disk: %s free of %s", engine.HumanBytes(free), engine.HumanBytes(total)))
	if len(list) == 0 {
		return sOK.Render("Nothing is kept here") + "\n" +
			sMuted.Render("No backups, downloads or safety copies - coolify-mirror uses no disk space on this server.") + "\n" + disk
	}
	var b strings.Builder
	b.WriteString(sTitle.Render("Saved by coolify-mirror on this server") + "  " + sBold.Render(engine.HumanBytes(engine.StoredTotal(list))) + "\n")
	kinds := []struct{ kind, label, hint string }{
		{engine.StoredBackup, "Backups made here", "delete one after the new server has restored it"},
		{engine.StoredDownload, "Downloaded backups", "kept because a restore did not finish"},
		{engine.StoredPartial, "Unfinished", "interrupted backups or downloads"},
		{engine.StoredSafety, "Safety copies", "what a restore replaced - delete once everything works"},
		{engine.StoredVolume, "Old volume data", "set aside by a restore - delete once everything works"},
		{engine.StoredLogs, "Logs", ""},
		{engine.StoredTemp, "Leftovers", "from interrupted runs"},
	}
	for _, k := range kinds {
		fs := engine.StoredOf(list, k.kind)
		if len(fs) == 0 {
			continue
		}
		n := fmt.Sprint(len(fs))
		if k.kind == engine.StoredLogs {
			n = "-"
		}
		b.WriteString(fmt.Sprintf("  %-19s %3s  %9s  %s\n", k.label, n, engine.HumanBytes(engine.StoredTotal(fs)), sMuted.Render(k.hint)))
	}
	b.WriteString(disk)
	return b.String()
}

// storedLabel is one line of the list: kind, size, date, what it is.
func storedLabel(f engine.StoredFile) string {
	date := "                "
	if !f.ModTime.IsZero() {
		date = f.ModTime.Format("2006-01-02 15:04")
	}
	l := fmt.Sprintf("%-11s %9s  %s  %s", engine.KindLabel(f.Kind), engine.HumanBytes(f.Size), date, f.About)
	// The marks go before the file name, which is the part cut on a narrow screen.
	if len(f.Shares) > 0 {
		l += "  ↔ shared now"
	}
	if f.Busy != "" {
		l += "  [in use]"
	}
	switch f.Kind {
	case engine.StoredBackup, engine.StoredDownload, engine.StoredPartial:
		l += "  · " + f.Name
	}
	return truncate(l, termWidth()-10)
}

// usageLine is the one-line note at start: what coolify-mirror keeps here
// ("" when nothing).
func usageLine() string {
	n, size := engine.UsageSummary()
	if size < 1<<20 {
		return ""
	}
	what := engine.HumanBytes(size) + " kept by coolify-mirror"
	if n > 0 {
		what += fmt.Sprintf(" (%d backup(s))", n)
	}
	free, _ := engine.DiskSpace(engine.HomeDir)
	return sMuted.Render("  "+what+" · "+engine.HumanBytes(free)+" free · free space: menu → ") + sAccent.Render(filesMenuName)
}
