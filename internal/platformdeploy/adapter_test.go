package platformdeploy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDokployAdapterApplyComposeCreatesThenDeploys(t *testing.T) {
	var calls []recordedCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, recordCall(t, r))
		switch r.URL.Path {
		case "/api/compose.create":
			writeJSON(t, w, map[string]string{"id": "dokploy-compose-1"})
		case "/api/compose.deploy":
			writeJSON(t, w, map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewDokployAdapter(HTTPConfig{
		BaseURL: server.URL,
		Token:   "token-1",
		Client:  server.Client(),
	})

	ref, err := adapter.ApplyCompose(context.Background(), AppManifest{
		Name:        "web",
		Platform:    models.PAASDokploy,
		ManagedBy:   models.PAASDokploy,
		ComposeYAML: "services:\n  web:\n    image: nginx:alpine\n",
	})

	require.NoError(t, err)
	assert.Equal(t, models.PAASDokploy, ref.Platform)
	assert.Equal(t, "dokploy-compose-1", ref.ExternalID)
	require.Len(t, calls, 2)
	assert.Equal(t, "/api/compose.create", calls[0].Path)
	assert.Equal(t, "/api/compose.deploy", calls[1].Path)
	assert.Equal(t, "token-1", calls[0].APIKey)
	assert.Equal(t, "web", calls[0].JSON["name"])
	assert.Equal(t, "services:\n  web:\n    image: nginx:alpine\n", calls[0].JSON["composeFile"])
	assert.Equal(t, "dokploy-compose-1", calls[1].JSON["composeId"])
}

func TestDokployAdapterApplyComposeUpdatesOnCreateConflict(t *testing.T) {
	var calls []recordedCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, recordCall(t, r))
		switch r.URL.Path {
		case "/api/compose.create":
			http.Error(w, `{"message":"already exists","id":"dokploy-compose-existing"}`, http.StatusConflict)
		case "/api/compose.update":
			writeJSON(t, w, map[string]string{"id": "dokploy-compose-existing"})
		case "/api/compose.deploy":
			writeJSON(t, w, map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewDokployAdapter(HTTPConfig{BaseURL: server.URL, Client: server.Client()})

	ref, err := adapter.ApplyCompose(context.Background(), AppManifest{
		Name:        "web",
		Platform:    models.PAASDokploy,
		ManagedBy:   models.PAASDokploy,
		ComposeYAML: "services: {}\n",
	})

	require.NoError(t, err)
	assert.Equal(t, "dokploy-compose-existing", ref.ExternalID)
	require.Len(t, calls, 3)
	assert.Equal(t, "/api/compose.create", calls[0].Path)
	assert.Equal(t, "/api/compose.update", calls[1].Path)
	assert.Equal(t, "/api/compose.deploy", calls[2].Path)
	assert.Equal(t, "web", calls[1].JSON["name"])
}

func TestCoolifyAdapterApplyComposeCreatesThenDeploys(t *testing.T) {
	var calls []recordedCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, recordCall(t, r))
		switch r.URL.Path {
		case "/api/v1/applications/dockercompose":
			writeJSON(t, w, map[string]string{"uuid": "coolify-app-1"})
		case "/api/v1/deploy":
			assert.Equal(t, "coolify-app-1", r.URL.Query().Get("uuid"))
			writeJSON(t, w, map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewCoolifyAdapter(HTTPConfig{
		BaseURL: server.URL,
		Token:   "token-2",
		Client:  server.Client(),
	})

	ref, err := adapter.ApplyCompose(context.Background(), AppManifest{
		Name:        "web",
		Platform:    models.PAASCoolify,
		ManagedBy:   models.PAASCoolify,
		ComposeYAML: "services:\n  web:\n    image: nginx:alpine\n",
	})

	require.NoError(t, err)
	assert.Equal(t, models.PAASCoolify, ref.Platform)
	assert.Equal(t, "coolify-app-1", ref.ExternalID)
	require.Len(t, calls, 2)
	assert.Equal(t, "/api/v1/applications/dockercompose", calls[0].Path)
	assert.Equal(t, "/api/v1/deploy", calls[1].Path)
	assert.Equal(t, "Bearer token-2", calls[0].Authorization)
	assert.Equal(t, "web", calls[0].JSON["name"])
	assert.Equal(t, "services:\n  web:\n    image: nginx:alpine\n", calls[0].JSON["docker_compose_raw"])
}

func TestCoolifyAdapterApplyComposeUpdatesOnCreateConflict(t *testing.T) {
	var calls []recordedCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, recordCall(t, r))
		switch r.URL.Path {
		case "/api/v1/applications/dockercompose":
			http.Error(w, `{"message":"already exists","uuid":"coolify-app-existing"}`, http.StatusConflict)
		case "/api/v1/applications/coolify-app-existing":
			require.Equal(t, http.MethodPatch, r.Method)
			writeJSON(t, w, map[string]string{"uuid": "coolify-app-existing"})
		case "/api/v1/deploy":
			assert.Equal(t, "coolify-app-existing", r.URL.Query().Get("uuid"))
			writeJSON(t, w, map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewCoolifyAdapter(HTTPConfig{
		BaseURL: server.URL,
		Token:   "token-2",
		Client:  server.Client(),
	})

	ref, err := adapter.ApplyCompose(context.Background(), AppManifest{
		Name:        "web",
		Platform:    models.PAASCoolify,
		ManagedBy:   models.PAASCoolify,
		ComposeYAML: "services:\n  web:\n    image: nginx:alpine\n",
	})

	require.NoError(t, err)
	assert.Equal(t, "coolify-app-existing", ref.ExternalID)
	require.Len(t, calls, 3)
	assert.Equal(t, "/api/v1/applications/dockercompose", calls[0].Path)
	assert.Equal(t, "/api/v1/applications/coolify-app-existing", calls[1].Path)
	assert.Equal(t, "/api/v1/deploy", calls[2].Path)
	assert.Equal(t, "Bearer token-2", calls[1].Authorization)
	assert.Equal(t, "web", calls[1].JSON["name"])
}

func TestLocalComposeAdapterRunsDockerComposeUp(t *testing.T) {
	var gotDir string
	var gotArgs []string
	adapter := NewLocalComposeAdapter("/opt/stackkit/deploy", WithLocalComposeRunner(func(_ context.Context, dir string, args ...string) ([]byte, error) {
		gotDir = dir
		gotArgs = append([]string(nil), args...)
		return []byte("started"), nil
	}))

	ref, err := adapter.ApplyCompose(context.Background(), AppManifest{
		Name:        "whoami",
		Platform:    models.PAASNone,
		ManagedBy:   models.PAASNone,
		ComposePath: "./.whoami-compose.yaml",
	})

	require.NoError(t, err)
	assert.Equal(t, "/opt/stackkit/deploy", gotDir)
	assert.Equal(t, []string{"compose", "-p", "stackkit-whoami", "-f", "./.whoami-compose.yaml", "up", "-d"}, gotArgs)
	assert.Equal(t, models.PAASNone, ref.Platform)
	assert.Equal(t, "whoami", ref.AppName)
	assert.Equal(t, "local-compose:whoami", ref.ExternalID)
}

func TestGenerateKomodoStackResource(t *testing.T) {
	resource, err := GenerateKomodoStackResource(AppManifest{
		Name:        "web",
		Platform:    "komodo",
		ManagedBy:   "komodo",
		ComposeYAML: "services:\n  web:\n    image: nginx:alpine\n",
	})

	require.NoError(t, err)
	assert.Contains(t, string(resource), "type: Stack")
	assert.Contains(t, string(resource), "name: web")
	assert.Contains(t, string(resource), "image: nginx:alpine")
}

type recordedCall struct {
	Path          string
	Authorization string
	APIKey        string
	JSON          map[string]any
}

func recordCall(t *testing.T, r *http.Request) recordedCall {
	t.Helper()
	var payload map[string]any
	if r.Body != nil {
		err := json.NewDecoder(r.Body).Decode(&payload)
		if err != nil && !errors.Is(err, io.EOF) {
			require.NoError(t, err)
		}
	}
	return recordedCall{
		Path:          r.URL.Path,
		Authorization: r.Header.Get("Authorization"),
		APIKey:        r.Header.Get("X-Api-Key"),
		JSON:          payload,
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(value))
}
