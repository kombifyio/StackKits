package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
	"github.com/mtalavi/coolify-mirror/internal/dbx"
)

// Preflight problems block a restore; they are checked when the restore is
// planned and again right before it writes anything, so no flag skips them.

// versionBlocker requires the exact same Coolify version on both servers:
// the database schema and the encrypted columns differ between releases.
func versionBlocker(in *coolify.Instance, man *Manifest) string {
	src := man.Source.CoolifyVersion
	switch {
	case src == "":
		return "the backup does not say which Coolify version it comes from"
	case in.Version == "":
		return "the Coolify version of this server could not be read"
	case CompareVersions(in.Version, src) < 0:
		return fmt.Sprintf("this server runs Coolify %s but the backup comes from Coolify %s - both must run the same version. Upgrade this server to %s (curl -fsSL https://cdn.coollabs.io/coolify/install.sh | sudo bash -s %s), then run the restore again",
			in.Version, src, src, src)
	case CompareVersions(in.Version, src) > 0:
		return fmt.Sprintf("this server runs Coolify %s but the backup comes from the older Coolify %s - both must run the same version. Upgrade the source server to %s (Coolify > Settings > Update, or curl -fsSL https://cdn.coollabs.io/coolify/install.sh | sudo bash -s %s), make a new backup there and restore that",
			in.Version, src, in.Version, in.Version)
	}
	return ""
}

// targetContent counts what a full restore would wipe out. Only a fresh
// Coolify (no projects, resources, extra servers or S3 storages) may be
// replaced; anything else must use a selective (merge) restore.
func targetContent(ctx context.Context, in *coolify.Instance) ([]string, error) {
	var row []struct {
		Projects  int `json:"projects"`
		Resources int `json:"resources"`
		Servers   int `json:"servers"`
		S3        int `json:"s3"`
	}
	q := `SELECT (SELECT count(*) FROM projects) AS projects,
  (SELECT count(*) FROM applications WHERE deleted_at IS NULL)
  + (SELECT count(*) FROM services WHERE deleted_at IS NULL)`
	for _, k := range in.Kinds(ctx) {
		q += "\n  + (SELECT count(*) FROM " + k.Table + " WHERE deleted_at IS NULL)"
	}
	q += ` AS resources,
  (SELECT count(*) FROM servers WHERE id <> 0) AS servers,
  (SELECT count(*) FROM s3_storages) AS s3`
	if err := in.Query(ctx, q, &row); err != nil || len(row) == 0 {
		return nil, fmt.Errorf("could not check whether this Coolify is empty: %w", err)
	}
	r := row[0]
	var out []string
	add := func(n int, what string) {
		if n > 0 {
			out = append(out, fmt.Sprintf("%d %s", n, what))
		}
	}
	add(r.Projects, "project(s)")
	add(r.Resources, "resource(s)")
	add(r.Servers, "server(s)")
	add(r.S3, "S3 storage(s)")
	return out, nil
}

// checkBundle validates the backup's official Server Transfer bundle with this
// Coolify's own validator and checks that it describes the same resources as
// the backup. It returns blockers and Coolify's warnings (webhook URLs to
// update, credentials to refresh, …).
func checkBundle(ctx context.Context, in *coolify.Instance, f *Fetched) (blockers, warnings []string) {
	if f.Bundle == nil {
		if f.Manifest.HasTransferBundle {
			return []string{"the backup lost its Coolify transfer bundle"}, nil
		}
		return nil, nil // made by a Coolify without Server Transfer support
	}
	res, err := in.ValidateBundle(ctx, f.Bundle)
	if err != nil {
		return []string{"Coolify could not check the transfer bundle: " + err.Error()}, nil
	}
	if !res.Supported {
		return nil, nil
	}
	if !res.Valid {
		return []string{"Coolify rejects the backup's transfer bundle: " + strings.Join(res.Errors, "; ")}, res.Warnings
	}
	want := map[string]bool{}
	for _, r := range f.Manifest.Resources {
		if r.Local() {
			want[r.UUID] = true
		}
	}
	got := map[string]bool{}
	for _, u := range res.UUIDs {
		got[u] = true
	}
	for u := range want {
		if !got[u] {
			return []string{"the transfer bundle does not match the backup (resource " + u + " is missing) - the file may have been altered"}, res.Warnings
		}
	}
	return nil, res.Warnings
}

// PreflightFull returns the problems that forbid a full restore here.
func PreflightFull(ctx context.Context, in *coolify.Instance, f *Fetched) []string {
	var out []string
	if b := versionBlocker(in, f.Manifest); b != "" {
		out = append(out, b)
	}
	out = append(out, CheckCompat(ctx, in).RestoreBlockers()...)
	if len(out) == 0 {
		b, _ := checkBundle(ctx, in, f)
		out = append(out, b...)
	}
	content, err := targetContent(ctx, in)
	switch {
	case err != nil:
		out = append(out, err.Error())
	case len(content) > 0:
		out = append(out, "a full restore only goes onto a fresh, empty Coolify - this one already has "+strings.Join(content, ", ")+
			". To bring resources into an existing Coolify, make a selective backup on the source and restore that (it merges)")
	}
	if s := SpaceWarning(in.DockerRoot, f.Manifest); s != "" {
		out = append(out, "not enough disk space: "+s)
	}
	return append(out, hostDepBlockers(ctx, f.Manifest)...)
}

// hostRequirementBlockers protects selective restores from silently importing a
// resource whose custom deployment depends on source-host policy that is absent
// or different on the destination.
func hostRequirementBlockers(man *Manifest) []string {
	var out []string
	for _, req := range man.HostRequirements {
		got, err := fileSHA256(req.Path)
		if err != nil {
			out = append(out, fmt.Sprintf("required host file %s is missing or unreadable (%s) - provision the destination host policy before restoring; selective restore will not overwrite global server policy", req.Path, req.Reason))
			continue
		}
		if req.SHA256 != "" && !strings.EqualFold(got, req.SHA256) {
			out = append(out, fmt.Sprintf("required host file %s has SHA-256 %s but the backup requires %s (%s) - reconcile the destination host policy before restoring", req.Path, got, req.SHA256, req.Reason))
		}
	}
	return out
}

// PreflightSelective returns the problems that forbid a selective restore here.
func PreflightSelective(ctx context.Context, in *coolify.Instance, f *Fetched) []string {
	var out []string
	if b := versionBlocker(in, f.Manifest); b != "" {
		out = append(out, b)
	}
	out = append(out, CheckCompat(ctx, in).RestoreBlockers()...)
	if s := SpaceWarning(in.DockerRoot, f.Manifest); s != "" {
		out = append(out, "not enough disk space: "+s)
	}
	out = append(out, hostRequirementBlockers(f.Manifest)...)
	out = append(out, hostDepConflicts(f.Manifest)...)
	if len(out) == 0 {
		b, _ := checkBundle(ctx, in, f)
		out = append(out, b...)
	}
	return append(out, hostDepBlockers(ctx, f.Manifest)...)
}

// BundleWarnings returns Coolify's own warnings for the backup's transfer
// bundle (what to update after the move).
func BundleWarnings(ctx context.Context, in *coolify.Instance, f *Fetched) []string {
	_, w := checkBundle(ctx, in, f)
	if f.Export != nil {
		w = relevantWarnings(w, f.Export)
	}
	w = append(w, CheckCompat(ctx, in).Warnings...)
	if f.Export != nil {
		for _, d := range f.Export.Drift {
			w = append(w, "from the source: "+d)
		}
	}
	return w
}

// relevantWarnings drops Coolify's GitHub App advice when a selective backup
// carries no GitHub App: Coolify's built-in "Public GitHub" source has no
// credentials or webhooks to update.
func relevantWarnings(w []string, ex *dbx.Export) []string {
	if len(ex.Tables["github_apps"]) > 0 {
		return w
	}
	var out []string
	for _, s := range w {
		if !strings.Contains(s, "GitHub App") {
			out = append(out, s)
		}
	}
	return out
}

func blockersErr(b []string) error {
	if len(b) == 0 {
		return nil
	}
	return errors.New("restore not allowed: " + strings.Join(b, "; "))
}
