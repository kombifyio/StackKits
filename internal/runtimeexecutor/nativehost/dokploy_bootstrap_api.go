package nativehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// errDokployOwnerExists reports Dokploy's own refusal of a second owner
// sign-up. Without node custody for that owner the bootstrap fails closed.
var errDokployOwnerExists = errors.New("Dokploy already has an owner")

// dokployOwnerSession signs in the node's Dokploy owner over the private
// loopback API. Better Auth checks the request origin of cookie-bearing calls
// against BETTER_AUTH_URL, so every call names the routed UI origin.
type dokployOwnerSession struct {
	client   *http.Client
	endpoint string
	origin   string
}

func newDokployOwnerSession(endpoint, origin string) (*dokployOwnerSession, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar, Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &dokployOwnerSession{client: client, endpoint: endpoint, origin: origin}, nil
}

func (s *dokployOwnerSession) call(ctx context.Context, method, path string, body, out any) (int, string, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, "", err
		}
		defer clear(raw)
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, s.endpoint+path, reader)
	if err != nil {
		return 0, "", err
	}
	request.Header.Set("Origin", s.origin)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	reply, err := s.client.Do(request)
	if err != nil {
		return 0, "", fmt.Errorf("Dokploy owner API %s is unreachable", path)
	}
	defer reply.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(reply.Body, 1<<20))
	defer clear(raw)
	if err != nil {
		return reply.StatusCode, "", err
	}
	if reply.StatusCode < 200 || reply.StatusCode > 299 {
		var refusal struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &refusal)
		return reply.StatusCode, refusal.Message, fmt.Errorf("Dokploy owner API %s returned HTTP %d", path, reply.StatusCode)
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return reply.StatusCode, "", fmt.Errorf("Dokploy owner API %s returned an unreadable body", path)
	}
	return reply.StatusCode, "", nil
}

// signUp creates the first and only Dokploy owner. Dokploy closes sign-up by
// itself once an owner exists and answers "Admin is already created".
func (s *dokployOwnerSession) signUp(ctx context.Context, email, password string) (string, error) {
	var reply struct {
		User struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"user"`
	}
	status, message, err := s.call(ctx, http.MethodPost, "/api/auth/sign-up/email", map[string]string{"email": email, "password": password, "name": "StackKits", "lastName": "Owner"}, &reply)
	if status == http.StatusBadRequest && message == "Admin is already created" {
		return "", errDokployOwnerExists
	}
	if err != nil {
		return "", err
	}
	if reply.User.ID == "" || !strings.EqualFold(reply.User.Email, email) {
		return "", errors.New("Dokploy owner sign-up returned another identity")
	}
	return reply.User.ID, nil
}

func (s *dokployOwnerSession) signIn(ctx context.Context, email, password string) (string, error) {
	var reply struct {
		User struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"user"`
	}
	if _, _, err := s.call(ctx, http.MethodPost, "/api/auth/sign-in/email", map[string]string{"email": email, "password": password}, &reply); err != nil {
		return "", errors.New("Dokploy owner sign-in with node custody failed")
	}
	if reply.User.ID == "" || !strings.EqualFold(reply.User.Email, email) {
		return "", errors.New("Dokploy owner sign-in returned another identity")
	}
	return reply.User.ID, nil
}

func (s *dokployOwnerSession) organizationID(ctx context.Context) (string, error) {
	var organizations []struct {
		ID string `json:"id"`
	}
	if _, _, err := s.call(ctx, http.MethodGet, "/api/auth/organization/list", nil, &organizations); err != nil {
		return "", err
	}
	if len(organizations) != 1 || organizations[0].ID == "" {
		return "", errors.New("Dokploy owner must belong to exactly its own organization")
	}
	return organizations[0].ID, nil
}

// createAPIKey issues the node's API key. Better Auth rate-limits keys by
// default to a few requests per day, so the node key disables that limit.
func (s *dokployOwnerSession) createAPIKey(ctx context.Context, organizationID string) (string, error) {
	var reply struct {
		Key              string `json:"key"`
		RateLimitEnabled bool   `json:"rateLimitEnabled"`
	}
	body := map[string]any{"name": "stackkits-node", "metadata": map[string]string{"organizationId": organizationID}, "rateLimitEnabled": false}
	if _, _, err := s.call(ctx, http.MethodPost, "/api/user.createApiKey", body, &reply); err != nil {
		return "", err
	}
	if reply.Key == "" || strings.ContainsAny(reply.Key, "\r\n\x00 ") || reply.RateLimitEnabled {
		return "", errors.New("Dokploy did not issue an unlimited node API key")
	}
	return reply.Key, nil
}

// dokployKeyCall is one API-key request against the private loopback API.
func dokployKeyCall(ctx context.Context, endpoint, key, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("x-api-key", key)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	reply, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Dokploy API %s is unreachable", path)
	}
	defer reply.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(reply.Body, 4<<20))
	if err != nil {
		return err
	}
	if reply.StatusCode != http.StatusOK {
		return fmt.Errorf("Dokploy API %s returned HTTP %d", path, reply.StatusCode)
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return fmt.Errorf("Dokploy API %s returned an unreadable body", path)
	}
	return nil
}

// dokployEnsureProject creates the node's StackKits project once and later
// requires the same project and environment from custody.
func dokployEnsureProject(ctx context.Context, endpoint, key, name string, projectID, environmentID *string) error {
	if *projectID == "" {
		var created struct {
			Project struct {
				ProjectID string `json:"projectId"`
			} `json:"project"`
			Environment struct {
				EnvironmentID string `json:"environmentId"`
			} `json:"environment"`
		}
		if err := dokployKeyCall(ctx, endpoint, key, http.MethodPost, "/api/project.create", map[string]string{"name": name, "description": "Managed by StackKits"}, &created); err != nil {
			return err
		}
		if created.Project.ProjectID == "" || created.Environment.EnvironmentID == "" {
			return errors.New("Dokploy project creation returned no project or environment")
		}
		*projectID, *environmentID = created.Project.ProjectID, created.Environment.EnvironmentID
		return nil
	}
	var project struct {
		ProjectID    string `json:"projectId"`
		Name         string `json:"name"`
		Environments []struct {
			EnvironmentID string `json:"environmentId"`
		} `json:"environments"`
	}
	if err := dokployKeyCall(ctx, endpoint, key, http.MethodGet, "/api/project.one?"+url.Values{"projectId": {*projectID}}.Encode(), nil, &project); err != nil {
		return err
	}
	if project.ProjectID != *projectID || project.Name != name {
		return errors.New("Dokploy StackKits project differs from node custody")
	}
	for _, environment := range project.Environments {
		if environment.EnvironmentID == *environmentID {
			return nil
		}
	}
	return errors.New("Dokploy StackKits environment differs from node custody")
}

// dokployOwners returns the e-mail addresses of the organization's owners.
func dokployOwners(ctx context.Context, endpoint, key string) ([]string, error) {
	var members []struct {
		Role string `json:"role"`
		User struct {
			Email string `json:"email"`
		} `json:"user"`
	}
	if err := dokployKeyCall(ctx, endpoint, key, http.MethodGet, "/api/user.all", nil, &members); err != nil {
		return nil, err
	}
	var owners []string
	for _, member := range members {
		if member.Role == "owner" {
			owners = append(owners, member.User.Email)
		}
	}
	return owners, nil
}
