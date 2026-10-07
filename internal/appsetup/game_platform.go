package appsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Game platforms of the game workload (ADR-0043, ADR-0048).
const (
	GamePlatformCalagopus   = "calagopus"
	GamePlatformPelican     = "pelican"
	GamePlatformPterodactyl = "pterodactyl"
)

// GameServerRequest is the owner's private setup input.
type GameServerRequest struct {
	Profile    string
	Name       string
	AcceptEULA bool
	AllowList  []string
	// Password is the owner-chosen join password for password-access games.
	Password        string
	OwnerEmail      string
	ExpectedVersion string
}

// GameServerResult is the secret-free observation of the created server.
type GameServerResult struct {
	ServerUUID string
	Identifier string
	Port       int
	Protocol   string
	Reachable  string // protocol answer observed on the node
	Created    bool
}

// GameServerSummary is the secret-free view of one owner game server.
type GameServerSummary struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
	State      string `json:"state"`
	Port       int    `json:"port,omitempty"`
}

// gameServerRecord is one server as the platform's administrative API
// reports it; ref addresses it there and Identifier in the owner's API.
type gameServerRecord struct {
	ref        string
	UUID       string
	Identifier string
	Installed  bool
	Installing bool
}

// gamePanel is the platform API surface the curated game operations use.
// Administrative calls use the setup key; owner calls the owner client key.
type gamePanel interface {
	lookupServer(ctx context.Context, externalID string) (gameServerRecord, bool, error)
	createServer(ctx context.Context, profile GameProfile, name, password, externalID, ownerEmail string) (gameServerRecord, error)
	convergeStartup(ctx context.Context, server gameServerRecord, profile GameProfile, name, password string) error
	refreshServer(ctx context.Context, server gameServerRecord) (gameServerRecord, error)
	reinstall(ctx context.Context, server gameServerRecord) error
	externalIDOf(ctx context.Context, identifier string) (string, error)
	state(ctx context.Context, identifier string) (string, error)
	power(ctx context.Context, identifier, signal string) error
	command(ctx context.Context, identifier, command string) error
	readFile(ctx context.Context, identifier, path string) (string, bool, error)
	writeFile(ctx context.Context, identifier, path, body string) error
	listServers(ctx context.Context) ([]GameServerSummary, error)
}

// GamePanel operates the installed Panel of one Game platform.
type GamePanel struct {
	platform string
	api      gamePanel
}

// NewGamePanel binds the Panel of platform at baseURL with the setup key and
// the owner's client key derived from custody (either may be empty when the
// operation does not need it).
func NewGamePanel(platform string, client *http.Client, baseURL, setupKey, clientKey string) (GamePanel, error) {
	api := panelHTTP{http: client, baseURL: strings.TrimRight(baseURL, "/"), setupKey: setupKey, clientKey: clientKey}
	switch platform {
	case GamePlatformCalagopus:
		return GamePanel{platform: platform, api: calagopusPanel{api}}, nil
	case GamePlatformPelican, GamePlatformPterodactyl:
		return GamePanel{platform: platform, api: pterodactylPanel{panelHTTP: api, pelican: platform == GamePlatformPelican}}, nil
	}
	return GamePanel{}, fmt.Errorf("game platform %q is not admitted", platform)
}

// CreateGameServer creates (or reconciles) exactly one curated game server,
// applies its secure defaults, admits the requested players, starts it and
// observes a real game-protocol answer on the node.
//
//nolint:gocyclo // One owner-approved action converges a complete server.
func (p GamePanel) CreateGameServer(ctx context.Context, request GameServerRequest) (GameServerResult, error) {
	profile, ok := GameProfiles[request.Profile]
	if !ok {
		return GameServerResult{}, fmt.Errorf("unknown game profile %q; choose one of %s", request.Profile, strings.Join(GameProfileIDs(), ", "))
	}
	if !request.AcceptEULA {
		return GameServerResult{}, errors.New("creating a game server requires the owner's own acceptance of the game's EULA (acceptEula: true)")
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = profile.DefaultName
	}
	if len(name) > 60 || strings.ContainsAny(name, "\r\n\"") {
		return GameServerResult{}, errors.New("server name must be a single line of at most 60 characters without quotes")
	}
	switch profile.Access {
	case "allow-list":
		if request.Password != "" {
			return GameServerResult{}, fmt.Errorf("%s admits players by %s; use allowList instead of a password", profile.DisplayName, profile.AccountHint)
		}
		for _, player := range request.AllowList {
			if !gamePlayerName.MatchString(player) {
				return GameServerResult{}, fmt.Errorf("player name %q is not a valid game account name", player)
			}
		}
	case "password":
		if len(request.AllowList) > 0 {
			return GameServerResult{}, fmt.Errorf("%s has no account allow list; share a join password instead", profile.DisplayName)
		}
		if err := validateGamePassword(request.Password, name); err != nil {
			return GameServerResult{}, err
		}
	}
	externalID := "stackkit-game-" + profile.ID + "-" + slugGameName(name)

	server, found, err := p.api.lookupServer(ctx, externalID)
	if err != nil {
		return GameServerResult{}, fmt.Errorf("look up existing game server: %w", err)
	}
	created := !found
	if created {
		if server, err = p.api.createServer(ctx, profile, name, request.Password, externalID, request.OwnerEmail); err != nil {
			return GameServerResult{}, err
		}
	} else if err := p.api.convergeStartup(ctx, server, profile, name, request.Password); err != nil {
		// Converge the curated environment (for example a changed join
		// password) on an existing server before it restarts.
		return GameServerResult{}, err
	}

	deadline := time.Now().Add(20 * time.Minute)
	reinstalled := false
	for !server.Installed {
		// A failed or abandoned installation (for example interrupted by a
		// node restart) is retried once; a running one is awaited.
		if !server.Installing {
			if reinstalled {
				return GameServerResult{}, errors.New("game server installation failed twice; inspect the server's install log in the Panel")
			}
			if err := p.api.reinstall(ctx, server); err != nil {
				return GameServerResult{}, fmt.Errorf("retry the failed installation: %w", err)
			}
			reinstalled = true
		}
		if time.Now().After(deadline) {
			return GameServerResult{}, errors.New("game server installation did not complete within 20 minutes")
		}
		time.Sleep(5 * time.Second)
		if server, err = p.api.refreshServer(ctx, server); err != nil {
			return GameServerResult{}, err
		}
	}

	// Servers read their properties only at start: stop a running server
	// gracefully before applying the curated defaults.
	if state, _ := p.api.state(ctx, server.Identifier); state != "offline" && state != "" {
		if err := p.api.power(ctx, server.Identifier, "stop"); err != nil {
			return GameServerResult{}, fmt.Errorf("stop the game server before applying defaults: %w", err)
		}
		if err := p.waitState(ctx, server.Identifier, "offline", 3*time.Minute); err != nil {
			return GameServerResult{}, err
		}
	}
	if err := p.applyDefaults(ctx, server.Identifier, profile, name); err != nil {
		return GameServerResult{}, err
	}
	if err := p.api.power(ctx, server.Identifier, "start"); err != nil {
		return GameServerResult{}, fmt.Errorf("start game server: %w", err)
	}
	if err := p.waitState(ctx, server.Identifier, "running", 8*time.Minute); err != nil {
		return GameServerResult{}, err
	}
	for _, player := range request.AllowList {
		if err := p.api.command(ctx, server.Identifier, fmt.Sprintf(profile.AllowCommand, player)); err != nil {
			return GameServerResult{}, fmt.Errorf("admit player %q: %w", player, err)
		}
	}
	if missing, err := p.verifyAllowList(ctx, server.Identifier, profile, request.AllowList); err != nil {
		return GameServerResult{}, err
	} else if len(missing) > 0 {
		return GameServerResult{}, fmt.Errorf("the game did not admit %s; check the exact %s and run setup again", strings.Join(missing, ", "), profile.AccountHint)
	}
	if profile.PasswordFile != "" {
		if err := p.verifyPasswordSet(ctx, server.Identifier, profile.PasswordFile); err != nil {
			return GameServerResult{}, err
		}
	}
	answer, err := observeGameProtocol(ctx, profile, request.Password, 5*time.Minute)
	if err != nil {
		return GameServerResult{}, err
	}
	return GameServerResult{ServerUUID: server.UUID, Identifier: server.Identifier, Port: profile.Port, Protocol: profile.Protocol, Reachable: answer, Created: created}, nil
}

func (p GamePanel) applyDefaults(ctx context.Context, identifier string, profile GameProfile, name string) error {
	if profile.WritesEULA {
		if err := p.api.writeFile(ctx, identifier, "/eula.txt", "eula=true\n"); err != nil {
			return fmt.Errorf("record the owner's EULA acceptance: %w", err)
		}
	}
	if profile.PropertiesFile == "" {
		return nil
	}
	// A server that has not started yet has no properties file; Panels
	// answer that with 404 or 500, and the curated keys are written anyway.
	current, _, _ := p.api.readFile(ctx, identifier, profile.PropertiesFile)
	values := map[string]string{profile.NameProperty: name}
	for key, value := range profile.Properties {
		values[key] = value
	}
	if err := p.api.writeFile(ctx, identifier, profile.PropertiesFile, mergeServerProperties(current, values)); err != nil {
		return fmt.Errorf("apply secure server defaults: %w", err)
	}
	return nil
}

func (p GamePanel) waitState(ctx context.Context, identifier, want string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		if state, err := p.api.state(ctx, identifier); err == nil && state == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("game server did not reach the %s state in time", want)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// verifyAllowList reads the server's own allow list back and returns every
// requested player it did not admit (for example an unknown account name).
func (p GamePanel) verifyAllowList(ctx context.Context, identifier string, profile GameProfile, players []string) ([]string, error) {
	if len(players) == 0 {
		return nil, nil
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		admitted := map[string]bool{}
		if data, found, err := p.api.readFile(ctx, identifier, profile.AllowListFile); err == nil && found {
			var entries []struct {
				Name string `json:"name"`
			}
			if json.Unmarshal([]byte(data), &entries) == nil {
				for _, entry := range entries {
					admitted[strings.ToLower(entry.Name)] = true
				}
			}
		}
		var missing []string
		for _, player := range players {
			if !admitted[strings.ToLower(player)] {
				missing = append(missing, player)
			}
		}
		if len(missing) == 0 || time.Now().After(deadline) {
			return missing, nil
		}
		time.Sleep(3 * time.Second)
	}
}

// verifyPasswordSet reads the rendered configuration back and fails unless a
// non-empty join password is in force. The value itself is never returned.
func (p GamePanel) verifyPasswordSet(ctx context.Context, identifier, file string) error {
	data, found, err := p.api.readFile(ctx, identifier, file)
	if err != nil || !found {
		return fmt.Errorf("read back the join password setting: %w", errors.Join(err, errors.New("file not found")))
	}
	for _, line := range strings.Split(data, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "password="); ok && value != "" {
			return nil
		}
	}
	return errors.New("the game server started without a join password; setup refuses an open server")
}

// OwnedGameServers lists the identifiers of every game server the owner's
// client key can operate, with its current power state.
func (p GamePanel) OwnedGameServers(ctx context.Context) (map[string]string, error) {
	servers, err := p.api.listServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list game servers: %w", err)
	}
	states := make(map[string]string, len(servers))
	for _, server := range servers {
		states[server.Identifier] = server.State
	}
	return states, nil
}

// StopGameServer asks the game to shut down with its own stop command (which
// saves the world) and waits until Wings reports it offline.
func (p GamePanel) StopGameServer(ctx context.Context, identifier string, limit time.Duration) error {
	if err := p.api.power(ctx, identifier, "stop"); err != nil {
		return fmt.Errorf("stop game server %s: %w", identifier, err)
	}
	return p.waitState(ctx, identifier, "offline", limit)
}

// StartGameServer sends the start signal without waiting for the world to load.
func (p GamePanel) StartGameServer(ctx context.Context, identifier string) error {
	if err := p.api.power(ctx, identifier, "start"); err != nil {
		return fmt.Errorf("start game server %s: %w", identifier, err)
	}
	return nil
}

// ListGameServers returns every game server the owner's client key operates.
func (p GamePanel) ListGameServers(ctx context.Context) ([]GameServerSummary, error) {
	servers, err := p.api.listServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list game servers: %w", err)
	}
	return servers, nil
}

// GameServerPower sends one power signal and, for start/stop/restart, waits
// for the resulting state so the caller reports an observed outcome.
func (p GamePanel) GameServerPower(ctx context.Context, identifier, signal string) (string, error) {
	want := map[string]string{"start": "running", "restart": "running", "stop": "offline"}[signal]
	if want == "" {
		return "", fmt.Errorf("power signal %q is not admitted; use start, stop or restart", signal)
	}
	if err := p.api.power(ctx, identifier, signal); err != nil {
		return "", fmt.Errorf("send %s to game server %s: %w", signal, identifier, err)
	}
	if err := p.waitState(ctx, identifier, want, 8*time.Minute); err != nil {
		return "", err
	}
	return want, nil
}

// AllowGamePlayer admits one player on a curated allow-list server and reads
// the game's own allow list back. Servers not created by StackKits are
// refused, because their admission command is unknown.
func (p GamePanel) AllowGamePlayer(ctx context.Context, identifier, player string) error {
	if !gamePlayerName.MatchString(player) {
		return fmt.Errorf("player name %q is not a valid game account name", player)
	}
	externalID, err := p.api.externalIDOf(ctx, identifier)
	if err != nil {
		return fmt.Errorf("look up the game server: %w", err)
	}
	var profile *GameProfile
	for id, candidate := range GameProfiles {
		if strings.HasPrefix(externalID, "stackkit-game-"+id+"-") {
			candidate := candidate
			profile = &candidate
		}
	}
	if profile == nil {
		return fmt.Errorf("game server %s was not created by stackkit setup game; manage its players in the Panel", identifier)
	}
	if profile.Access != "allow-list" {
		return fmt.Errorf("%s has no account allow list; players join with the server's join password", profile.DisplayName)
	}
	if err := p.api.command(ctx, identifier, fmt.Sprintf(profile.AllowCommand, player)); err != nil {
		return fmt.Errorf("admit player %q (the server must be running): %w", player, err)
	}
	missing, err := p.verifyAllowList(ctx, identifier, *profile, []string{player})
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return fmt.Errorf("the game did not admit %s; check the exact %s", player, profile.AccountHint)
	}
	return nil
}

// panelHTTP is the authenticated JSON transport shared by the platforms.
type panelHTTP struct {
	http      *http.Client
	baseURL   string
	setupKey  string
	clientKey string
}

// errPanelNotFound marks an HTTP 404 answer.
var errPanelNotFound = errors.New("not found")

func (c panelHTTP) do(ctx context.Context, method, path, key string, body any, contentType string, out any) error {
	var reader io.Reader
	if body != nil {
		switch value := body.(type) {
		case string:
			reader = strings.NewReader(value)
		default:
			data, err := json.Marshal(value)
			if err != nil {
				return err
			}
			reader = bytes.NewReader(data)
			contentType = "application/json"
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return err
	}
	if response.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s %s: %w", method, path, errPanelNotFound)
	}
	if response.StatusCode >= 300 {
		return fmt.Errorf("panel %s %s returned HTTP %d", method, path, response.StatusCode)
	}
	if out != nil && len(data) > 0 {
		if raw, ok := out.(*string); ok {
			*raw = string(data)
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode panel %s response: %w", path, err)
		}
	}
	return nil
}
