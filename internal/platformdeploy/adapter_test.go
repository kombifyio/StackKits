package platformdeploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
		case "/api/compose.update":
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
	require.Len(t, calls, 3)
	assert.Equal(t, "/api/compose.create", calls[0].Path)
	assert.Equal(t, "/api/compose.update", calls[1].Path)
	assert.Equal(t, "/api/compose.deploy", calls[2].Path)
	assert.Equal(t, "token-1", calls[0].APIKey)
	assert.Equal(t, "web", calls[0].JSON["name"])
	assert.Equal(t, "services:\n  web:\n    image: nginx:alpine\n", calls[0].JSON["composeFile"])
	assert.Equal(t, "dokploy-compose-1", calls[1].JSON["composeId"])
	assert.Equal(t, "raw", calls[1].JSON["sourceType"])
	assert.Equal(t, "dokploy-compose-1", calls[2].JSON["composeId"])
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
	assert.Equal(t, "raw", calls[1].JSON["sourceType"])
}

func TestCoolifyAdapterApplyComposeCreatesThenDeploys(t *testing.T) {
	var calls []recordedCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, recordCall(t, r))
		switch r.URL.Path {
		case "/api/v1/services":
			require.Equal(t, http.MethodPost, r.Method)
			writeJSON(t, w, map[string]string{"uuid": "coolify-app-1"})
		case "/api/v1/services/coolify-app-1/start":
			require.Equal(t, http.MethodPost, r.Method)
			writeJSON(t, w, map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewCoolifyAdapter(HTTPConfig{
		BaseURL:         server.URL,
		Token:           "token-2",
		Client:          server.Client(),
		ProjectUUID:     "project-1",
		ServerID:        "server-1",
		EnvironmentUUID: "env-uuid-1",
		EnvironmentID:   "production",
		DestinationUUID: "destination-1",
	})

	ref, err := adapter.ApplyCompose(context.Background(), AppManifest{
		Name:        "web",
		Platform:    models.PAASCoolify,
		ManagedBy:   models.PAASCoolify,
		URL:         "https://web.example.test",
		ComposeYAML: "\n    services:\n      web:\n        image: nginx:alpine\n",
	})

	require.NoError(t, err)
	assert.Equal(t, models.PAASCoolify, ref.Platform)
	assert.Equal(t, "coolify-app-1", ref.ExternalID)
	require.Len(t, calls, 2)
	assert.Equal(t, "/api/v1/services", calls[0].Path)
	assert.Equal(t, "/api/v1/services/coolify-app-1/start", calls[1].Path)
	assert.Equal(t, "Bearer token-2", calls[0].Authorization)
	assert.NotContains(t, calls[0].JSON, "type")
	assert.Equal(t, "web", calls[0].JSON["name"])
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("services:\n  web:\n    image: nginx:alpine\n")), calls[0].JSON["docker_compose_raw"])
	assert.Equal(t, "project-1", calls[0].JSON["project_uuid"])
	assert.Equal(t, "server-1", calls[0].JSON["server_uuid"])
	assert.Equal(t, "env-uuid-1", calls[0].JSON["environment_uuid"])
	assert.Equal(t, "production", calls[0].JSON["environment_name"])
	assert.Equal(t, "destination-1", calls[0].JSON["destination_uuid"])
	assert.Equal(t, true, calls[0].JSON["is_container_label_escape_enabled"])
	urls, ok := calls[0].JSON["urls"].([]any)
	require.True(t, ok)
	require.Len(t, urls, 1)
	route, ok := urls[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "web", route["name"])
	assert.Equal(t, "https://web.example.test", route["url"])
}

func TestKomodoAdapterApplyComposeCreatesThenDeploys(t *testing.T) {
	var calls []recordedCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, recordCall(t, r))
		switch r.URL.Path {
		case "/write/CreateStack":
			require.Equal(t, http.MethodPost, r.Method)
			writeJSON(t, w, map[string]any{"_id": map[string]string{"$oid": "komodo-stack-1"}})
		case "/execute/DeployStack":
			require.Equal(t, http.MethodPost, r.Method)
			writeJSON(t, w, map[string]any{"_id": map[string]string{"$oid": "komodo-update-1"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewKomodoAdapter(HTTPConfig{
		BaseURL:  server.URL,
		APIKey:   "key-1",
		Secret:   "secret-1",
		Client:   server.Client(),
		ServerID: "server-local",
	})

	ref, err := adapter.ApplyCompose(context.Background(), AppManifest{
		Name:        "web",
		Platform:    models.PAASKomodo,
		ManagedBy:   models.PAASKomodo,
		URL:         "https://web.example.test",
		ComposeYAML: "\n    services:\n      web:\n        image: nginx:alpine\n",
	})

	require.NoError(t, err)
	assert.Equal(t, models.PAASKomodo, ref.Platform)
	assert.Equal(t, "komodo-stack-1", ref.ExternalID)
	assert.Equal(t, "komodo-update-1", ref.DeploymentID)
	require.Len(t, calls, 2)
	assert.Equal(t, "/write/CreateStack", calls[0].Path)
	assert.Equal(t, "key-1", calls[0].APIKey)
	assert.Equal(t, "secret-1", calls[0].APISecret)
	config, ok := calls[0].JSON["config"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "server-local", config["server_id"])
	assert.Equal(t, "web", config["project_name"])
	assert.Equal(t, false, config["auto_pull"])
	assert.Equal(t, "services:\n  web:\n    image: nginx:alpine\n", config["file_contents"])
	assert.Equal(t, "/execute/DeployStack", calls[1].Path)
	assert.Equal(t, "komodo-stack-1", calls[1].JSON["stack"])
}

func TestKomodoAdapterApplyComposeUpdatesOnCreateConflictByResolvedStackID(t *testing.T) {
	var calls []recordedCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := recordCall(t, r)
		calls = append(calls, call)
		switch r.URL.Path {
		case "/write/CreateStack":
			http.Error(w, `{"message":"stack already exists"}`, http.StatusConflict)
		case "/read/GetStack":
			require.Equal(t, "web", call.JSON["stack"])
			writeJSON(t, w, map[string]any{"_id": map[string]string{"$oid": "komodo-stack-existing"}})
		case "/write/UpdateStack":
			require.Equal(t, "komodo-stack-existing", call.JSON["id"])
			writeJSON(t, w, map[string]any{"_id": map[string]string{"$oid": "komodo-stack-existing"}})
		case "/execute/DeployStack":
			require.Equal(t, "komodo-stack-existing", call.JSON["stack"])
			writeJSON(t, w, map[string]any{"id": "komodo-deploy-1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewKomodoAdapter(HTTPConfig{
		BaseURL: server.URL,
		APIKey:  "key-1",
		Secret:  "secret-1",
		Client:  server.Client(),
	})

	ref, err := adapter.ApplyCompose(context.Background(), AppManifest{
		Name:        "web",
		Platform:    models.PAASKomodo,
		ManagedBy:   models.PAASKomodo,
		ComposeYAML: "services: {}\n",
	})

	require.NoError(t, err)
	assert.Equal(t, "komodo-stack-existing", ref.ExternalID)
	assert.Equal(t, "komodo-deploy-1", ref.DeploymentID)
	require.Len(t, calls, 4)
	assert.Equal(t, "/write/CreateStack", calls[0].Path)
	assert.Equal(t, "/read/GetStack", calls[1].Path)
	assert.Equal(t, "/write/UpdateStack", calls[2].Path)
	assert.Equal(t, "/execute/DeployStack", calls[3].Path)
}

func TestKomodoAdapterApplyComposeWaitsForDeployUpdateCompletion(t *testing.T) {
	withFastKomodoDeployTiming(t)

	var calls []recordedCall
	updatePolls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := recordCall(t, r)
		calls = append(calls, call)
		switch r.URL.Path {
		case "/write/CreateStack":
			writeJSON(t, w, map[string]any{"_id": map[string]string{"$oid": "komodo-stack-1"}})
		case "/execute/DeployStack":
			writeJSON(t, w, map[string]any{
				"_id":    map[string]string{"$oid": "komodo-update-1"},
				"status": "InProgress",
			})
		case "/read/GetUpdate":
			require.Equal(t, "komodo-update-1", call.JSON["id"])
			updatePolls++
			if updatePolls == 1 {
				writeJSON(t, w, map[string]any{
					"_id":     map[string]string{"$oid": "komodo-update-1"},
					"status":  "InProgress",
					"success": false,
				})
				return
			}
			writeJSON(t, w, map[string]any{
				"_id":     map[string]string{"$oid": "komodo-update-1"},
				"status":  "Complete",
				"success": true,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewKomodoAdapter(HTTPConfig{
		BaseURL: server.URL,
		APIKey:  "key-1",
		Secret:  "secret-1",
		Client:  server.Client(),
	})

	ref, err := adapter.ApplyCompose(context.Background(), AppManifest{
		Name:        "web",
		Platform:    models.PAASKomodo,
		ManagedBy:   models.PAASKomodo,
		ComposeYAML: "services: {}\n",
	})

	require.NoError(t, err)
	assert.Equal(t, "komodo-stack-1", ref.ExternalID)
	assert.Equal(t, "komodo-update-1", ref.DeploymentID)
	assert.Equal(t, 2, updatePolls)
	require.Len(t, calls, 4)
	assert.Equal(t, "/execute/DeployStack", calls[1].Path)
	assert.Equal(t, "/read/GetUpdate", calls[2].Path)
	assert.Equal(t, "/read/GetUpdate", calls[3].Path)
}

func TestKomodoAdapterApplyComposeRetriesWhenServerIsNotReachableYet(t *testing.T) {
	withFastKomodoDeployTiming(t)

	var calls []recordedCall
	deployAttempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := recordCall(t, r)
		calls = append(calls, call)
		switch r.URL.Path {
		case "/write/CreateStack":
			writeJSON(t, w, map[string]any{"_id": map[string]string{"$oid": "komodo-stack-1"}})
		case "/execute/DeployStack":
			deployAttempts++
			if deployAttempts == 1 {
				writeJSON(t, w, map[string]any{
					"success": false,
					"logs":    []any{map[string]any{"message": "Cannot send command when Server is unreachable or disabled"}},
				})
				return
			}
			writeJSON(t, w, map[string]any{"id": "komodo-deploy-1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewKomodoAdapter(HTTPConfig{
		BaseURL: server.URL,
		APIKey:  "key-1",
		Secret:  "secret-1",
		Client:  server.Client(),
	})

	ref, err := adapter.ApplyCompose(context.Background(), AppManifest{
		Name:        "web",
		Platform:    models.PAASKomodo,
		ManagedBy:   models.PAASKomodo,
		ComposeYAML: "services: {}\n",
	})

	require.NoError(t, err)
	assert.Equal(t, "komodo-stack-1", ref.ExternalID)
	assert.Equal(t, "komodo-deploy-1", ref.DeploymentID)
	assert.Equal(t, 2, deployAttempts)
	require.Len(t, calls, 3)
	assert.Equal(t, "/execute/DeployStack", calls[1].Path)
	assert.Equal(t, "/execute/DeployStack", calls[2].Path)
}

func withFastKomodoDeployTiming(t *testing.T) {
	t.Helper()
	previousRetryDelay := komodoDeployRetryDelay
	previousPollDelay := komodoDeployPollDelay
	previousPollAttempt := komodoDeployPollAttempt
	komodoDeployRetryDelay = time.Millisecond
	komodoDeployPollDelay = time.Millisecond
	komodoDeployPollAttempt = 4
	t.Cleanup(func() {
		komodoDeployRetryDelay = previousRetryDelay
		komodoDeployPollDelay = previousPollDelay
		komodoDeployPollAttempt = previousPollAttempt
	})
}

func TestKomodoAdapterApplyComposeErrorsWhenCreateResponseCannotResolveID(t *testing.T) {
	var calls []recordedCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, recordCall(t, r))
		switch r.URL.Path {
		case "/write/CreateStack":
			writeJSON(t, w, map[string]any{"ok": true})
		case "/read/GetStack":
			writeJSON(t, w, map[string]any{"name": "web"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewKomodoAdapter(HTTPConfig{
		BaseURL: server.URL,
		APIKey:  "key-1",
		Secret:  "secret-1",
		Client:  server.Client(),
	})

	_, err := adapter.ApplyCompose(context.Background(), AppManifest{
		Name:        "web",
		Platform:    models.PAASKomodo,
		ManagedBy:   models.PAASKomodo,
		ComposeYAML: "services: {}\n",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetStack returned no stack id")
	require.Len(t, calls, 2)
	assert.Equal(t, "/write/CreateStack", calls[0].Path)
	assert.Equal(t, "/read/GetStack", calls[1].Path)
}

func TestKomodoAdapterApplyComposeRejectsMissingAPISecret(t *testing.T) {
	adapter := NewKomodoAdapter(HTTPConfig{
		BaseURL: "http://komodo.example.test",
		APIKey:  "key-1",
	})

	_, err := adapter.ApplyCompose(context.Background(), AppManifest{
		Name:        "web",
		Platform:    models.PAASKomodo,
		ManagedBy:   models.PAASKomodo,
		ComposeYAML: "services: {}\n",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "api key and api secret")
}

func TestCoolifyAdapterNormalizesIndentedComposeBeforeUpload(t *testing.T) {
	var calls []recordedCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, recordCall(t, r))
		switch r.URL.Path {
		case "/api/v1/services":
			writeJSON(t, w, map[string]string{"uuid": "coolify-app-1"})
		case "/api/v1/services/coolify-app-1/start":
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

	_, err := adapter.ApplyCompose(context.Background(), AppManifest{
		Name:      "vaultwarden",
		Platform:  models.PAASCoolify,
		ManagedBy: models.PAASCoolify,
		ComposeYAML: `
    services:
      vaultwarden:
        image: vaultwarden/server:latest
        ports:
          - "8080:80"
`,
	})

	require.NoError(t, err)
	require.Len(t, calls, 2)
	encoded, ok := calls[0].JSON["docker_compose_raw"].(string)
	require.True(t, ok)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	assert.Equal(t, "services:\n  vaultwarden:\n    image: vaultwarden/server:latest\n    ports:\n      - \"8080:80\"\n", string(decoded))
}

func TestCoolifyAdapterOmitsCoolifyURLRoutesWhenComposeDefinesTraefikRouters(t *testing.T) {
	var calls []recordedCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, recordCall(t, r))
		switch r.URL.Path {
		case "/api/v1/services":
			writeJSON(t, w, map[string]string{"uuid": "coolify-whoami"})
		case "/api/v1/services/coolify-whoami/start":
			writeJSON(t, w, map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewCoolifyAdapter(HTTPConfig{BaseURL: server.URL, Client: server.Client()})

	_, err := adapter.ApplyCompose(context.Background(), AppManifest{
		Name:      "whoami",
		Platform:  models.PAASCoolify,
		ManagedBy: models.PAASCoolify,
		URL:       "http://whoami.home.localhost",
		ComposeYAML: `services:
  whoami:
    image: traefik/whoami:latest
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.whoami.rule=Host(` + "`whoami.home.localhost`" + `)"
      - "traefik.http.routers.whoami.middlewares=tinyauth@docker"
`,
	})

	require.NoError(t, err)
	require.Len(t, calls, 2)
	assert.NotContains(t, calls[0].JSON, "urls")
}

func TestCoolifyAdapterApplyComposeUpdatesOnCreateConflict(t *testing.T) {
	var calls []recordedCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, recordCall(t, r))
		switch r.URL.Path {
		case "/api/v1/services":
			http.Error(w, `{"message":"already exists","uuid":"coolify-app-existing"}`, http.StatusConflict)
		case "/api/v1/services/coolify-app-existing":
			require.Equal(t, http.MethodPatch, r.Method)
			writeJSON(t, w, map[string]string{"uuid": "coolify-app-existing"})
		case "/api/v1/services/coolify-app-existing/start":
			require.Equal(t, http.MethodPost, r.Method)
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
	assert.Equal(t, "/api/v1/services", calls[0].Path)
	assert.Equal(t, "/api/v1/services/coolify-app-existing", calls[1].Path)
	assert.Equal(t, "/api/v1/services/coolify-app-existing/start", calls[2].Path)
	assert.Equal(t, "Bearer token-2", calls[1].Authorization)
	assert.Equal(t, "web", calls[1].JSON["name"])
}

func TestCoolifyAdapterUsesComposeServiceNameForServiceResource(t *testing.T) {
	var calls []recordedCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, recordCall(t, r))
		switch r.URL.Path {
		case "/api/v1/services":
			writeJSON(t, w, map[string]string{"uuid": "coolify-immich"})
		case "/api/v1/services/coolify-immich/start":
			writeJSON(t, w, map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewCoolifyAdapter(HTTPConfig{BaseURL: server.URL, Client: server.Client()})

	ref, err := adapter.ApplyCompose(context.Background(), AppManifest{
		Name:      "immich",
		Platform:  models.PAASCoolify,
		ManagedBy: models.PAASCoolify,
		URL:       "http://photos.example.test",
		ComposeYAML: `services:
  immich-server:
    image: ghcr.io/immich-app/immich-server:release
  immich-redis:
    image: redis:7-alpine
`,
	})

	require.NoError(t, err)
	assert.Equal(t, "immich", ref.AppName)
	assert.Equal(t, "coolify-immich", ref.ExternalID)
	require.Len(t, calls, 2)
	assert.Equal(t, "immich-server", calls[0].JSON["name"])
	urls, ok := calls[0].JSON["urls"].([]any)
	require.True(t, ok)
	require.Len(t, urls, 1)
	route, ok := urls[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "immich-server", route["name"])
	assert.Equal(t, "http://photos.example.test", route["url"])
}

func TestCoolifyAdapterApplyComposeUsesLegacyDockerComposeAPIWhenEnabled(t *testing.T) {
	var calls []recordedCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, recordCall(t, r))
		switch r.URL.Path {
		case "/api/v1/applications/dockercompose":
			writeJSON(t, w, map[string]string{"uuid": "coolify-legacy-app-1"})
		case "/api/v1/deploy":
			assert.Equal(t, "coolify-legacy-app-1", r.URL.Query().Get("uuid"))
			writeJSON(t, w, map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewCoolifyAdapter(HTTPConfig{
		BaseURL:                server.URL,
		Token:                  "token-2",
		Client:                 server.Client(),
		LegacyDockerComposeAPI: true,
	})

	ref, err := adapter.ApplyCompose(context.Background(), AppManifest{
		Name:        "web",
		Platform:    models.PAASCoolify,
		ManagedBy:   models.PAASCoolify,
		ComposeYAML: "services: {}\n",
	})

	require.NoError(t, err)
	assert.Equal(t, "coolify-legacy-app-1", ref.ExternalID)
	require.Len(t, calls, 2)
	assert.Equal(t, "/api/v1/applications/dockercompose", calls[0].Path)
	assert.Equal(t, "/api/v1/deploy", calls[1].Path)
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
	Method        string
	Path          string
	Authorization string
	APIKey        string
	APISecret     string
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
		Method:        r.Method,
		Path:          r.URL.Path,
		Authorization: r.Header.Get("Authorization"),
		APIKey:        r.Header.Get("X-Api-Key"),
		APISecret:     r.Header.Get("X-Api-Secret"),
		JSON:          payload,
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(value))
}
