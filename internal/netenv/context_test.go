package netenv

import (
	"testing"

	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// ResolveNodeContext
// ---------------------------------------------------------------------------

func TestResolveNodeContext(t *testing.T) {
	t.Run("home network x86 returns local", func(t *testing.T) {
		ctx := ResolveNodeContext(ContextInput{
			NetworkEnv: models.NetEnvHome,
			Arch:       "amd64",
			CPUCores:   8,
			MemoryGB:   16,
		})
		assert.Equal(t, models.ContextLocal, ctx)
	})

	t.Run("VPS returns cloud", func(t *testing.T) {
		ctx := ResolveNodeContext(ContextInput{
			NetworkEnv: models.NetEnvVPS,
			Arch:       "amd64",
			CPUCores:   4,
			MemoryGB:   8,
		})
		assert.Equal(t, models.ContextCloud, ctx)
	})

	t.Run("kombify Cloud returns cloud", func(t *testing.T) {
		ctx := ResolveNodeContext(ContextInput{
			NetworkEnv: models.NetEnvCloud,
			Arch:       "amd64",
			CPUCores:   4,
			MemoryGB:   8,
		})
		assert.Equal(t, models.ContextCloud, ctx)
	})

	t.Run("ARM64 low resources returns pi", func(t *testing.T) {
		ctx := ResolveNodeContext(ContextInput{
			NetworkEnv: models.NetEnvHome,
			Arch:       "arm64",
			CPUCores:   2,
			MemoryGB:   2,
		})
		assert.Equal(t, models.ContextPi, ctx)
	})

	t.Run("ARM64 low CPU returns pi", func(t *testing.T) {
		ctx := ResolveNodeContext(ContextInput{
			NetworkEnv: models.NetEnvHome,
			Arch:       "arm64",
			CPUCores:   2,
			MemoryGB:   8,
		})
		assert.Equal(t, models.ContextPi, ctx)
	})

	t.Run("ARM64 low memory returns pi", func(t *testing.T) {
		ctx := ResolveNodeContext(ContextInput{
			NetworkEnv: models.NetEnvHome,
			Arch:       "arm64",
			CPUCores:   4,
			MemoryGB:   2,
		})
		assert.Equal(t, models.ContextPi, ctx)
	})

	t.Run("ARM64 adequate resources on home returns local", func(t *testing.T) {
		ctx := ResolveNodeContext(ContextInput{
			NetworkEnv: models.NetEnvHome,
			Arch:       "arm64",
			CPUCores:   8,
			MemoryGB:   16,
		})
		assert.Equal(t, models.ContextLocal, ctx)
	})

	t.Run("ARM64 adequate resources on VPS returns cloud", func(t *testing.T) {
		ctx := ResolveNodeContext(ContextInput{
			NetworkEnv: models.NetEnvVPS,
			Arch:       "arm64",
			CPUCores:   8,
			MemoryGB:   16,
		})
		assert.Equal(t, models.ContextCloud, ctx)
	})

	t.Run("aarch64 arch treated as ARM", func(t *testing.T) {
		ctx := ResolveNodeContext(ContextInput{
			NetworkEnv: models.NetEnvHome,
			Arch:       "aarch64",
			CPUCores:   2,
			MemoryGB:   2,
		})
		assert.Equal(t, models.ContextPi, ctx)
	})

	t.Run("unknown network defaults to local", func(t *testing.T) {
		ctx := ResolveNodeContext(ContextInput{
			NetworkEnv: models.NetEnvUnknown,
			Arch:       "amd64",
			CPUCores:   4,
			MemoryGB:   8,
		})
		assert.Equal(t, models.ContextLocal, ctx)
	})

	t.Run("empty network defaults to local", func(t *testing.T) {
		ctx := ResolveNodeContext(ContextInput{
			NetworkEnv: "",
			Arch:       "amd64",
		})
		assert.Equal(t, models.ContextLocal, ctx)
	})

	t.Run("zero hardware info uses network env only", func(t *testing.T) {
		ctx := ResolveNodeContext(ContextInput{
			NetworkEnv: models.NetEnvVPS,
			Arch:       "amd64",
			CPUCores:   0,
			MemoryGB:   0,
		})
		assert.Equal(t, models.ContextCloud, ctx)
	})

	t.Run("ARM64 zero hardware info uses network env", func(t *testing.T) {
		// ARM without hardware info — can't determine if low-resource, fall through to netenv
		ctx := ResolveNodeContext(ContextInput{
			NetworkEnv: models.NetEnvHome,
			Arch:       "arm64",
			CPUCores:   0,
			MemoryGB:   0,
		})
		assert.Equal(t, models.ContextLocal, ctx)
	})
}

// ---------------------------------------------------------------------------
// ResolveFromResult
// ---------------------------------------------------------------------------

func TestResolveFromResult(t *testing.T) {
	t.Run("VPS result with hardware returns cloud", func(t *testing.T) {
		result := &Result{Environment: models.NetEnvVPS}
		ctx := ResolveFromResult(result, 4, 8)
		assert.Equal(t, models.ContextCloud, ctx)
	})

	t.Run("home result returns local", func(t *testing.T) {
		result := &Result{Environment: models.NetEnvHome}
		ctx := ResolveFromResult(result, 8, 16)
		assert.Equal(t, models.ContextLocal, ctx)
	})
}

// ---------------------------------------------------------------------------
// FormatNodeContext
// ---------------------------------------------------------------------------

func TestFormatNodeContext(t *testing.T) {
	tests := []struct {
		ctx      models.NodeContext
		expected string
	}{
		{models.ContextLocal, "local (home/office server)"},
		{models.ContextCloud, "cloud (VPS/dedicated/managed)"},
		{models.ContextPi, "pi (ARM64 low-resource device)"},
		{"invalid", "unknown"},
	}

	for _, tt := range tests {
		t.Run(string(tt.ctx), func(t *testing.T) {
			assert.Equal(t, tt.expected, FormatNodeContext(tt.ctx))
		})
	}
}

// ---------------------------------------------------------------------------
// NodeContextIsCloud
// ---------------------------------------------------------------------------

func TestNodeContextIsCloud(t *testing.T) {
	assert.True(t, NodeContextIsCloud(models.ContextCloud))
	assert.False(t, NodeContextIsCloud(models.ContextLocal))
	assert.False(t, NodeContextIsCloud(models.ContextPi))
}

// ---------------------------------------------------------------------------
// SuggestDomainForContext
// ---------------------------------------------------------------------------

func TestSuggestDomainForContext(t *testing.T) {
	t.Run("cloud with local domain corrects to kombify.me", func(t *testing.T) {
		localDomains := []string{
			"stack.local", "home.lab", "my.lan", "my.home",
			"lab.homebase", "homelab", "",
		}
		for _, d := range localDomains {
			domain, reason := SuggestDomainForContext(models.ContextCloud, d)
			assert.Equal(t, models.DomainKombifyMe, domain, "domain=%q should be corrected", d)
			assert.NotEmpty(t, reason, "domain=%q should have a reason", d)
		}
	})

	t.Run("cloud with real domain keeps it", func(t *testing.T) {
		domain, reason := SuggestDomainForContext(models.ContextCloud, "mylab.example.com")
		assert.Equal(t, "mylab.example.com", domain)
		assert.Empty(t, reason)
	})

	t.Run("local with empty domain defaults to home.lab", func(t *testing.T) {
		domain, reason := SuggestDomainForContext(models.ContextLocal, "")
		assert.Equal(t, models.DomainHomeLab, domain)
		assert.Contains(t, reason, "local")
	})

	t.Run("local with existing domain keeps it", func(t *testing.T) {
		domain, reason := SuggestDomainForContext(models.ContextLocal, "stack.local")
		assert.Equal(t, "stack.local", domain)
		assert.Empty(t, reason)
	})

	t.Run("pi with empty domain defaults to home.lab", func(t *testing.T) {
		domain, reason := SuggestDomainForContext(models.ContextPi, "")
		assert.Equal(t, models.DomainHomeLab, domain)
		assert.Contains(t, reason, "local")
	})

	t.Run("pi with existing domain keeps it", func(t *testing.T) {
		domain, reason := SuggestDomainForContext(models.ContextPi, "mypi.local")
		assert.Equal(t, "mypi.local", domain)
		assert.Empty(t, reason)
	})
}

// ---------------------------------------------------------------------------
// isLocalDomain (private helper)
// ---------------------------------------------------------------------------

func TestIsLocalDomain(t *testing.T) {
	localDomains := []string{
		"", "homelab", "stack.local", "my.lab", "test.lan", "app.home", "lab.homebase",
	}
	for _, d := range localDomains {
		assert.True(t, isLocalDomain(d), "expected %q to be local domain", d)
	}

	nonLocalDomains := []string{
		"example.com", "my.domain.io", "kombify.me",
	}
	for _, d := range nonLocalDomains {
		assert.False(t, isLocalDomain(d), "expected %q to NOT be local domain", d)
	}
}
