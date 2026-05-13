package commands

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBackupCmd_RegisteredOnRoot is the wiring smoke test: the
// `backup` subcommand must show up under `stackkit` so that
// `stackkit backup ...` resolves at runtime. Regression-prone because
// commands self-register via init() — a bad merge that drops the
// AddCommand call would silently remove the entire feature.
func TestBackupCmd_RegisteredOnRoot(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"backup"})
	require.NoError(t, err, "rootCmd.Find should locate the 'backup' subcommand")
	assert.Equal(t, "backup", cmd.Name())
}

// TestBackupCmd_HasAllSubcommands enumerates the contract documented
// in docs/CLI.md. If a subcommand is renamed or removed, this fails
// and forces the doc to be updated in the same PR — exactly the
// coupling we want.
func TestBackupCmd_HasAllSubcommands(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"backup"})
	require.NoError(t, err)

	want := map[string]bool{
		"init":                false,
		"run":                 false,
		"list":                false,
		"restore":             false,
		"verify":              false,
		"migrate-from-restic": false,
		"enroll":              false,
	}
	for _, sub := range cmd.Commands() {
		// Use Name(), not Use, because Use can include argument
		// placeholders ("restore <snapshot-id>").
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}
	for name, present := range want {
		assert.True(t, present, "missing subcommand: backup %s", name)
	}
}

// TestBackupCmd_EnrollRequiresToken verifies the flag-validation
// contract before we even dispatch to runBackupEnroll. cobra's
// MarkFlagRequired surfaces the error during parsing, which means the
// "controller not yet available" error path never runs without a
// token — that distinction matters for users running CI scripts
// against the scaffold.
func TestBackupCmd_EnrollRequiresToken(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"backup", "enroll"})
	require.NoError(t, err)

	flag := cmd.Flag("token")
	require.NotNil(t, flag, "--token flag must be defined on backup enroll")

	// cobra stores required flags in the annotations map under
	// "cobra_annotation_bash_completion_one_required_flag" with the
	// value "true". Checking the annotation rather than running the
	// command keeps the test free of side effects.
	assert.Contains(t, flag.Annotations, "cobra_annotation_bash_completion_one_required_flag",
		"--token must be marked required so users see the error before the stub runs")
}

// TestBackupCmd_RestoreRequiresSnapshotArg locks the positional-arg
// contract. If a future refactor accidentally dropped the
// cobra.ExactArgs(1) constraint, this test catches it before users
// hit a confusing nil-deref or empty-string restore.
func TestBackupCmd_RestoreRequiresSnapshotArg(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"backup", "restore"})
	require.NoError(t, err)
	require.NotNil(t, cmd.Args, "restore must enforce a positional snapshot-id argument")

	// Args is a function; calling it with an empty arg slice must
	// reject. We don't care about the exact error message, only that
	// the constraint exists.
	err = cmd.Args(cmd, []string{})
	assert.Error(t, err, "restore with zero args must be rejected")
}

// TestHumanSize covers the byte-formatting helper used by `backup
// list` table output. Tiny but the helper has bit-shift math worth
// pinning down so a refactor doesn't quietly start reporting "5.0 GiB"
// for half a megabyte.
func TestHumanSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{1024 * 1024 * 1024, "1.0 GiB"},
		{1024 * 1024 * 1024 * 1024, "1.0 TiB"},
	}
	for _, c := range cases {
		got := humanSize(c.in)
		assert.Equal(t, c.want, got, "humanSize(%d)", c.in)
	}
}

// TestTruncateBackup is the CLI-side counterpart of the helper used to
// keep secrets out of stub output. Same shape as the agent binary's
// helper, defined separately because the package boundary makes
// sharing inconvenient.
func TestTruncateBackup(t *testing.T) {
	assert.Equal(t, "abc", truncate("abc", 5))
	assert.Equal(t, "abc", truncate("abc", 3))
	assert.Equal(t, "abcde", truncate("abcdefg", 5))
	// Token-truncation must always be strictly shorter than the
	// input so a logged truncation can never be misread as the full
	// token. The CLI's truncate (no ellipsis suffix) achieves this by
	// returning the prefix only.
	long := strings.Repeat("X", 100)
	assert.True(t, len(truncate(long, 12)) < len(long))
}
