package snapshot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewKopia_Defaults(t *testing.T) {
	k := NewKopia()
	require.NotNil(t, k)
	assert.Equal(t, "kopia", k.binary)
	assert.Equal(t, 10*time.Minute, k.timeout)
}

func TestNewKopia_OptionsApplied(t *testing.T) {
	k := NewKopia(
		WithBinary("/usr/local/bin/kopia"),
		WithTimeout(2*time.Minute),
	)
	assert.Equal(t, "/usr/local/bin/kopia", k.binary)
	assert.Equal(t, 2*time.Minute, k.timeout)
}

func TestParseStatus_NotConnectedTranslatesToConfiguredFalse(t *testing.T) {
	out := []byte("ERROR: not connected to a repository\n")
	sr, err := parseStatus(out, errors.New("exit status 1"))
	require.NoError(t, err, "fresh-install signal should not bubble up as error")
	assert.False(t, sr.Configured)
}

func TestParseStatus_OtherExitFailureSurfaces(t *testing.T) {
	out := []byte("ERROR: kopia binary went sideways\n")
	_, err := parseStatus(out, errors.New("exit status 2"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kopia repository status")
}

func TestParseStatus_HappyJSON(t *testing.T) {
	out := []byte(`{"configFile":"/var/lib/kopia/repository.config","storage":"filesystem"}`)
	sr, err := parseStatus(out, nil)
	require.NoError(t, err)
	assert.True(t, sr.Configured, "non-empty configFile implies configured")
	assert.Equal(t, "/var/lib/kopia/repository.config", sr.ConfigFile)
	assert.Equal(t, "filesystem", sr.Storage)
}

func TestParseStatus_BadJSONSurfaces(t *testing.T) {
	out := []byte(`{this isn't valid json`)
	_, err := parseStatus(out, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse json")
}

func TestStatus_BinaryMissing_ReturnsError(t *testing.T) {
	// Force a non-existent binary so exec fails fast.
	k := NewKopia(WithBinary("/this/path/should/not/exist/kopia"), WithTimeout(2*time.Second))
	_, err := k.Status(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kopia repository status")
}

func TestSnapshotRestore_RequiresArgs(t *testing.T) {
	k := NewKopia()
	err := k.SnapshotRestore(context.Background(), "", "/tmp/restore")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")

	err = k.SnapshotRestore(context.Background(), "abc", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestErrRepoNotConfigured_IsExported(t *testing.T) {
	// Sanity: callers will errors.Is on this value, so it must be a
	// stable package-level sentinel.
	require.NotNil(t, ErrRepoNotConfigured)
	assert.True(t, errors.Is(ErrRepoNotConfigured, ErrRepoNotConfigured))
}
