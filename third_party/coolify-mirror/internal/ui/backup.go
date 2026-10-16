package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
	"github.com/mtalavi/coolify-mirror/internal/engine"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// termWidth is the terminal width (100 when unknown).
func termWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 20 {
		return w
	}
	return 100
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// pad fills s with spaces to n terminal cells.
func pad(s string, n int) string {
	if w := lipgloss.Width(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

// domainText is the first domain and how many more there are.
func domainText(ds []string) string {
	switch len(ds) {
	case 0:
		return "(no domain)"
	case 1:
		return coolify.ShowHost(ds[0])
	}
	main := append([]string(nil), ds...)
	coolify.SortMainFirst(main)
	return fmt.Sprintf("%s +%d", coolify.ShowHost(main[0]), len(ds)-1)
}

// resourceState is state() for one resource; a service with some of its
// containers down counts as partly running.
func resourceState(r coolify.Resource) string {
	switch {
	case r.Running():
		return state(1, 1)
	case r.Status == "degraded":
		return sWarn.Render("◐ partly running")
	}
	return state(0, 1)
}

// resourceLabel renders one line: domain(s), name, type, project/env, state.
// It is shortened to fit the terminal so options never wrap.
func resourceLabel(r coolify.Resource) string {
	w := termWidth()
	st := resourceState(r)
	if !r.Local() {
		st = sWarn.Render("remote server " + r.ServerName + " - not supported")
	}
	avail := w - listMargin - 2 - lipgloss.Width(st)
	if avail < 16+2+12 {
		// Narrow terminal: the name and its state only.
		n := clamp(avail, 6, avail)
		return pad(truncate(r.Name, n), n) + "  " + st
	}
	domW := clamp(w/3, 16, 34)
	room := clamp(avail-domW-2, 8, avail)
	name := truncate(r.Name, room)
	rest := room - lipgloss.Width(name)
	meta := truncate(" · "+r.Label()+" · "+r.Project+"/"+r.Environment, rest)
	return pad(truncate(domainText(r.Domains), domW), domW) + "  " + name + sMuted.Render(pad(meta, rest)) + "  " + st
}

// listHeight is the height a list needs to show its options under a title
// and a description that may wrap on a narrow terminal.
func listHeight(options int, desc string) int {
	w := termWidth() - 4
	lines := 1
	for _, l := range strings.Split(desc, "\n") {
		lines += clamp((lipgloss.Width(l)+w-1)/w, 1, 10)
	}
	return clamp(options+lines+1, 6, 24)
}

// listMargin is what a list adds in front of each option: border, cursor,
// tick box, and a little room.
const listMargin = 12

// projectState renders whether a project runs: everything, partly, nothing.
func projectState(p coolify.Project) string {
	switch p.RunState() {
	case "running":
		return state(1, 1)
	case "partly":
		if p.Degraded() > 0 {
			return sWarn.Render("◐ partly running")
		}
		return state(p.Running(), len(p.Resources))
	}
	return state(0, 1)
}

// projectLabel renders one project line: name, domain(s), what is in it,
// state. Columns are left out as the terminal gets narrower.
func projectLabel(p coolify.Project) string {
	w := termWidth()
	st := projectState(p)
	if local, _ := p.Local(); len(local) == 0 {
		st = sWarn.Render("remote server - not supported")
	}
	avail := w - listMargin - 2 - lipgloss.Width(st)
	title, dom := p.Title(), domainText(p.Domains())
	switch {
	case avail >= 14+2+16+2+6:
		nameW := clamp(w/5, 14, 26)
		domW := clamp(w/4, 16, 32)
		line := pad(truncate(title, nameW), nameW) + "  " + pad(truncate(dom, domW), domW) + "  "
		if kindsW := avail - nameW - 2 - domW - 2; kindsW >= 6 {
			line += sMuted.Render(pad(truncate(p.Kinds(), kindsW), kindsW)) + "  "
		}
		return line + st
	case avail >= 10+2+10:
		nameW := clamp(avail/2, 10, 26)
		domW := avail - nameW - 2
		return pad(truncate(title, nameW), nameW) + "  " + pad(truncate(dom, domW), domW) + "  " + st
	}
	n := clamp(avail, 6, avail)
	return pad(truncate(title, n), n) + "  " + st
}

// errSingle: the user wants to pick single resources instead of projects.
var errSingle = errors.New("pick single resources")

const pickSingleLine = "\x00single"

// pickProjects asks which projects to back up. Everything in a project moves
// together; the last line switches to picking single resources.
func pickProjects(ctx context.Context, projects []coolify.Project) ([]coolify.Project, error) {
	byKey := map[string]coolify.Project{}
	var opts []huh.Option[string]
	for i, p := range projects {
		k := strconv.Itoa(i)
		byKey[k] = p
		opts = append(opts, huh.NewOption(projectLabel(p), k))
	}
	single := "Pick single apps instead (advanced) →"
	if lipgloss.Width(single) > termWidth()-listMargin {
		single = "Single apps instead →"
	}
	opts = append(opts, huh.NewOption(sMuted.Render(single), pickSingleLine))
	var chosen []string
	ms := huh.NewMultiSelect[string]()
	// Enter alone takes the highlighted line; space ticks several first.
	pick := func(v []string) []string {
		if len(v) == 0 {
			if h, ok := ms.Hovered(); ok {
				v = []string{h}
			}
		}
		return v
	}
	desc := "Everything in a project moves together: its apps, databases, services and domains.\n" +
		"↑/↓ move · enter = the highlighted one · several: space on each, then enter · / search · esc back"
	ms.Title("Which project should be backed up?").
		Description(desc).
		Options(opts...).
		Filterable(true).
		Height(listHeight(len(opts), desc)).
		Value(&chosen).
		Validate(func(v []string) error {
			v = pick(v)
			for _, k := range v {
				if k == pickSingleLine {
					if len(v) > 1 {
						return errors.New("untick the projects to pick single apps (or untick that line)")
					}
					continue
				}
				if local, _ := byKey[k].Local(); len(local) == 0 {
					return fmt.Errorf("%s runs on a remote server - only resources on this server can be backed up", byKey[k].Title())
				}
			}
			return nil
		})
	if err := huh.NewForm(huh.NewGroup(ms)).WithTheme(theme()).WithKeyMap(listKeys()).WithShowHelp(true).RunWithContext(ctx); err != nil {
		return nil, err
	}
	chosen = pick(chosen)
	if len(chosen) == 0 {
		return nil, errBack
	}
	var out []coolify.Project
	for _, k := range chosen {
		if k == pickSingleLine {
			return nil, errSingle
		}
		out = append(out, byKey[k])
	}
	return out, nil
}

// pickResources asks which single resources to back up.
func pickResources(ctx context.Context, all []coolify.Resource) ([]coolify.Resource, error) {
	byUUID := map[string]coolify.Resource{}
	var opts []huh.Option[string]
	for _, r := range all {
		byUUID[r.UUID] = r
		opts = append(opts, huh.NewOption(resourceLabel(r), r.UUID))
	}
	var chosen []string
	// Enter alone takes the highlighted line; space ticks several first.
	ms := huh.NewMultiSelect[string]()
	desc := "↑/↓ move · enter = the highlighted one · several: space on each, then enter · / search · esc back"
	ms.Title("Which app should be backed up?").
		Description(desc).
		Options(opts...).
		Filterable(true).
		Height(listHeight(len(opts), desc)).
		Value(&chosen).
		Validate(func(v []string) error {
			if len(v) == 0 {
				if h, ok := ms.Hovered(); ok {
					v = []string{h}
				}
			}
			for _, u := range v {
				if r := byUUID[u]; !r.Local() {
					return fmt.Errorf("%s runs on remote server %s - only resources on this server can be backed up", r.Name, r.ServerName)
				}
			}
			return nil
		})
	if err := huh.NewForm(huh.NewGroup(ms)).WithTheme(theme()).WithKeyMap(listKeys()).WithShowHelp(true).RunWithContext(ctx); err != nil {
		return nil, err
	}
	if len(chosen) == 0 {
		if h, ok := ms.Hovered(); ok {
			chosen = []string{h}
		}
	}
	if len(chosen) == 0 {
		return nil, errBack
	}
	var sel []coolify.Resource
	for _, u := range chosen {
		sel = append(sel, byUUID[u])
	}
	return sel, nil
}

func backupSelected(ctx context.Context, in *coolify.Instance) error {
	all, err := withSpinner(ctx, "Reading projects and domains", func(ctx context.Context) ([]coolify.Resource, error) {
		return in.ListResources(ctx)
	})
	if err != nil {
		return err
	}
	if len(all) == 0 {
		fmt.Println(sWarn.Render("  This Coolify has no resources yet."))
		return errBack
	}

	var sel []coolify.Resource
	picked, err := pickProjects(ctx, coolify.GroupProjects(all))
	switch {
	case errors.Is(err, errSingle):
		if sel, err = pickResources(ctx, all); err != nil {
			return err
		}
		for _, r := range sel {
			fmt.Println("  " + sOK.Render("›") + " " + r.Name + sMuted.Render(" · "+r.Label()+" · "+r.Project))
		}
	case err != nil:
		return err
	default:
		for _, p := range picked {
			local, remote := p.Local()
			fmt.Println("  " + sOK.Render("›") + " " + sBold.Render(p.Title()) + sMuted.Render(fmt.Sprintf("  %d · %s", len(local), p.Kinds())))
			if len(remote) > 0 {
				fmt.Println("    " + sWarn.Render(fmt.Sprintf("! %d of its resources run on a remote server and are not included", len(remote))))
			}
			sel = append(sel, local...)
		}
	}

	deps, err := withSpinner(ctx, "Looking for databases and services they depend on", func(ctx context.Context) ([]engine.Dependency, error) {
		return engine.ResolveDependencies(ctx, in, sel, all)
	})
	if err != nil {
		return err
	}
	var added []engine.Dependency
	if len(deps) > 0 {
		var lines []string
		for _, d := range deps {
			lines = append(lines, fmt.Sprintf("• %s (%s, project %s) — %s", d.Resource.Name, d.Resource.Label(), d.Resource.Project, d.Reason))
		}
		include, err := confirm(ctx, "Also back up what they depend on? (recommended)", strings.Join(lines, "\n"), true)
		if err != nil {
			return err
		}
		if include {
			added = deps
			for _, d := range deps {
				sel = append(sel, d.Resource)
			}
		}
	}

	req := engine.BackupRequest{Mode: engine.ModeSelective, Resources: sel}
	if err := reviewBackup(ctx, &req, selectionSummary(sel, added), false); err != nil {
		return err
	}
	return runBackup(ctx, in, req)
}

// selectionSummary lists what a selective backup contains, by project.
func selectionSummary(sel []coolify.Resource, added []engine.Dependency) string {
	why := map[string]string{}
	for _, d := range added {
		why[d.Resource.UUID] = d.Reason
	}
	head := sBold.Render(fmt.Sprintf("%d project(s) · %d resource(s)", len(coolify.GroupProjects(sel)), len(sel)))
	return head + "\n" + resourceTree(sel, why)
}

// resourceTree lists resources under their project: name, type, domain (or
// why it was added). Long lists end with "… and N more".
func resourceTree(sel []coolify.Resource, why map[string]string) string {
	projects := coolify.GroupProjects(sel)
	var lines []string
	nameW := 4
	for _, r := range sel {
		nameW = clamp(lipgloss.Width(r.Name), nameW, 28)
	}
	const most = 14
	shown := 0
	for _, p := range projects {
		if shown >= most {
			break
		}
		lines = append(lines, "  "+sAccent.Render(p.Title()))
		for _, r := range p.Resources {
			if shown >= most {
				break
			}
			shown++
			extra := ""
			if len(r.Domains) > 0 {
				extra = domainText(r.Domains)
			}
			if w, ok := why[r.UUID]; ok {
				extra = "added: " + w
			}
			lines = append(lines, "    "+pad(truncate(r.Name, nameW), nameW)+"  "+sMuted.Render(pad(r.Label(), 12)+extra))
		}
	}
	if more := len(sel) - shown; more > 0 {
		lines = append(lines, sMuted.Render(fmt.Sprintf("    … and %d more", more)))
	}
	return strings.Join(lines, "\n")
}

func backupFull(ctx context.Context, in *coolify.Instance) error {
	all, err := withSpinner(ctx, "Reading the Coolify configuration", func(ctx context.Context) ([]coolify.Resource, error) {
		return in.ListResources(ctx)
	})
	if err != nil {
		return err
	}
	local, remote := 0, 0
	for _, r := range all {
		if r.Local() {
			local++
		} else {
			remote++
		}
	}
	what := sBold.Render("The whole server") + "\n" +
		sMuted.Render(fmt.Sprintf("  Coolify's database (projects, settings, users, keys, env vars), the APP_KEY,\n"+
			"  /data/coolify (proxy, certificates, compose files) and the data of\n"+
			"  %d resource(s) in %d project(s) on this server.", local, len(coolify.GroupProjects(all))))
	if remote > 0 {
		what += "\n" + sWarn.Render(fmt.Sprintf("  %d resource(s) run on remote servers: their settings are included, their data stays on those servers.", remote))
	}
	req := engine.BackupRequest{Mode: engine.ModeFull}
	if err := reviewBackup(ctx, &req, what, true); err != nil {
		return err
	}
	return runBackup(ctx, in, req)
}

const (
	reviewStart    = "start"
	reviewSettings = "settings"
	reviewBack     = "back"
)

// reviewBackup shows what will be backed up and with which settings, then
// asks to start, change the settings or go back. Nothing starts before that.
func reviewBackup(ctx context.Context, req *engine.BackupRequest, what string, full bool) error {
	req.Consistency = engine.ConsistencyPause
	req.Images = engine.ImagesApps
	for {
		fmt.Println(boxed(sBox, sTitle.Render("Ready to back up")+"\n"+what+"\n\n"+sMuted.Render("Settings  ")+settingsText(req, full)))
		next := reviewStart
		if err := huh.NewForm(huh.NewGroup(huh.NewSelect[string]().
			Title("Start the backup?").
			Options(
				huh.NewOption("Start the backup", reviewStart),
				huh.NewOption("Change the settings", reviewSettings),
				huh.NewOption("Back to the menu", reviewBack),
			).Value(&next))).WithTheme(theme()).WithKeyMap(keys()).WithShowHelp(true).RunWithContext(ctx); err != nil {
			return err
		}
		switch next {
		case reviewStart:
			return nil
		case reviewBack:
			return errBack
		}
		if err := customSettings(ctx, req, full); err != nil {
			return err
		}
	}
}

// settingsText describes the backup settings in one line.
func settingsText(req *engine.BackupRequest, full bool) string {
	var parts []string
	switch req.Consistency {
	case engine.ConsistencyStop:
		parts = append(parts, "stop and start the containers again")
	case engine.ConsistencyLive:
		parts = append(parts, "containers keep running (databases may be inconsistent)")
	default:
		parts = append(parts, "containers paused for a moment")
	}
	switch req.Images {
	case engine.ImagesAll:
		parts = append(parts, "all images included")
	case engine.ImagesNone:
		parts = append(parts, "no images (the new server pulls or builds them)")
	default:
		parts = append(parts, "app images included (no rebuild)")
	}
	if full && req.IncludeBackups {
		parts = append(parts, "with Coolify's database backup files")
	}
	text := strings.Join(parts, " · ")
	if req.Consistency == engine.ConsistencyPause && req.Images == engine.ImagesApps && !req.IncludeBackups {
		return sOK.Render("Recommended") + sMuted.Render(" - "+text)
	}
	return text
}

// customSettings lets the user choose each backup setting.
func customSettings(ctx context.Context, req *engine.BackupRequest, full bool) error {
	fields := []huh.Field{
		huh.NewSelect[string]().
			Title("Running containers while their data is copied").
			Options(
				huh.NewOption("Pause them for a moment (recommended, safe for databases)", engine.ConsistencyPause),
				huh.NewOption("Stop and start them again (safest, short downtime)", engine.ConsistencyStop),
				huh.NewOption("Don't touch them (no interruption, databases may be inconsistent)", engine.ConsistencyLive),
			).Value(&req.Consistency),
		huh.NewSelect[string]().
			Title("Docker images").
			Options(
				huh.NewOption("Include application images (recommended: no rebuild on the new server)", engine.ImagesApps),
				huh.NewOption("Include all images (also for services/databases; works offline)", engine.ImagesAll),
				huh.NewOption("No images (smaller; the new server pulls/builds them)", engine.ImagesNone),
			).Value(&req.Images),
	}
	if full {
		fields = append(fields, huh.NewConfirm().
			Title("Include Coolify's own database backup files (/data/coolify/backups)?").
			Affirmative("Yes").Negative("No").Value(&req.IncludeBackups))
	}
	return huh.NewForm(huh.NewGroup(fields...)).WithTheme(theme()).WithKeyMap(keys()).WithShowHelp(true).RunWithContext(ctx)
}

func runBackup(ctx context.Context, in *coolify.Instance, req engine.BackupRequest) error {
	pr := engine.NewProgress("Backup")
	var res *engine.BackupResult
	title := "Creating backup"
	if req.Mode == engine.ModeFull {
		title = "Creating FULL backup"
	}
	err := runWithProgress(ctx, title, pr, func(ctx context.Context) error {
		var err error
		res, err = engine.Backup(ctx, in, req, pr)
		return err
	})
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(sOK.Render("Backup complete") + "\n")
	b.WriteString(fmt.Sprintf("%s  %s\n", sMuted.Render("File "), res.Path))
	b.WriteString(fmt.Sprintf("%s  %s  %s\n", sMuted.Render("Size "), sBold.Render(engine.HumanBytes(res.Size)),
		sMuted.Render(fmt.Sprintf("(%s of data, %d resource(s), %s)", engine.HumanBytes(res.RawBytes), len(res.Manifest.Resources), engine.HumanDuration(res.Duration)))))
	b.WriteString(fmt.Sprintf("%s  %s\n", sMuted.Render("Key  "), res.Key))
	b.WriteString(sMuted.Render("Kept on this server until you delete it (menu → " + filesMenuName + ")"))
	fmt.Println(boxed(sBox, b.String()))
	return offerShare(ctx, in, res.Path, res.Key)
}
