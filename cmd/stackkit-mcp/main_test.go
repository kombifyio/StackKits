package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDocsModeExposesToolsResourcesAndPrompts(t *testing.T) {
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	app := &stackkitMCP{opts: serverOptions{modes: map[string]bool{"docs": true}}, docs: loadDocs()}
	serverSession, err := app.newServer().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer func() { _ = serverSession.Wait() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = clientSession.Close() }()

	foundTool := false
	for tool, err := range clientSession.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("tools: %v", err)
		}
		if tool.Name == "stackkit_docs_search" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Fatal("stackkit_docs_search not listed")
	}

	foundResource := false
	for resource, err := range clientSession.Resources(ctx, nil) {
		if err != nil {
			t.Fatalf("resources: %v", err)
		}
		if resource.Name == "docs/agent/agents.md" {
			foundResource = true
		}
	}
	if !foundResource {
		t.Fatal("docs/agent/agents.md resource not listed")
	}

	foundPrompt := false
	for prompt, err := range clientSession.Prompts(ctx, nil) {
		if err != nil {
			t.Fatalf("prompts: %v", err)
		}
		if prompt.Name == "stackkit_basekit_autonomous_rollout" {
			foundPrompt = true
		}
	}
	if !foundPrompt {
		t.Fatal("stackkit_basekit_autonomous_rollout prompt not listed")
	}
}

func TestReadOnlyDefaultsDoNotExposeActionTools(t *testing.T) {
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	app := &stackkitMCP{opts: serverOptions{modes: map[string]bool{"docs": true, "actions": true}}, docs: loadDocs()}
	serverSession, err := app.newServer().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer func() { _ = serverSession.Wait() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = clientSession.Close() }()

	for tool, err := range clientSession.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("tools: %v", err)
		}
		if tool.Name == "stackkit_apply" {
			t.Fatal("stackkit_apply must not be registered unless STACKKIT_MCP_ALLOW_WRITE=true")
		}
	}
}

func TestActionToolsRequireAllowWrite(t *testing.T) {
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	app := &stackkitMCP{opts: serverOptions{modes: map[string]bool{"actions": true}, allowWrite: true}, docs: loadDocs()}
	serverSession, err := app.newServer().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer func() { _ = serverSession.Wait() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = clientSession.Close() }()

	found := false
	for tool, err := range clientSession.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("tools: %v", err)
		}
		if tool.Name == "stackkit_apply" {
			found = true
		}
	}
	if !found {
		t.Fatal("stackkit_apply should be registered when write gate is enabled")
	}
}

func TestEmbeddedDocsAndOpenAPI(t *testing.T) {
	docs := loadDocs()
	if !strings.Contains(docs["docs/agent/agents.md"], "BaseKit is the verified beta") {
		t.Fatal("embedded agent guide missing BaseKit release stance")
	}
	if !strings.Contains(openAPISpec(), "/api/v1/status") {
		t.Fatal("embedded OpenAPI spec missing management status")
	}
	if !strings.Contains(endpointSnippet(openAPISpec(), "/api/v1/status"), "getManagementStatus") {
		t.Fatal("endpoint snippet missing status operation")
	}
}

func TestRequireMCPToken(t *testing.T) {
	nextCalled := false
	handler := requireMCPToken("secret", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/", nil))
	if rr.Code != http.StatusUnauthorized || nextCalled {
		t.Fatal("missing MCP token should be rejected")
	}

	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent || !nextCalled {
		t.Fatal("valid MCP bearer token should be accepted")
	}
}

func TestLoopbackAndHTTPServerHardening(t *testing.T) {
	if !isLoopbackListenAddr("127.0.0.1:8091") || !isLoopbackListenAddr("localhost:8091") {
		t.Fatal("loopback addresses should be accepted")
	}
	if isLoopbackListenAddr("0.0.0.0:8091") {
		t.Fatal("wildcard address is not loopback")
	}
	server := newHTTPServer("127.0.0.1:0", http.NotFoundHandler())
	if server.ReadHeaderTimeout != 15*time.Second || server.ReadTimeout != 30*time.Second || server.IdleTimeout != 120*time.Second {
		t.Fatalf("unexpected server timeouts: %+v", server)
	}
}
