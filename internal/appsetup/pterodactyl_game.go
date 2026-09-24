package appsetup

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GameProfile is one curated game-server recipe (ADR-0043). Images are pinned
// by digest; secure defaults are edition-specific and never copied between
// editions.
type GameProfile struct {
	ID          string
	EggName     string
	NestName    string
	DockerImage string
	Port        int
	Protocol    string // "tcp" (Java Server List Ping) or "udp" (Bedrock RakNet)
	MemoryMB    int
	DiskMB      int
	CPUPercent  int
	Environment func(name string) map[string]string
	// Properties are applied to server.properties before first start.
	Properties map[string]string
	// AllowCommand is the console command that admits one player.
	AllowCommand string
	// WritesEULA reports whether the server reads eula.txt.
	WritesEULA bool
	// AllowListFile is where the server records admitted players.
	AllowListFile string
	// NameProperty carries the world name into server.properties.
	NameProperty string
}

// GameProfiles is the curated catalog. Adding a game is a reviewed change:
// Egg source, image digest, secure defaults, ports and resources.
var GameProfiles = map[string]GameProfile{
	"minecraft-java": {
		ID: "minecraft-java", EggName: "Vanilla Minecraft", NestName: "Minecraft",
		DockerImage: "ghcr.io/pterodactyl/yolks:java_25@sha256:bc301e4696f5c4fc1bb21214f960ebdfd0ae05ffceaf5cd68511bf6e493757c3",
		Port:        25565, Protocol: "tcp", MemoryMB: 2048, DiskMB: 10240, CPUPercent: 200,
		Environment: func(string) map[string]string {
			return map[string]string{"SERVER_JARFILE": "server.jar", "VANILLA_VERSION": "latest"}
		},
		Properties: map[string]string{
			"online-mode": "true", "white-list": "true", "enforce-whitelist": "true", "enable-rcon": "false",
			"enable-query": "false", "difficulty": "normal", "max-players": "10", "server-port": "25565",
		},
		AllowCommand: "whitelist add %s", WritesEULA: true, AllowListFile: "/whitelist.json", NameProperty: "motd",
	},
	"minecraft-bedrock": {
		ID: "minecraft-bedrock", EggName: "Vanilla Bedrock", NestName: "Minecraft",
		DockerImage: "ghcr.io/ptero-eggs/yolks:debian@sha256:d7b83c285632be9c9dd03b909378200c4f4c664035d72ad4b5b694af03dc75ac",
		Port:        19132, Protocol: "udp", MemoryMB: 1536, DiskMB: 8192, CPUPercent: 200,
		Environment: func(name string) map[string]string {
			return map[string]string{
				"BEDROCK_VERSION": "latest", "LD_LIBRARY_PATH": ".", "SERVERNAME": name,
				"GAMEMODE": "survival", "DIFFICULTY": "normal", "CHEATS": "false",
			}
		},
		// Bedrock 1.26 defaults to NetherNet; direct IP joins need RakNet.
		Properties: map[string]string{
			"online-mode": "true", "allow-list": "true", "transport": "raknet", "server-port": "19132",
			"max-players": "10", "default-player-permission-level": "member",
		},
		AllowCommand: "allowlist add %s", WritesEULA: false, AllowListFile: "/allowlist.json", NameProperty: "server-name",
	},
}

// GameServerRequest is the owner's private setup input.
type GameServerRequest struct {
	Profile         string
	Name            string
	AcceptEULA      bool
	AllowList       []string
	OwnerEmail      string
	ApplicationKey  string
	ClientKey       string
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

var gamePlayerName = regexp.MustCompile(`^[A-Za-z0-9_ .-]{1,32}$`)

type pterodactylClient struct {
	http    *http.Client
	baseURL string
	appKey  string
	userKey string
}

func (c pterodactylClient) do(ctx context.Context, method, path, key string, body any, contentType string, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		switch value := body.(type) {
		case string:
			reader = strings.NewReader(value)
		default:
			data, err := json.Marshal(value)
			if err != nil {
				return 0, err
			}
			reader = bytes.NewReader(data)
			contentType = "application/json"
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.baseURL, "/")+path, reader)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return response.StatusCode, err
	}
	if response.StatusCode >= 300 {
		return response.StatusCode, fmt.Errorf("pterodactyl %s %s returned HTTP %d", method, path, response.StatusCode)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return response.StatusCode, fmt.Errorf("decode pterodactyl %s response: %w", path, err)
		}
	}
	return response.StatusCode, nil
}

type pterodactylList[T any] struct {
	Data []struct {
		Attributes T `json:"attributes"`
	} `json:"data"`
}

type pterodactylServer struct {
	ID         int    `json:"id"`
	UUID       string `json:"uuid"`
	Identifier string `json:"identifier"`
	Status     string `json:"status"`
	Container  struct {
		Installed any `json:"installed"`
	} `json:"container"`
}

func (s pterodactylServer) installed() bool {
	switch value := s.Container.Installed.(type) {
	case bool:
		return value
	case float64:
		return value == 1
	}
	return false
}

// CreatePterodactylGameServer creates (or reconciles) exactly one curated game
// server, applies its secure defaults, admits the requested players, starts
// it and observes a real game-protocol answer on the node.
//
//nolint:gocyclo // One owner-approved action converges a complete server.
func CreatePterodactylGameServer(ctx context.Context, client *http.Client, baseURL string, request GameServerRequest) (GameServerResult, error) {
	profile, ok := GameProfiles[request.Profile]
	if !ok {
		return GameServerResult{}, fmt.Errorf("unknown game profile %q; choose minecraft-java or minecraft-bedrock", request.Profile)
	}
	if !request.AcceptEULA {
		return GameServerResult{}, errors.New("creating a game server requires the owner's own acceptance of the game's EULA (acceptEula: true)")
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = "Family " + map[string]string{"minecraft-java": "Survival", "minecraft-bedrock": "Bedrock"}[profile.ID]
	}
	if len(name) > 64 || strings.ContainsAny(name, "\r\n") {
		return GameServerResult{}, errors.New("server name must be a single line of at most 64 characters")
	}
	for _, player := range request.AllowList {
		if !gamePlayerName.MatchString(player) {
			return GameServerResult{}, fmt.Errorf("player name %q is not a valid game account name", player)
		}
	}
	c := pterodactylClient{http: client, baseURL: baseURL, appKey: request.ApplicationKey, userKey: request.ClientKey}
	externalID := "stackkit-game-" + profile.ID + "-" + slugGameName(name)

	var existing struct {
		Attributes pterodactylServer `json:"attributes"`
	}
	status, err := c.do(ctx, http.MethodGet, "/api/application/servers/external/"+url.PathEscape(externalID), c.appKey, nil, "", &existing)
	created := false
	var server pterodactylServer
	switch {
	case err == nil:
		server = existing.Attributes
	case status == http.StatusNotFound:
		server, err = c.createServer(ctx, profile, name, externalID, request.OwnerEmail)
		if err != nil {
			return GameServerResult{}, err
		}
		created = true
	default:
		return GameServerResult{}, fmt.Errorf("look up existing game server: %w", err)
	}

	deadline := time.Now().Add(10 * time.Minute)
	reinstalled := false
	for !server.installed() {
		// A failed or abandoned installation (for example interrupted by a
		// node restart) is retried once; a running one is awaited.
		if server.Status != "installing" {
			if reinstalled {
				return GameServerResult{}, errors.New("game server installation failed twice; inspect the server's install log in the Panel")
			}
			if _, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/application/servers/%d/reinstall", server.ID), c.appKey, nil, "", nil); err != nil {
				return GameServerResult{}, fmt.Errorf("retry the failed installation: %w", err)
			}
			reinstalled = true
		}
		if time.Now().After(deadline) {
			return GameServerResult{}, errors.New("game server installation did not complete within 10 minutes")
		}
		time.Sleep(5 * time.Second)
		var current struct {
			Attributes pterodactylServer `json:"attributes"`
		}
		if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/application/servers/%d", server.ID), c.appKey, nil, "", &current); err != nil {
			return GameServerResult{}, err
		}
		server = current.Attributes
	}

	// Servers read their properties only at start: stop a running server
	// gracefully before applying the curated defaults.
	if state, _ := c.currentState(ctx, server.Identifier); state != "offline" && state != "" {
		if _, err := c.do(ctx, http.MethodPost, "/api/client/servers/"+server.Identifier+"/power", c.userKey, map[string]string{"signal": "stop"}, "", nil); err != nil {
			return GameServerResult{}, fmt.Errorf("stop the game server before applying defaults: %w", err)
		}
		if err := c.waitState(ctx, server.Identifier, "offline", 3*time.Minute); err != nil {
			return GameServerResult{}, err
		}
	}
	if err := c.applyDefaults(ctx, server.Identifier, profile, name); err != nil {
		return GameServerResult{}, err
	}
	if _, err := c.do(ctx, http.MethodPost, "/api/client/servers/"+server.Identifier+"/power", c.userKey, map[string]string{"signal": "start"}, "", nil); err != nil {
		return GameServerResult{}, fmt.Errorf("start game server: %w", err)
	}
	if err := c.waitState(ctx, server.Identifier, "running", 8*time.Minute); err != nil {
		return GameServerResult{}, err
	}
	for _, player := range request.AllowList {
		command := fmt.Sprintf(profile.AllowCommand, player)
		if _, err := c.do(ctx, http.MethodPost, "/api/client/servers/"+server.Identifier+"/command", c.userKey, map[string]string{"command": command}, "", nil); err != nil {
			return GameServerResult{}, fmt.Errorf("admit player %q: %w", player, err)
		}
	}
	if missing, err := c.verifyAllowList(ctx, server.Identifier, profile, request.AllowList); err != nil {
		return GameServerResult{}, err
	} else if len(missing) > 0 {
		return GameServerResult{}, fmt.Errorf("the game did not admit %s; check the exact account names (Java uses Minecraft account names, Bedrock uses Xbox gamertags) and run setup again", strings.Join(missing, ", "))
	}
	answer, err := observeGameProtocol(ctx, profile, 3*time.Minute)
	if err != nil {
		return GameServerResult{}, err
	}
	return GameServerResult{ServerUUID: server.UUID, Identifier: server.Identifier, Port: profile.Port, Protocol: profile.Protocol, Reachable: answer, Created: created}, nil
}

func (c pterodactylClient) createServer(ctx context.Context, profile GameProfile, name, externalID, ownerEmail string) (pterodactylServer, error) {
	var users pterodactylList[struct {
		ID    int    `json:"id"`
		Email string `json:"email"`
	}]
	if _, err := c.do(ctx, http.MethodGet, "/api/application/users?filter[email]="+url.QueryEscape(ownerEmail), c.appKey, nil, "", &users); err != nil {
		return pterodactylServer{}, fmt.Errorf("find the Panel owner: %w", err)
	}
	userID := 0
	for _, user := range users.Data {
		if strings.EqualFold(user.Attributes.Email, ownerEmail) {
			userID = user.Attributes.ID
		}
	}
	if userID == 0 {
		return pterodactylServer{}, errors.New("the Panel owner account is missing; re-run apply so the bootstrap converges")
	}
	eggID, startup, err := c.findEgg(ctx, profile)
	if err != nil {
		return pterodactylServer{}, err
	}
	allocationID, err := c.freeAllocation(ctx, profile.Port)
	if err != nil {
		return pterodactylServer{}, err
	}
	body := map[string]any{
		"name": name, "user": userID, "egg": eggID, "docker_image": profile.DockerImage, "startup": startup,
		"environment":    profile.Environment(name),
		"limits":         map[string]int{"memory": profile.MemoryMB, "swap": 0, "disk": profile.DiskMB, "io": 500, "cpu": profile.CPUPercent},
		"feature_limits": map[string]int{"databases": 0, "allocations": 1, "backups": 3},
		"allocation":     map[string]int{"default": allocationID},
		"external_id":    externalID, "start_on_completion": false,
	}
	var createdServer struct {
		Attributes pterodactylServer `json:"attributes"`
	}
	if _, err := c.do(ctx, http.MethodPost, "/api/application/servers", c.appKey, body, "", &createdServer); err != nil {
		return pterodactylServer{}, fmt.Errorf("create game server: %w", err)
	}
	return createdServer.Attributes, nil
}

func (c pterodactylClient) findEgg(ctx context.Context, profile GameProfile) (int, string, error) {
	var nests pterodactylList[struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}]
	if _, err := c.do(ctx, http.MethodGet, "/api/application/nests", c.appKey, nil, "", &nests); err != nil {
		return 0, "", err
	}
	for _, nest := range nests.Data {
		if nest.Attributes.Name != profile.NestName {
			continue
		}
		var eggs pterodactylList[struct {
			ID      int    `json:"id"`
			Name    string `json:"name"`
			Startup string `json:"startup"`
		}]
		if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/application/nests/%d/eggs", nest.Attributes.ID), c.appKey, nil, "", &eggs); err != nil {
			return 0, "", err
		}
		for _, egg := range eggs.Data {
			if egg.Attributes.Name == profile.EggName {
				return egg.Attributes.ID, egg.Attributes.Startup, nil
			}
		}
	}
	return 0, "", fmt.Errorf("curated Egg %q is not installed; re-run apply so the bootstrap imports it", profile.EggName)
}

func (c pterodactylClient) freeAllocation(ctx context.Context, port int) (int, error) {
	var nodes pterodactylList[struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}]
	if _, err := c.do(ctx, http.MethodGet, "/api/application/nodes", c.appKey, nil, "", &nodes); err != nil {
		return 0, err
	}
	for _, node := range nodes.Data {
		if node.Attributes.Name != "stackkit-node" {
			continue
		}
		var allocations pterodactylList[struct {
			ID       int  `json:"id"`
			Port     int  `json:"port"`
			Assigned bool `json:"assigned"`
		}]
		if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/application/nodes/%d/allocations?per_page=100", node.Attributes.ID), c.appKey, nil, "", &allocations); err != nil {
			return 0, err
		}
		for _, allocation := range allocations.Data {
			if allocation.Attributes.Port == port && !allocation.Attributes.Assigned {
				return allocation.Attributes.ID, nil
			}
		}
		return 0, fmt.Errorf("port %d is already used by another game server on this node", port)
	}
	return 0, errors.New("the StackKits game node is missing; re-run apply so the bootstrap converges")
}

func (c pterodactylClient) applyDefaults(ctx context.Context, identifier string, profile GameProfile, name string) error {
	if profile.WritesEULA {
		if _, err := c.do(ctx, http.MethodPost, "/api/client/servers/"+identifier+"/files/write?file=%2Feula.txt", c.userKey, "eula=true\n", "text/plain", nil); err != nil {
			return fmt.Errorf("record the owner's EULA acceptance: %w", err)
		}
	}
	current := ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.baseURL, "/")+"/api/client/servers/"+identifier+"/files/contents?file=%2Fserver.properties", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.userKey)
	if response, err := c.http.Do(request); err == nil {
		if response.StatusCode == http.StatusOK {
			data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			current = string(data)
		}
		response.Body.Close()
	}
	values := map[string]string{profile.NameProperty: name}
	for key, value := range profile.Properties {
		values[key] = value
	}
	updated := mergeServerProperties(current, values)
	if _, err := c.do(ctx, http.MethodPost, "/api/client/servers/"+identifier+"/files/write?file=%2Fserver.properties", c.userKey, updated, "text/plain", nil); err != nil {
		return fmt.Errorf("apply secure server defaults: %w", err)
	}
	return nil
}

func (c pterodactylClient) currentState(ctx context.Context, identifier string) (string, error) {
	var resources struct {
		Attributes struct {
			State string `json:"current_state"`
		} `json:"attributes"`
	}
	_, err := c.do(ctx, http.MethodGet, "/api/client/servers/"+identifier+"/resources", c.userKey, nil, "", &resources)
	return resources.Attributes.State, err
}

func (c pterodactylClient) waitState(ctx context.Context, identifier, want string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		if state, err := c.currentState(ctx, identifier); err == nil && state == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("game server did not reach the %s state in time", want)
		}
		time.Sleep(5 * time.Second)
	}
}

// mergeServerProperties sets the curated keys and keeps every other line.
func mergeServerProperties(current string, values map[string]string) string {
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(current, "\r\n", "\n"), "\n") {
		key, _, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if found && !strings.HasPrefix(key, "#") {
			if value, ok := values[key]; ok {
				out = append(out, key+"="+value)
				seen[key] = true
				continue
			}
		}
		if line != "" || len(out) > 0 {
			out = append(out, line)
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sortStrings(keys)
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func slugGameName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 40 {
		slug = slug[:40]
	}
	if slug == "" {
		slug = "world"
	}
	return slug
}

// observeGameProtocol asks the published node port with the game's own
// discovery protocol, as a client on the node would.
func observeGameProtocol(ctx context.Context, profile GameProfile, limit time.Duration) (string, error) {
	deadline := time.Now().Add(limit)
	var lastErr error
	for time.Now().Before(deadline) {
		var answer string
		var err error
		if profile.Protocol == "tcp" {
			answer, err = javaServerListPing("127.0.0.1", profile.Port)
		} else {
			answer, err = bedrockRakNetPing("127.0.0.1", profile.Port)
		}
		if err == nil {
			return answer, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return "", fmt.Errorf("game port %d/%s did not answer its discovery protocol: %w", profile.Port, profile.Protocol, lastErr)
}

func javaServerListPing(host string, port int) (string, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	varint := func(n int) []byte {
		var out []byte
		for {
			b := byte(n & 0x7f)
			n >>= 7
			if n != 0 {
				b |= 0x80
			}
			out = append(out, b)
			if n == 0 {
				return out
			}
		}
	}
	handshake := append(varint(0), varint(767)...)
	handshake = append(handshake, varint(len(host))...)
	handshake = append(handshake, host...)
	handshake = binary.BigEndian.AppendUint16(handshake, uint16(port))
	handshake = append(handshake, varint(1)...)
	packet := append(varint(len(handshake)), handshake...)
	packet = append(packet, varint(1)...)
	packet = append(packet, varint(0)...)
	if _, err := conn.Write(packet); err != nil {
		return "", err
	}
	readVarint := func() (int, error) {
		value := 0
		for i := 0; i < 5; i++ {
			var b [1]byte
			if _, err := io.ReadFull(conn, b[:]); err != nil {
				return 0, err
			}
			value |= int(b[0]&0x7f) << (7 * i)
			if b[0]&0x80 == 0 {
				return value, nil
			}
		}
		return 0, errors.New("invalid varint")
	}
	if _, err := readVarint(); err != nil {
		return "", err
	}
	if _, err := readVarint(); err != nil {
		return "", err
	}
	length, err := readVarint()
	if err != nil || length <= 0 || length > 1<<20 {
		return "", errors.New("invalid status length")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return "", err
	}
	var status struct {
		Version struct {
			Name string `json:"name"`
		} `json:"version"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return "", err
	}
	return "minecraft-java " + status.Version.Name, nil
}

func bedrockRakNetPing(host string, port int) (string, error) {
	conn, err := net.DialTimeout("udp", net.JoinHostPort(host, strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	magic := []byte{0x00, 0xff, 0xff, 0x00, 0xfe, 0xfe, 0xfe, 0xfe, 0xfd, 0xfd, 0xfd, 0xfd, 0x12, 0x34, 0x56, 0x78}
	guid := make([]byte, 8)
	_, _ = rand.Read(guid)
	packet := []byte{0x01}
	packet = binary.BigEndian.AppendUint64(packet, uint64(time.Now().UnixMilli()))
	packet = append(packet, magic...)
	packet = append(packet, guid...)
	if _, err := conn.Write(packet); err != nil {
		return "", err
	}
	buffer := make([]byte, 2048)
	n, err := conn.Read(buffer)
	if err != nil {
		return "", err
	}
	if n < 35 || buffer[0] != 0x1c {
		return "", errors.New("unexpected RakNet answer")
	}
	length := int(binary.BigEndian.Uint16(buffer[33:35]))
	if 35+length > n {
		return "", errors.New("truncated RakNet answer")
	}
	fields := strings.Split(string(buffer[35:35+length]), ";")
	if len(fields) < 4 || fields[0] != "MCPE" {
		return "", errors.New("RakNet answer is not a Bedrock server")
	}
	return "minecraft-bedrock " + fields[3], nil
}

// verifyAllowList reads the server's own allow list back and returns every
// requested player it did not admit (for example an unknown account name).
func (c pterodactylClient) verifyAllowList(ctx context.Context, identifier string, profile GameProfile, players []string) ([]string, error) {
	if len(players) == 0 {
		return nil, nil
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		admitted := map[string]bool{}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.baseURL, "/")+"/api/client/servers/"+identifier+"/files/contents?file="+url.QueryEscape(profile.AllowListFile), nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "Bearer "+c.userKey)
		if response, err := c.http.Do(request); err == nil {
			if response.StatusCode == http.StatusOK {
				var entries []struct {
					Name string `json:"name"`
				}
				if data, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20)); readErr == nil && json.Unmarshal(data, &entries) == nil {
					for _, entry := range entries {
						admitted[strings.ToLower(entry.Name)] = true
					}
				}
			}
			response.Body.Close()
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
