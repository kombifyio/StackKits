package pocketid

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "test-static-key")
	return c, srv
}

func TestCreateUser(t *testing.T) {
	tests := []struct {
		name       string
		req        CreateUserRequest
		respStatus int
		respBody   string
		wantErr    bool
		wantStatus int
		wantUserID string
		wantEmail  string
	}{
		{
			name: "success path",
			req: CreateUserRequest{
				Username:  "alice",
				Email:     "alice@example.com",
				FirstName: "Alice",
				LastName:  "Owner",
				IsAdmin:   true,
			},
			respStatus: http.StatusCreated,
			respBody:   `{"id":"uid-1","username":"alice","email":"alice@example.com","firstName":"Alice","lastName":"Owner","displayName":"","isAdmin":true}`,
			wantUserID: "uid-1",
			wantEmail:  "alice@example.com",
		},
		{
			name: "200 also accepted (PocketID returns 200, not 201)",
			req: CreateUserRequest{
				Username: "bob",
			},
			respStatus: http.StatusOK,
			respBody:   `{"id":"uid-2","username":"bob","email":null,"firstName":"","lastName":null,"displayName":"","isAdmin":false}`,
			wantUserID: "uid-2",
			wantEmail:  "",
		},
		{
			name:       "401 maps to HTTPError",
			req:        CreateUserRequest{Username: "x"},
			respStatus: http.StatusUnauthorized,
			respBody:   `{"error":"You are not signed in"}`,
			wantErr:    true,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "500 maps to HTTPError",
			req:        CreateUserRequest{Username: "x"},
			respStatus: http.StatusInternalServerError,
			respBody:   `{"error":"boom"}`,
			wantErr:    true,
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("method: got %s, want POST", r.Method)
				}
				if r.URL.Path != "/api/users" {
					t.Errorf("path: got %s, want /api/users", r.URL.Path)
				}
				if got := r.Header.Get("X-API-Key"); got != "test-static-key" {
					t.Errorf("X-API-Key: got %q, want test-static-key", got)
				}
				if got := r.Header.Get("Content-Type"); got != "application/json" {
					t.Errorf("Content-Type: got %q", got)
				}
				var body CreateUserRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if body.Username != tt.req.Username {
					t.Errorf("body.username: got %q, want %q", body.Username, tt.req.Username)
				}
				w.WriteHeader(tt.respStatus)
				_, _ = io.WriteString(w, tt.respBody)
			})

			user, err := c.CreateUser(t.Context(), tt.req)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				var herr *HTTPError
				if !errors.As(err, &herr) {
					t.Fatalf("expected HTTPError, got %T: %v", err, err)
				}
				if herr.StatusCode != tt.wantStatus {
					t.Errorf("status: got %d, want %d", herr.StatusCode, tt.wantStatus)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if user.ID != tt.wantUserID {
				t.Errorf("user.ID: got %q, want %q", user.ID, tt.wantUserID)
			}
			if user.Email != tt.wantEmail {
				t.Errorf("user.Email: got %q, want %q", user.Email, tt.wantEmail)
			}
		})
	}
}

func TestAddUserToGroup(t *testing.T) {
	t.Run("success path", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Errorf("method: got %s, want PUT", r.Method)
			}
			if r.URL.Path != "/api/users/uid-1/user-groups" {
				t.Errorf("path: got %s", r.URL.Path)
			}
			var body struct {
				UserGroupIDs []string `json:"userGroupIds"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(body.UserGroupIDs) != 1 || body.UserGroupIDs[0] != "grp-1" {
				t.Errorf("userGroupIds: got %v, want [grp-1]", body.UserGroupIDs)
			}
			w.WriteHeader(http.StatusOK)
		})

		if err := c.AddUserToGroup(t.Context(), "uid-1", "grp-1"); err != nil {
			t.Fatalf("AddUserToGroup: %v", err)
		}
	})

	t.Run("404 surfaces HTTPError", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"user not found"}`)
		})
		err := c.AddUserToGroup(t.Context(), "missing", "grp-1")
		if err == nil {
			t.Fatal("expected error")
		}
		var herr *HTTPError
		if !errors.As(err, &herr) {
			t.Fatalf("expected HTTPError, got %T", err)
		}
		if herr.StatusCode != http.StatusNotFound {
			t.Errorf("status: got %d, want 404", herr.StatusCode)
		}
	})
}

func TestCreateUserGroup(t *testing.T) {
	t.Run("success returns the created group", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method: got %s, want POST", r.Method)
			}
			if r.URL.Path != "/api/user-groups" {
				t.Errorf("path: got %s, want /api/user-groups", r.URL.Path)
			}
			if got := r.Header.Get("X-API-Key"); got != "test-static-key" {
				t.Errorf("X-API-Key: got %q", got)
			}
			var body CreateUserGroupRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Name != "owners" {
				t.Errorf("name: got %q, want owners", body.Name)
			}
			if body.FriendlyName != "Owners" {
				t.Errorf("friendlyName: got %q, want Owners", body.FriendlyName)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"grp-owners","name":"owners","friendlyName":"Owners"}`)
		})

		group, err := c.CreateUserGroup(t.Context(), CreateUserGroupRequest{
			Name:         "owners",
			FriendlyName: "Owners",
		})
		if err != nil {
			t.Fatalf("CreateUserGroup: %v", err)
		}
		if group.ID != "grp-owners" {
			t.Errorf("ID: got %q, want grp-owners", group.ID)
		}
		if group.Name != "owners" {
			t.Errorf("Name: got %q, want owners", group.Name)
		}
		if group.FriendlyName != "Owners" {
			t.Errorf("FriendlyName: got %q, want Owners", group.FriendlyName)
		}
	})

	t.Run("409 Conflict maps to ErrAlreadyExists", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"error":"group already exists"}`)
		})
		_, err := c.CreateUserGroup(t.Context(), CreateUserGroupRequest{
			Name:         "owners",
			FriendlyName: "Owners",
		})
		if !errors.Is(err, ErrAlreadyExists) {
			t.Errorf("expected ErrAlreadyExists, got %v", err)
		}
	})

	t.Run("500 propagates as HTTPError", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":"server"}`)
		})
		_, err := c.CreateUserGroup(t.Context(), CreateUserGroupRequest{
			Name:         "owners",
			FriendlyName: "Owners",
		})
		if err == nil {
			t.Fatal("expected error")
		}
		if errors.Is(err, ErrAlreadyExists) {
			t.Fatal("must not be ErrAlreadyExists on 500")
		}
		var herr *HTTPError
		if !errors.As(err, &herr) {
			t.Fatalf("expected HTTPError, got %T: %v", err, err)
		}
		if herr.StatusCode != http.StatusInternalServerError {
			t.Errorf("status: got %d", herr.StatusCode)
		}
	})
}

func TestGetGroupIDByName(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user-groups" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("search"); got != "owners" {
			t.Errorf("search query: got %q, want owners", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"data":[{"id":"grp-owners","name":"owners"},{"id":"grp-x","name":"owners-team"}]}`)
	})

	id, err := c.GetGroupIDByName(t.Context(), "owners")
	if err != nil {
		t.Fatalf("GetGroupIDByName: %v", err)
	}
	if id != "grp-owners" {
		t.Errorf("id: got %q, want grp-owners", id)
	}

	t.Run("not found returns empty string", func(t *testing.T) {
		c2, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"data":[]}`)
		})
		id, err := c2.GetGroupIDByName(t.Context(), "ghosts")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if id != "" {
			t.Errorf("id: got %q, want empty", id)
		}
	})
}

func TestRegisterOIDCClient(t *testing.T) {
	createCalled := false
	secretCalled := false
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/oidc/clients":
			createCalled = true
			if r.Method != http.MethodPost {
				t.Errorf("clients method: got %s", r.Method)
			}
			var body RegisterClientRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Name != "TinyAuth" {
				t.Errorf("name: got %q", body.Name)
			}
			if len(body.CallbackURLs) != 1 || body.CallbackURLs[0] != "https://app/cb" {
				t.Errorf("callbackURLs: got %v", body.CallbackURLs)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"client-1","name":"TinyAuth","callbackURLs":["https://app/cb"],"isPublic":false}`)
		case "/api/oidc/clients/client-1/secret":
			secretCalled = true
			if r.Method != http.MethodPost {
				t.Errorf("secret method: got %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"secret":"super-secret-value"}`)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	cl, err := c.RegisterOIDCClient(t.Context(), RegisterClientRequest{
		Name:         "TinyAuth",
		CallbackURLs: []string{"https://app/cb"},
	})
	if err != nil {
		t.Fatalf("RegisterOIDCClient: %v", err)
	}
	if !createCalled || !secretCalled {
		t.Errorf("expected both create and secret calls, got create=%v secret=%v", createCalled, secretCalled)
	}
	if cl.ID != "client-1" {
		t.Errorf("ID: got %q", cl.ID)
	}
	if cl.Secret != "super-secret-value" {
		t.Errorf("Secret: got %q", cl.Secret)
	}
}

func TestWaitHealthy(t *testing.T) {
	t.Run("returns immediately on 204", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != healthzPath {
				t.Errorf("path: got %s, want %s", r.URL.Path, healthzPath)
			}
			w.WriteHeader(http.StatusNoContent)
		})
		if err := c.WaitHealthy(t.Context(), 2*time.Second); err != nil {
			t.Fatalf("WaitHealthy: %v", err)
		}
	})

	t.Run("times out when server hangs", func(t *testing.T) {
		// Server that never responds. We use a context channel to release
		// goroutines on test cleanup so we don't leak.
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-release:
			}
		})
		// Tighten the per-request HTTP timeout so we don't depend on the
		// 30s default for this case.
		c.HTTP = &http.Client{Timeout: 50 * time.Millisecond}

		start := time.Now()
		err := c.WaitHealthy(t.Context(), 200*time.Millisecond)
		elapsed := time.Since(start)
		if err == nil {
			t.Fatal("expected timeout error")
		}
		if !strings.Contains(err.Error(), "deadline") && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected deadline error, got: %v", err)
		}
		if elapsed > 1*time.Second {
			t.Errorf("WaitHealthy took too long: %v", elapsed)
		}
	})

	t.Run("retries until 200", func(t *testing.T) {
		var calls int
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls < 3 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		})
		if err := c.WaitHealthy(t.Context(), 5*time.Second); err != nil {
			t.Fatalf("WaitHealthy: %v", err)
		}
		if calls < 3 {
			t.Errorf("expected at least 3 calls, got %d", calls)
		}
	})
}

func TestBootstrapInitialAdmin(t *testing.T) {
	t.Run("token valid returns ErrAlreadyBootstrapped", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/users" {
				t.Errorf("path: got %s", r.URL.Path)
			}
			if got := r.Header.Get("X-API-Key"); got != "test-static-key" {
				t.Errorf("X-API-Key: got %q", got)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"data":[],"pagination":{"totalItems":0}}`)
		})

		token, err := c.BootstrapInitialAdmin(t.Context(), "owner@example.com", "owner", "irrelevant")
		if !errors.Is(err, ErrAlreadyBootstrapped) {
			t.Fatalf("expected ErrAlreadyBootstrapped, got %v", err)
		}
		if token != "test-static-key" {
			t.Errorf("token: got %q", token)
		}
	})

	t.Run("missing AdminToken errors before any HTTP call", func(t *testing.T) {
		called := false
		_, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			called = true
		})
		c := NewClient(srv.URL, "")

		_, err := c.BootstrapInitialAdmin(t.Context(), "", "", "")
		if err == nil {
			t.Fatal("expected error for empty token")
		}
		if errors.Is(err, ErrAlreadyBootstrapped) {
			t.Fatal("should not be ErrAlreadyBootstrapped")
		}
		if called {
			t.Error("HTTP server should not have been called")
		}
	})

	t.Run("token rejected surfaces HTTPError", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"You are not signed in"}`)
		})
		_, err := c.BootstrapInitialAdmin(t.Context(), "", "", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if errors.Is(err, ErrAlreadyBootstrapped) {
			t.Fatal("should not be ErrAlreadyBootstrapped on 401")
		}
		var herr *HTTPError
		if !errors.As(err, &herr) {
			t.Fatalf("expected HTTPError, got %T: %v", err, err)
		}
		if herr.StatusCode != http.StatusUnauthorized {
			t.Errorf("status: got %d", herr.StatusCode)
		}
	})
}

func TestCreateOneTimeAccessToken(t *testing.T) {
	t.Run("success returns raw token", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method: got %s, want POST", r.Method)
			}
			if r.URL.Path != "/api/users/uid-1/one-time-access-token" {
				t.Errorf("path: got %s", r.URL.Path)
			}
			if got := r.Header.Get("X-API-Key"); got != "test-static-key" {
				t.Errorf("X-API-Key: got %q", got)
			}
			var body struct {
				ExpiresAt string `json:"expiresAt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.ExpiresAt == "" {
				t.Error("expiresAt missing")
			}
			ts, err := time.Parse(time.RFC3339, body.ExpiresAt)
			if err != nil {
				t.Errorf("expiresAt not RFC3339: %q (%v)", body.ExpiresAt, err)
			}
			if delta := time.Until(ts); delta < 23*time.Hour || delta > 25*time.Hour {
				t.Errorf("expiresAt should be ~24h out, got %s", delta)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"token":"otat-abc123","expiresAt":"`+body.ExpiresAt+`"}`)
		})
		tok, err := c.CreateOneTimeAccessToken(t.Context(), "uid-1", 24*time.Hour)
		if err != nil {
			t.Fatalf("CreateOneTimeAccessToken: %v", err)
		}
		if tok != "otat-abc123" {
			t.Errorf("token: got %q, want otat-abc123", tok)
		}
	})

	t.Run("rejects empty userID without HTTP call", func(t *testing.T) {
		called := false
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			called = true
		})
		if _, err := c.CreateOneTimeAccessToken(t.Context(), "", time.Hour); err == nil {
			t.Fatal("expected error for empty userID")
		}
		if called {
			t.Error("HTTP server should not have been called")
		}
	})

	t.Run("rejects non-positive ttl without HTTP call", func(t *testing.T) {
		called := false
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			called = true
		})
		if _, err := c.CreateOneTimeAccessToken(t.Context(), "uid-1", 0); err == nil {
			t.Fatal("expected error for zero ttl")
		}
		if called {
			t.Error("HTTP server should not have been called")
		}
	})

	t.Run("404 surfaces HTTPError", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"user not found"}`)
		})
		_, err := c.CreateOneTimeAccessToken(t.Context(), "missing", time.Hour)
		if err == nil {
			t.Fatal("expected error")
		}
		var herr *HTTPError
		if !errors.As(err, &herr) {
			t.Fatalf("expected HTTPError, got %T: %v", err, err)
		}
		if herr.StatusCode != http.StatusNotFound {
			t.Errorf("status: got %d", herr.StatusCode)
		}
	})

	t.Run("empty token in response is an error", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"token":""}`)
		})
		if _, err := c.CreateOneTimeAccessToken(t.Context(), "uid-1", time.Hour); err == nil {
			t.Fatal("expected error for empty token")
		}
	})
}

func TestHTTPErrorString(t *testing.T) {
	e := &HTTPError{
		StatusCode: 401,
		Method:     "GET",
		Path:       "/api/users",
		Body:       `{"error":"nope"}`,
	}
	got := e.Error()
	if !strings.Contains(got, "401") || !strings.Contains(got, "GET") || !strings.Contains(got, "/api/users") {
		t.Errorf("Error() missing fields: %s", got)
	}
}
