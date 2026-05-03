// Package template tests
package template

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderer(t *testing.T) {
	t.Run("creates renderer", func(t *testing.T) {
		renderer := NewRenderer("/templates", "/output")
		assert.NotNil(t, renderer)
	})
}

func TestRenderContext(t *testing.T) {
	t.Run("creates render context", func(t *testing.T) {
		ctx := &RenderContext{
			Spec: &models.StackSpec{
				Name:     "test",
				StackKit: "base-kit",
			},
			StackKit: &models.StackKit{
				Metadata: models.StackKitMetadata{
					Name:    "base-kit",
					Version: "1.0.0",
				},
			},
			Variables: map[string]interface{}{
				"domain": "example.com",
			},
		}

		assert.NotNil(t, ctx.Spec)
		assert.NotNil(t, ctx.StackKit)
		assert.NotNil(t, ctx.Variables)
	})
}

func TestServiceContext(t *testing.T) {
	t.Run("creates service context", func(t *testing.T) {
		svc := ServiceContext{
			Name:  "traefik",
			Image: "traefik:v3.0",
			Ports: []PortMapping{
				{Host: 80, Container: 80, Protocol: "tcp"},
				{Host: 443, Container: 443, Protocol: "tcp"},
			},
			Volumes: []VolumeMapping{
				{Source: "/var/run/docker.sock", Target: "/var/run/docker.sock", ReadOnly: true},
			},
			Environment: map[string]string{
				"TZ": "Europe/Berlin",
			},
			Labels: map[string]string{
				"managed-by": "stackkit",
			},
			Networks:  []string{"stackkit-network"},
			DependsOn: []string{},
			Enabled:   true,
		}

		assert.Equal(t, "traefik", svc.Name)
		assert.Len(t, svc.Ports, 2)
		assert.Len(t, svc.Volumes, 1)
		assert.True(t, svc.Enabled)
	})
}

func TestRenderSingle(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "template-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	templateDir := filepath.Join(tmpDir, "templates")
	outputDir := filepath.Join(tmpDir, "output")
	require.NoError(t, os.MkdirAll(templateDir, 0750))
	require.NoError(t, os.MkdirAll(outputDir, 0750))

	t.Run("renders simple template", func(t *testing.T) {
		tmplContent := `# Generated for {{.Spec.Name}}
domain = "{{.Spec.Domain}}"
`
		tmplPath := filepath.Join(templateDir, "test.tf.tmpl")
		err := os.WriteFile(tmplPath, []byte(tmplContent), 0600)
		require.NoError(t, err)

		renderer := NewRenderer(templateDir, outputDir)
		ctx := &RenderContext{
			Spec: &models.StackSpec{
				Name:   "my-homelab",
				Domain: "example.com",
			},
		}

		result, err := renderer.RenderSingle("test.tf.tmpl", ctx)

		require.NoError(t, err)
		assert.Contains(t, result, "my-homelab")
		assert.Contains(t, result, "example.com")
	})

	t.Run("returns error for missing template", func(t *testing.T) {
		renderer := NewRenderer(templateDir, outputDir)
		ctx := &RenderContext{}

		_, err := renderer.RenderSingle("nonexistent.tmpl", ctx)

		assert.Error(t, err)
	})

	t.Run("returns error for invalid template syntax", func(t *testing.T) {
		tmplContent := `{{.Invalid.Field.Missing}`
		tmplPath := filepath.Join(templateDir, "invalid.tmpl")
		err := os.WriteFile(tmplPath, []byte(tmplContent), 0600)
		require.NoError(t, err)

		renderer := NewRenderer(templateDir, outputDir)
		ctx := &RenderContext{}

		_, err = renderer.RenderSingle("invalid.tmpl", ctx)

		assert.Error(t, err)
	})
}

func TestRender(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "template-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	templateDir := filepath.Join(tmpDir, "templates")
	outputDir := filepath.Join(tmpDir, "output")
	require.NoError(t, os.MkdirAll(templateDir, 0750))

	t.Run("renders all templates", func(t *testing.T) {
		// Create template files
		tmpl1 := `# Main config for {{.Spec.Name}}`
		tmpl2 := `# Variables for {{.Spec.StackKit}}`

		require.NoError(t, os.WriteFile(filepath.Join(templateDir, "main.tf"), []byte(tmpl1), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(templateDir, "variables.tf.tmpl"), []byte(tmpl2), 0600))

		renderer := NewRenderer(templateDir, outputDir)
		ctx := &RenderContext{
			Spec: &models.StackSpec{
				Name:     "test",
				StackKit: "base-kit",
			},
		}

		err := renderer.Render(ctx)

		require.NoError(t, err)

		// Check output files exist
		_, err = os.Stat(filepath.Join(outputDir, "main.tf"))
		assert.NoError(t, err)

		_, err = os.Stat(filepath.Join(outputDir, "variables.tf"))
		assert.NoError(t, err)
	})
}

func TestTemplateFunctions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "template-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	templateDir := filepath.Join(tmpDir, "templates")
	outputDir := filepath.Join(tmpDir, "output")
	require.NoError(t, os.MkdirAll(templateDir, 0750))
	require.NoError(t, os.MkdirAll(outputDir, 0750))

	renderer := NewRenderer(templateDir, outputDir)

	testCases := []struct {
		name     string
		template string
		expected string
	}{
		{
			name:     "lower function",
			template: `{{lower "HELLO"}}`,
			expected: "hello",
		},
		{
			name:     "upper function",
			template: `{{upper "hello"}}`,
			expected: "HELLO",
		},
		{
			name:     "trim function",
			template: `{{trim "  hello  "}}`,
			expected: "hello",
		},
		{
			name:     "replace function",
			template: `{{replace "hello-world" "-" "_"}}`,
			expected: "hello_world",
		},
		{
			name:     "contains function",
			template: `{{if contains "hello world" "world"}}yes{{end}}`,
			expected: "yes",
		},
		{
			name:     "hasPrefix function",
			template: `{{if hasPrefix "hello" "hel"}}yes{{end}}`,
			expected: "yes",
		},
		{
			name:     "hasSuffix function",
			template: `{{if hasSuffix "hello" "lo"}}yes{{end}}`,
			expected: "yes",
		},
		{
			name:     "default function with empty",
			template: `{{default "default-value" ""}}`,
			expected: "default-value",
		},
		{
			name:     "default function with value",
			template: `{{default "default-value" "actual-value"}}`,
			expected: "actual-value",
		},
		{
			name:     "quote function",
			template: `{{quote "hello"}}`,
			expected: `"hello"`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmplPath := filepath.Join(templateDir, "test.tmpl")
			err := os.WriteFile(tmplPath, []byte(tc.template), 0600)
			require.NoError(t, err)

			result, err := renderer.RenderSingle("test.tmpl", &RenderContext{})

			require.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestEnvMap(t *testing.T) {
	t.Run("creates empty map", func(t *testing.T) {
		result := envMap(nil)
		assert.Equal(t, "{}", result)
	})

	t.Run("creates map with values", func(t *testing.T) {
		env := map[string]string{
			"TZ":   "Europe/Berlin",
			"NODE": "production",
		}
		result := envMap(env)

		assert.Contains(t, result, "TZ")
		assert.Contains(t, result, "Europe/Berlin")
	})
}

func TestLabelMap(t *testing.T) {
	t.Run("creates empty map", func(t *testing.T) {
		result := labelMap(nil)
		assert.Equal(t, "{}", result)
	})

	t.Run("creates map with values", func(t *testing.T) {
		labels := map[string]string{
			"managed-by": "stackkit",
		}
		result := labelMap(labels)

		assert.Contains(t, result, "managed-by")
		assert.Contains(t, result, "stackkit")
	})
}

func TestPortList(t *testing.T) {
	t.Run("creates empty list", func(t *testing.T) {
		result := portList(nil)
		assert.Equal(t, "", result)
	})

	t.Run("creates port blocks", func(t *testing.T) {
		ports := []PortMapping{
			{Host: 80, Container: 80, Protocol: "tcp"},
			{Host: 443, Container: 443, Protocol: "tcp"},
		}
		result := portList(ports)

		assert.Contains(t, result, "internal = 80")
		assert.Contains(t, result, "external = 80")
		assert.Contains(t, result, "internal = 443")
	})

	t.Run("defaults to tcp protocol", func(t *testing.T) {
		ports := []PortMapping{
			{Host: 53, Container: 53},
		}
		result := portList(ports)

		assert.Contains(t, result, `protocol = "tcp"`)
	})
}

func TestGenerateMainTf(t *testing.T) {
	t.Run("generates main.tf content", func(t *testing.T) {
		ctx := &RenderContext{
			Spec: &models.StackSpec{
				Name:     "test",
				StackKit: "base-kit",
				Network: models.NetworkSpec{
					Subnet: "172.20.0.0/16",
				},
			},
			StackKit: &models.StackKit{
				Metadata: models.StackKitMetadata{
					Name: "base-kit",
				},
			},
		}

		result, err := GenerateMainTf(ctx)
		require.NoError(t, err)

		assert.Contains(t, result, "Generated by stackkit")
		assert.Contains(t, result, "base-kit")
		assert.Contains(t, result, "required_providers")
		assert.Contains(t, result, "kreuzwerker/docker")
		assert.Contains(t, result, "172.20.0.0/16")
	})
}

func TestServiceFor(t *testing.T) {
	services := []ServiceContext{
		{Name: "traefik", Image: "traefik:v3.0"},
		{Name: "dokploy", Image: "dokploy/dokploy:latest"},
	}

	t.Run("finds existing service", func(t *testing.T) {
		svc := serviceFor("traefik", services)

		require.NotNil(t, svc)
		assert.Equal(t, "traefik:v3.0", svc.Image)
	})

	t.Run("returns nil for missing service", func(t *testing.T) {
		svc := serviceFor("nonexistent", services)

		assert.Nil(t, svc)
	})
}

func TestIfEnabled(t *testing.T) {
	t.Run("returns value when enabled", func(t *testing.T) {
		result := ifEnabled(true, "hello")
		assert.Equal(t, "hello", result)
	})

	t.Run("returns nil when disabled", func(t *testing.T) {
		result := ifEnabled(false, "hello")
		assert.Nil(t, result)
	})
}

func TestIndent(t *testing.T) {
	t.Run("indents single line", func(t *testing.T) {
		result := indent(4, "hello")
		assert.Equal(t, "    hello", result)
	})

	t.Run("indents multiple lines", func(t *testing.T) {
		result := indent(2, "line1\nline2\nline3")
		assert.Equal(t, "  line1\n  line2\n  line3", result)
	})

	t.Run("preserves empty lines", func(t *testing.T) {
		result := indent(2, "line1\n\nline3")
		assert.Equal(t, "  line1\n\n  line3", result)
	})

	t.Run("zero indent", func(t *testing.T) {
		result := indent(0, "hello")
		assert.Equal(t, "hello", result)
	})
}

func TestToYaml(t *testing.T) {
	t.Run("converts map to yaml", func(t *testing.T) {
		data := map[string]string{"key": "value"}
		result := toYaml(data)
		assert.Contains(t, result, "key: value")
	})

	t.Run("handles nil", func(t *testing.T) {
		result := toYaml(nil)
		assert.Equal(t, "", result)
	})

	t.Run("converts nested struct", func(t *testing.T) {
		data := map[string]interface{}{
			"network": map[string]string{
				"subnet": "10.0.0.0/8",
			},
		}
		result := toYaml(data)
		assert.Contains(t, result, "network")
		assert.Contains(t, result, "subnet")
	})
}

func TestToJson(t *testing.T) {
	t.Run("converts map to json", func(t *testing.T) {
		data := map[string]string{"key": "value"}
		result := toJSON(data)
		assert.Contains(t, result, `"key":"value"`)
	})

	t.Run("handles nil", func(t *testing.T) {
		result := toJSON(nil)
		assert.Equal(t, "null", result)
	})

	t.Run("converts number", func(t *testing.T) {
		result := toJSON(42)
		assert.Equal(t, "42", result)
	})
}

func TestToJsonPretty(t *testing.T) {
	t.Run("converts map to pretty json", func(t *testing.T) {
		data := map[string]string{"key": "value"}
		result := toJSONPretty(data)
		assert.Contains(t, result, "  ")
		assert.Contains(t, result, `"key": "value"`)
	})

	t.Run("handles nil", func(t *testing.T) {
		result := toJSONPretty(nil)
		assert.Equal(t, "null", result)
	})
}

func TestDefaultValue(t *testing.T) {
	t.Run("returns default for nil", func(t *testing.T) {
		result := defaultValue("fallback", nil)
		assert.Equal(t, "fallback", result)
	})

	t.Run("returns default for empty string", func(t *testing.T) {
		result := defaultValue("fallback", "")
		assert.Equal(t, "fallback", result)
	})

	t.Run("returns value when present", func(t *testing.T) {
		result := defaultValue("fallback", "actual")
		assert.Equal(t, "actual", result)
	})
}

func TestQuote(t *testing.T) {
	t.Run("wraps in double quotes", func(t *testing.T) {
		assert.Equal(t, `"hello"`, quote("hello"))
	})

	t.Run("handles empty string", func(t *testing.T) {
		assert.Equal(t, `""`, quote(""))
	})
}

func TestRenderWithNonexistentTemplateDir(t *testing.T) {
	renderer := NewRenderer("/nonexistent/templates", t.TempDir())
	ctx := &RenderContext{
		Spec: &models.StackSpec{Name: "test", StackKit: "base-kit"},
	}

	err := renderer.Render(ctx)
	assert.Error(t, err)
}

func TestPortListWithProtocol(t *testing.T) {
	t.Run("uses specified protocol", func(t *testing.T) {
		ports := []PortMapping{
			{Host: 53, Container: 53, Protocol: "udp"},
		}
		result := portList(ports)
		assert.Contains(t, result, `protocol = "udp"`)
	})

	t.Run("multiple ports with different protocols", func(t *testing.T) {
		ports := []PortMapping{
			{Host: 80, Container: 80, Protocol: "tcp"},
			{Host: 53, Container: 53, Protocol: "udp"},
		}
		result := portList(ports)
		assert.Contains(t, result, `protocol = "tcp"`)
		assert.Contains(t, result, `protocol = "udp"`)
	})
}
