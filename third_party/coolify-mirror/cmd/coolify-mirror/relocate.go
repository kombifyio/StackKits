package main

// plan and check (kombify fork addition, see KOMBIFY-FORK.md): read-only
// relocation preflight with machine-readable output for kombify StackKits.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
	"github.com/mtalavi/coolify-mirror/internal/engine"
)

// exitNotReady is returned by check when the move is blocked.
const exitNotReady = 3

type notReadyError struct{ n int }

func (e notReadyError) Error() string { return fmt.Sprintf("the move is blocked (%d blocker(s))", e.n) }

func cmdPlan(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	full := fs.Bool("full", false, "")
	projects := fs.String("project", "", "")
	domains := fs.String("domain", "", "")
	uuids := fs.String("uuid", "", "")
	all := fs.Bool("all", false, "")
	noDeps := fs.Bool("no-deps", false, "")
	images := fs.String("images", engine.ImagesApps, "")
	asJSON := fs.Bool("json", false, "")
	output := fs.String("output", "", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Plain coolify.Detect: detect() would resume containers left paused by
	// an interrupted backup, and a plan changes nothing.
	in, err := coolify.Detect(ctx)
	if err != nil {
		return err
	}
	req := engine.BackupRequest{Mode: engine.ModeSelective, Images: *images}
	var added []string
	if *full {
		req.Mode = engine.ModeFull
	} else {
		rs, err := in.ListResources(ctx)
		if err != nil {
			return err
		}
		sel, err := selectResourcesQuiet(rs, *all, splitList(*projects), splitList(*domains), splitList(*uuids))
		if err != nil {
			return err
		}
		if !*noDeps {
			deps, err := engine.ResolveDependencies(ctx, in, sel, rs)
			if err != nil {
				return err
			}
			for _, d := range deps {
				added = append(added, fmt.Sprintf("added %s %q - %s", d.Resource.Label(), d.Resource.Name, d.Reason))
				sel = append(sel, d.Resource)
			}
		}
		req.Resources = sel
	}
	p, err := engine.Plan(ctx, in, req)
	if err != nil {
		return err
	}
	if len(added) > 0 {
		p.Warnings = append(added, p.Warnings...)
	}
	if *asJSON || *output != "" {
		return writeJSON(*output, p)
	}
	m := p.Manifest
	fmt.Printf("Plan (%s) on %s, Coolify %s, %s\n", m.Mode, m.Source.Hostname, m.Source.CoolifyVersion, m.Source.Arch)
	for _, r := range m.Resources {
		fmt.Printf("  %-10s %-30s %s\n", r.Label(), r.Name, r.UUID)
	}
	fmt.Printf("  %d volume(s), %d path(s), %d image group(s), %d database dump(s), about %s\n",
		len(m.Volumes), len(m.Paths), len(m.Images), len(m.Dumps), engine.HumanBytes(m.TotalBytes))
	printList("blocked", p.Blockers)
	printList("warning", p.Warnings)
	if len(p.Blockers) > 0 {
		return notReadyError{len(p.Blockers)}
	}
	return nil
}

func cmdCheck(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	planPath := fs.String("plan", "", "")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *planPath == "" {
		return errors.New("check needs --plan FILE (or - for stdin), made by `coolify-mirror plan --json` on the source")
	}
	var r io.Reader = os.Stdin
	if *planPath != "-" {
		f, err := os.Open(*planPath)
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	}
	var p engine.RelocationPlan
	dec := json.NewDecoder(io.LimitReader(r, 64<<20))
	if err := dec.Decode(&p); err != nil {
		return fmt.Errorf("cannot read the plan: %w", err)
	}
	in, err := coolify.Detect(ctx)
	if err != nil {
		return err
	}
	c, err := engine.CheckPlan(ctx, in, &p)
	if err != nil {
		return err
	}
	if *asJSON {
		if err := writeJSON("", c); err != nil {
			return err
		}
	} else {
		fmt.Printf("Check on %s, Coolify %s, %s: plan from %s (%s, %d resource(s))\n", c.Destination.Hostname,
			c.Destination.CoolifyVersion, c.Destination.Arch, c.PlanSource.Hostname, c.Mode, len(p.Manifest.Resources))
		printList("blocked", c.Blockers)
		printList("warning", c.Warnings)
		if c.Ready {
			fmt.Println("ready: the restore can run on this server")
		}
	}
	if !c.Ready {
		return notReadyError{len(c.Blockers)}
	}
	return nil
}

func writeJSON(path string, v any) error {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if path == "" || path == "-" {
		_, err = os.Stdout.Write(out)
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

func printList(label string, items []string) {
	for _, s := range items {
		fmt.Printf("%s: %s\n", label, s)
	}
}

// selectResourcesQuiet is selectResources without its notes on stdout, which
// would break the JSON output.
func selectResourcesQuiet(rs []coolify.Resource, all bool, projects, domains, uuids []string) ([]coolify.Resource, error) {
	stdout := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err == nil {
		os.Stdout = devnull
		defer func() { os.Stdout = stdout; devnull.Close() }()
	}
	return selectResources(rs, all, projects, domains, uuids)
}

func cmdDrain(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("drain", flag.ContinueOnError)
	planPath := fs.String("plan", "", "")
	output := fs.String("output", "", "")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *planPath == "" {
		return errors.New("drain needs --plan FILE, made by `coolify-mirror plan --json` on this server")
	}
	p, err := readPlan(*planPath)
	if err != nil {
		return err
	}
	in, err := detect(ctx)
	if err != nil {
		return err
	}
	if compat := engine.CheckCompat(ctx, in); len(compat.Blockers) > 0 {
		return errors.New(strings.Join(compat.Blockers, "; "))
	}
	rec, err := engine.Drain(ctx, in, p)
	if rec != nil && *output != "" {
		// The record is written even for a partial drain: undrain needs it.
		if werr := writeJSON(*output, rec); werr != nil && err == nil {
			err = werr
		}
	}
	if err != nil {
		return err
	}
	if *asJSON {
		if err := writeJSON("", rec); err != nil {
			return err
		}
	} else {
		for _, d := range rec.Resources {
			state := "stopped"
			switch {
			case d.Error != "":
				state = "NOT stopped: " + d.Error
			case !d.WasRunning:
				state = "was already stopped"
			}
			fmt.Printf("  %-10s %-30s %s\n", d.Kind, d.Name, state)
		}
	}
	if !rec.Complete() {
		return errors.New("the drain is incomplete: some resources still run (undrain restarts the stopped ones)")
	}
	return nil
}

func cmdUndrain(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("undrain", flag.ContinueOnError)
	recordPath := fs.String("record", "", "")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *recordPath == "" {
		return errors.New("undrain needs --record FILE, written by `coolify-mirror drain --output FILE`")
	}
	rec, err := readDrainRecord(*recordPath)
	if err != nil {
		return err
	}
	in, err := detect(ctx)
	if err != nil {
		return err
	}
	pr := engine.NewProgress("Undrain")
	stop := func() {}
	if !*asJSON {
		stop = printProgress(pr)
	}
	results, err := engine.Undrain(ctx, in, rec, pr)
	stop()
	if err != nil {
		return err
	}
	type item struct {
		UUID    string `json:"uuid"`
		Name    string `json:"name"`
		Running bool   `json:"running"`
		Message string `json:"message,omitempty"`
	}
	out := struct {
		Format    string `json:"format"`
		Resources []item `json:"resources"`
	}{Format: "kombify.io/coolify-relocation-undrain/v1", Resources: []item{}}
	failed := 0
	for _, r := range results {
		out.Resources = append(out.Resources, item{UUID: r.Resource.UUID, Name: r.Resource.Name, Running: r.OK, Message: r.Message})
		if !r.OK {
			failed++
		}
	}
	if *asJSON {
		if err := writeJSON("", out); err != nil {
			return err
		}
	} else {
		for _, r := range out.Resources {
			state := "running"
			if !r.Running {
				state = "NOT running: " + r.Message
			}
			fmt.Printf("  %-30s %s\n", r.Name, state)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d resource(s) did not start again", failed)
	}
	return nil
}

func readPlan(path string) (*engine.RelocationPlan, error) {
	var p engine.RelocationPlan
	if err := readJSONFile(path, &p); err != nil {
		return nil, fmt.Errorf("cannot read the plan: %w", err)
	}
	return &p, nil
}

func readDrainRecord(path string) (*engine.DrainRecord, error) {
	var rec engine.DrainRecord
	if err := readJSONFile(path, &rec); err != nil {
		return nil, fmt.Errorf("cannot read the drain record: %w", err)
	}
	if rec.Format != engine.DrainFormat {
		return nil, errors.New("not a coolify relocation drain record (format " + engine.DrainFormat + ")")
	}
	return &rec, nil
}

func readJSONFile(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(io.LimitReader(f, 64<<20)).Decode(v)
}
