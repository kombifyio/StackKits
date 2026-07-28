package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	projectionSchema = "stackkit.desired-identity-projection/v1"
	signatureDomain  = projectionSchema + "\x00"
	issuerID         = "kombify-cloud-fixture"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: identity-projection-fixture emit|serve-pocketid")
	}
	var err error
	switch os.Args[1] {
	case "emit":
		err = emit(os.Args[2:])
	case "serve-pocketid":
		err = servePocketID(os.Args[2:])
	default:
		err = errors.New("unknown fixture mode")
	}
	if err != nil {
		log.Fatal(err)
	}
}

func emit(arguments []string) error {
	flags := flag.NewFlagSet("emit", flag.ContinueOnError)
	var output, ownerRef, username, email, displayName string
	flags.StringVar(&output, "output", "", "output directory")
	flags.StringVar(&ownerRef, "owner-ref", "", "exact local ownerRef")
	flags.StringVar(&username, "username", "cloud-user", "desired PocketID username")
	flags.StringVar(&email, "email", "cloud-user@example.test", "desired PocketID email")
	flags.StringVar(&displayName, "display-name", "Cloud Convenience User", "desired display name")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if output == "" || !strings.HasPrefix(ownerRef, "owner/local/") {
		return errors.New("emit requires --output and --owner-ref")
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	keyDigest := sha256.Sum256(public)
	keyID := "ed25519://sha256/" + hex.EncodeToString(keyDigest[:])
	trust := map[string]any{
		"keys": []any{map[string]any{
			"issuerId":  issuerID,
			"keyId":     keyID,
			"publicKey": base64.RawStdEncoding.EncodeToString(public),
		}},
		"schemaVersion": "stackkit.advanced-trust-bundle/v1",
	}
	trustRaw, _ := json.Marshal(trust)
	now := time.Now().UTC().Truncate(time.Second)
	randomID := make([]byte, 16)
	if _, err := rand.Read(randomID); err != nil {
		return err
	}
	unsigned := map[string]any{
		"audience":  "stackkit-local-identity",
		"expiresAt": now.Add(30 * time.Minute).Format(time.RFC3339),
		"groups":    []string{"family"},
		"issuedAt":  now.Format(time.RFC3339),
		"issuerId":  issuerID,
		"keyId":     keyID,
		"kind":      "DesiredIdentityProjection",
		"ownerRef":  ownerRef,
		"profile": map[string]any{
			"displayName": displayName,
			"email":       email,
			"username":    username,
		},
		"projectionId":    "identity-projection/" + hex.EncodeToString(randomID),
		"requestedAction": "upsert",
		"schemaVersion":   projectionSchema,
		"signature":       "",
		"subjectRef":      "identity-source/fixture-user",
	}
	unsignedRaw, _ := json.Marshal(unsigned)
	digest := sha256.Sum256(append([]byte(signatureDomain), unsignedRaw...))
	unsigned["signature"] = base64.RawStdEncoding.EncodeToString(ed25519.Sign(private, digest[:]))
	projectionRaw, _ := json.Marshal(unsigned)
	if err := os.MkdirAll(output, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(output, "trust-bundle.json"), trustRaw, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(output, "desired-projection.json"), projectionRaw, 0o600); err != nil {
		return err
	}
	projectionDigest := sha256.Sum256(projectionRaw)
	manifest, _ := json.Marshal(map[string]any{
		"schemaVersion":            "stackkit.identity-projection-fixture/v1",
		"issuerId":                 issuerID,
		"keyId":                    keyID,
		"projectionSHA256":         "sha256:" + hex.EncodeToString(projectionDigest[:]),
		"privateMaterialPersisted": false,
	})
	return os.WriteFile(filepath.Join(output, "manifest.json"), manifest, 0o600)
}

type pocketState struct {
	mu     sync.Mutex
	users  map[string]map[string]any
	groups map[string]map[string]any
}

func servePocketID(arguments []string) error {
	flags := flag.NewFlagSet("serve-pocketid", flag.ContinueOnError)
	var listen string
	flags.StringVar(&listen, "listen", "127.0.0.1:1411", "listen address")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	state := &pocketState{
		users:  map[string]map[string]any{},
		groups: map[string]map[string]any{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/users", state.usersHandler)
	mux.HandleFunc("/api/users/", state.userHandler)
	mux.HandleFunc("/api/user-groups", state.groupsHandler)
	server := &http.Server{
		Addr: listen, Handler: mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("PocketID identity fixture listening on %s", listen)
	return server.ListenAndServe()
}

func (s *pocketState) usersHandler(writer http.ResponseWriter, request *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch request.Method {
	case http.MethodGet:
		search := request.URL.Query().Get("search")
		data := make([]any, 0)
		for _, user := range s.users {
			if user["username"] == search {
				data = append(data, user)
			}
		}
		writeJSON(writer, map[string]any{"data": data})
	case http.MethodPost:
		var input map[string]any
		if json.NewDecoder(request.Body).Decode(&input) != nil {
			http.Error(writer, "bad request", http.StatusBadRequest)
			return
		}
		id := fmt.Sprintf("fixture-user-%d", len(s.users)+1)
		input["id"] = id
		input["isAdmin"] = false
		input["disabled"] = false
		input["userGroups"] = []any{}
		s.users[id] = input
		writer.WriteHeader(http.StatusCreated)
		writeJSON(writer, input)
	default:
		http.Error(writer, "method", http.StatusMethodNotAllowed)
	}
}

func (s *pocketState) userHandler(writer http.ResponseWriter, request *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	relative := strings.TrimPrefix(request.URL.Path, "/api/users/")
	parts := strings.Split(relative, "/")
	user := s.users[parts[0]]
	if user == nil {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	if len(parts) == 1 && request.Method == http.MethodGet {
		writeJSON(writer, user)
		return
	}
	if len(parts) == 2 && parts[1] == "user-groups" && request.Method == http.MethodPut {
		var input struct {
			UserGroupIDs []string `json:"userGroupIds"`
		}
		if json.NewDecoder(request.Body).Decode(&input) != nil {
			http.Error(writer, "bad request", http.StatusBadRequest)
			return
		}
		groups := make([]any, 0, len(input.UserGroupIDs))
		for _, id := range input.UserGroupIDs {
			if group := s.groups[id]; group != nil {
				groups = append(groups, group)
			}
		}
		user["userGroups"] = groups
		writeJSON(writer, user)
		return
	}
	http.Error(writer, "method", http.StatusMethodNotAllowed)
}

func (s *pocketState) groupsHandler(writer http.ResponseWriter, request *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch request.Method {
	case http.MethodGet:
		search := request.URL.Query().Get("search")
		data := make([]any, 0)
		for _, group := range s.groups {
			if group["name"] == search {
				data = append(data, group)
			}
		}
		writeJSON(writer, map[string]any{"data": data})
	case http.MethodPost:
		var input map[string]any
		if json.NewDecoder(request.Body).Decode(&input) != nil {
			http.Error(writer, "bad request", http.StatusBadRequest)
			return
		}
		id := fmt.Sprintf("fixture-group-%d", len(s.groups)+1)
		input["id"] = id
		s.groups[id] = input
		writer.WriteHeader(http.StatusCreated)
		writeJSON(writer, input)
	default:
		http.Error(writer, "method", http.StatusMethodNotAllowed)
	}
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}
