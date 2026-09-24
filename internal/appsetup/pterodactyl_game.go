package appsetup

import (
	"bufio"
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
// by digest; secure defaults are game-specific and never copied between games.
type GameProfile struct {
	ID          string
	DisplayName string
	DefaultName string
	EggName     string
	NestName    string
	DockerImage string
	Port        int
	// ExtraPorts are further node ports the game needs (for example a query port).
	ExtraPorts []int
	Protocol   string // transport of Port: "tcp" or "udp"
	MemoryMB   int
	DiskMB     int
	CPUPercent int
	// Environment overlays the Egg's own variable defaults.
	Environment func(name, password string) map[string]string
	// PropertiesFile receives Properties before first start; empty when the
	// Egg renders its configuration from the environment.
	PropertiesFile string
	Properties     map[string]string
	// NameProperty carries the world name into PropertiesFile.
	NameProperty string
	// Access is "allow-list" (named accounts) or "password" (owner-chosen
	// join password shared with friends).
	Access string
	// AllowCommand is the console command that admits one player.
	AllowCommand string
	// AllowListFile is where the server records admitted players.
	AllowListFile string
	// WritesEULA reports whether the server reads eula.txt.
	WritesEULA bool
	// AccountHint names the account kind players are admitted by.
	AccountHint string
	// PasswordFile is where the Egg renders the join password as a
	// "password=" line, read back after start.
	PasswordFile string
	// Probe performs the first steps of a real client join on the node with
	// the game's own protocol; password is the owner's join password, if any.
	Probe     func(host string, port int, password string) (string, error)
	ProbePort int
}

var minecraftJavaProperties = map[string]string{
	"online-mode": "true", "white-list": "true", "enforce-whitelist": "true", "enable-rcon": "false",
	"enable-query": "false", "difficulty": "normal", "max-players": "10", "server-port": "25565",
}

// GameProfiles is the curated catalog. Adding a game is a reviewed change:
// Egg source, image digest, secure defaults, ports, resources and a probe.
var GameProfiles = map[string]GameProfile{
	"minecraft-java": {
		ID: "minecraft-java", DisplayName: "Minecraft Java (Vanilla)", DefaultName: "Family Survival",
		EggName: "Vanilla Minecraft", NestName: "Minecraft",
		DockerImage: "ghcr.io/pterodactyl/yolks:java_25@sha256:bc301e4696f5c4fc1bb21214f960ebdfd0ae05ffceaf5cd68511bf6e493757c3",
		Port:        25565, Protocol: "tcp", MemoryMB: 2048, DiskMB: 10240, CPUPercent: 200,
		Environment: func(string, string) map[string]string {
			return map[string]string{"SERVER_JARFILE": "server.jar", "VANILLA_VERSION": "latest"}
		},
		PropertiesFile: "/server.properties", Properties: minecraftJavaProperties, NameProperty: "motd",
		Access: "allow-list", AllowCommand: "whitelist add %s", AllowListFile: "/whitelist.json", WritesEULA: true,
		AccountHint: "Minecraft account names", Probe: javaJoinCheck, ProbePort: 25565,
	},
	// Paper is the performance fork most family servers run; it keeps the
	// vanilla protocol, so Java clients join without mods.
	"minecraft-paper": {
		ID: "minecraft-paper", DisplayName: "Minecraft Java (Paper)", DefaultName: "Family Paper",
		EggName: "Paper", NestName: "Minecraft",
		DockerImage: "ghcr.io/pterodactyl/yolks:java_25@sha256:bc301e4696f5c4fc1bb21214f960ebdfd0ae05ffceaf5cd68511bf6e493757c3",
		Port:        25566, Protocol: "tcp", MemoryMB: 3072, DiskMB: 10240, CPUPercent: 200,
		Environment: func(string, string) map[string]string {
			return map[string]string{"SERVER_JARFILE": "server.jar", "MINECRAFT_VERSION": "latest", "BUILD_NUMBER": "latest"}
		},
		PropertiesFile: "/server.properties", Properties: withProperty(minecraftJavaProperties, "server-port", "25566"), NameProperty: "motd",
		Access: "allow-list", AllowCommand: "whitelist add %s", AllowListFile: "/whitelist.json", WritesEULA: true,
		AccountHint: "Minecraft account names", Probe: javaJoinCheck, ProbePort: 25566,
	},
	"minecraft-bedrock": {
		ID: "minecraft-bedrock", DisplayName: "Minecraft Bedrock", DefaultName: "Family Bedrock",
		EggName: "Vanilla Bedrock", NestName: "Minecraft",
		DockerImage: "ghcr.io/ptero-eggs/yolks:debian@sha256:d7b83c285632be9c9dd03b909378200c4f4c664035d72ad4b5b694af03dc75ac",
		Port:        19132, Protocol: "udp", MemoryMB: 1536, DiskMB: 8192, CPUPercent: 200,
		Environment: func(name, _ string) map[string]string {
			return map[string]string{
				"BEDROCK_VERSION": "latest", "LD_LIBRARY_PATH": ".", "SERVERNAME": name,
				"GAMEMODE": "survival", "DIFFICULTY": "normal", "CHEATS": "false",
			}
		},
		// Bedrock 1.26 defaults to NetherNet; direct IP joins need RakNet.
		PropertiesFile: "/server.properties",
		Properties: map[string]string{
			"online-mode": "true", "allow-list": "true", "transport": "raknet", "server-port": "19132",
			"max-players": "10", "default-player-permission-level": "member",
		},
		NameProperty: "server-name",
		Access:       "allow-list", AllowCommand: "allowlist add %s", AllowListFile: "/allowlist.json",
		AccountHint: "Xbox gamertags", Probe: bedrockJoinCheck, ProbePort: 19132,
	},
	// Terraria has no account-based allow list; a join password is required.
	"terraria": {
		ID: "terraria", DisplayName: "Terraria", DefaultName: "Family Terraria",
		EggName: "Terraria Vanilla", NestName: "Terraria",
		DockerImage: "ghcr.io/ptero-eggs/yolks:debian@sha256:d7b83c285632be9c9dd03b909378200c4f4c664035d72ad4b5b694af03dc75ac",
		Port:        7777, Protocol: "tcp", MemoryMB: 1536, DiskMB: 4096, CPUPercent: 200,
		Environment: func(name, password string) map[string]string {
			return map[string]string{
				"TERRARIA_VERSION": "latest", "WORLD_NAME": worldSlug(name, 20), "MAX_PLAYERS": "8",
				"WORLD_SIZE": "2", "WORLD_DIFFICULTY": "0", "SERVER_MOTD": name, "PASSWORD": password,
			}
		},
		Access: "password", PasswordFile: "/serverconfig.txt", Probe: terrariaHandshake, ProbePort: 7777,
	},
	// Valheim stays off the public server list and off crossplay relays;
	// friends join by address and password. Port+1 is Steam's query port.
	"valheim": {
		ID: "valheim", DisplayName: "Valheim", DefaultName: "Family Valheim",
		EggName: "Valheim", NestName: "Valheim",
		DockerImage: "ghcr.io/ptero-eggs/games:valheim@sha256:5ade5bcfc0c00ad26fd1bac8ff35b6e5e48d8c6cb70ddb5ba72f448520956e24",
		Port:        2456, ExtraPorts: []int{2457}, Protocol: "udp", MemoryMB: 4096, DiskMB: 10240, CPUPercent: 300,
		Environment: func(name, password string) map[string]string {
			return map[string]string{
				"SERVER_NAME": name, "WORLD": worldSlug(name, 20), "PASSWORD": password,
				"PUBLIC_SERVER": "0", "ENABLE_CROSSPLAY": "0", "AUTO_UPDATE": "1",
			}
		},
		Access: "password", Probe: steamNetworkingChallenge, ProbePort: 2456,
	},
}

func withProperty(base map[string]string, key, value string) map[string]string {
	out := make(map[string]string, len(base))
	for k, v := range base {
		out[k] = v
	}
	out[key] = value
	return out
}

// GameProfileIDs lists the curated profiles in a stable order.
func GameProfileIDs() []string {
	return []string{"minecraft-java", "minecraft-paper", "minecraft-bedrock", "terraria", "valheim"}
}

// GameServerRequest is the owner's private setup input.
type GameServerRequest struct {
	Profile    string
	Name       string
	AcceptEULA bool
	AllowList  []string
	// Password is the owner-chosen join password for password-access games.
	Password        string
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
		server, err = c.createServer(ctx, profile, name, request.Password, externalID, request.OwnerEmail)
		if err != nil {
			return GameServerResult{}, err
		}
		created = true
	default:
		return GameServerResult{}, fmt.Errorf("look up existing game server: %w", err)
	}

	if !created {
		// Converge the curated environment (for example a changed join
		// password) on an existing server before it restarts.
		if err := c.updateStartup(ctx, server.ID, profile, name, request.Password); err != nil {
			return GameServerResult{}, err
		}
	}

	deadline := time.Now().Add(20 * time.Minute)
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
			return GameServerResult{}, errors.New("game server installation did not complete within 20 minutes")
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
		return GameServerResult{}, fmt.Errorf("the game did not admit %s; check the exact %s and run setup again", strings.Join(missing, ", "), profile.AccountHint)
	}
	if profile.PasswordFile != "" {
		if err := c.verifyPasswordSet(ctx, server.Identifier, profile.PasswordFile); err != nil {
			return GameServerResult{}, err
		}
	}
	answer, err := observeGameProtocol(ctx, profile, request.Password, 5*time.Minute)
	if err != nil {
		return GameServerResult{}, err
	}
	return GameServerResult{ServerUUID: server.UUID, Identifier: server.Identifier, Port: profile.Port, Protocol: profile.Protocol, Reachable: answer, Created: created}, nil
}

func (c pterodactylClient) createServer(ctx context.Context, profile GameProfile, name, password, externalID, ownerEmail string) (pterodactylServer, error) {
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
	egg, err := c.findEgg(ctx, profile)
	if err != nil {
		return pterodactylServer{}, err
	}
	allocations, err := c.freeAllocations(ctx, profile, append([]int{profile.Port}, profile.ExtraPorts...))
	if err != nil {
		return pterodactylServer{}, err
	}
	body := map[string]any{
		"name": name, "user": userID, "egg": egg.ID, "docker_image": profile.DockerImage, "startup": egg.Startup,
		"environment":    egg.environment(profile.Environment(name, password)),
		"limits":         map[string]int{"memory": profile.MemoryMB, "swap": 0, "disk": profile.DiskMB, "io": 500, "cpu": profile.CPUPercent},
		"feature_limits": map[string]int{"databases": 0, "allocations": len(allocations), "backups": 3},
		"allocation":     map[string]any{"default": allocations[0], "additional": allocations[1:]},
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

type pterodactylEgg struct {
	ID        int
	Startup   string
	Variables map[string]string
}

// environment starts from the Egg's own defaults so every variable the Panel
// validates is present, then applies the curated profile values.
func (egg pterodactylEgg) environment(overlay map[string]string) map[string]string {
	out := make(map[string]string, len(egg.Variables)+len(overlay))
	for key, value := range egg.Variables {
		out[key] = value
	}
	for key, value := range overlay {
		out[key] = value
	}
	return out
}

func (c pterodactylClient) findEgg(ctx context.Context, profile GameProfile) (pterodactylEgg, error) {
	var nests pterodactylList[struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}]
	if _, err := c.do(ctx, http.MethodGet, "/api/application/nests?per_page=100", c.appKey, nil, "", &nests); err != nil {
		return pterodactylEgg{}, err
	}
	for _, nest := range nests.Data {
		if nest.Attributes.Name != profile.NestName {
			continue
		}
		var eggs pterodactylList[struct {
			ID            int    `json:"id"`
			Name          string `json:"name"`
			Startup       string `json:"startup"`
			Relationships struct {
				Variables pterodactylList[struct {
					EnvVariable  string `json:"env_variable"`
					DefaultValue string `json:"default_value"`
				}] `json:"variables"`
			} `json:"relationships"`
		}]
		if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/application/nests/%d/eggs?include=variables", nest.Attributes.ID), c.appKey, nil, "", &eggs); err != nil {
			return pterodactylEgg{}, err
		}
		for _, egg := range eggs.Data {
			if egg.Attributes.Name != profile.EggName {
				continue
			}
			variables := map[string]string{}
			for _, variable := range egg.Attributes.Relationships.Variables.Data {
				variables[variable.Attributes.EnvVariable] = variable.Attributes.DefaultValue
			}
			return pterodactylEgg{ID: egg.Attributes.ID, Startup: egg.Attributes.Startup, Variables: variables}, nil
		}
	}
	return pterodactylEgg{}, fmt.Errorf("curated Egg %q is not installed; re-run apply so the bootstrap imports it", profile.EggName)
}

func (c pterodactylClient) updateStartup(ctx context.Context, serverID int, profile GameProfile, name, password string) error {
	egg, err := c.findEgg(ctx, profile)
	if err != nil {
		return err
	}
	body := map[string]any{
		"startup": egg.Startup, "egg": egg.ID, "image": profile.DockerImage, "skip_scripts": false,
		"environment": egg.environment(profile.Environment(name, password)),
	}
	if _, err := c.do(ctx, http.MethodPatch, fmt.Sprintf("/api/application/servers/%d/startup", serverID), c.appKey, body, "", nil); err != nil {
		return fmt.Errorf("converge the curated game settings: %w", err)
	}
	return nil
}

// freeAllocations reserves the profile's ports and refuses a server the node
// cannot hold next to the existing ones: the Panel enforces node capacity
// only for automatic deployment, not for an explicit allocation.
func (c pterodactylClient) freeAllocations(ctx context.Context, profile GameProfile, ports []int) ([]int, error) {
	var nodes pterodactylList[struct {
		ID                 int    `json:"id"`
		Name               string `json:"name"`
		Memory             int    `json:"memory"`
		AllocatedResources struct {
			Memory int `json:"memory"`
		} `json:"allocated_resources"`
	}]
	if _, err := c.do(ctx, http.MethodGet, "/api/application/nodes", c.appKey, nil, "", &nodes); err != nil {
		return nil, err
	}
	for _, node := range nodes.Data {
		if node.Attributes.Name != "stackkit-node" {
			continue
		}
		if free := node.Attributes.Memory - node.Attributes.AllocatedResources.Memory; profile.MemoryMB > free {
			return nil, fmt.Errorf("the game node has %d MB of game memory left and %s needs %d MB; delete an unused server in the Panel or add memory to the node", max(free, 0), profile.DisplayName, profile.MemoryMB)
		}
		var allocations pterodactylList[struct {
			ID       int  `json:"id"`
			Port     int  `json:"port"`
			Assigned bool `json:"assigned"`
		}]
		if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/application/nodes/%d/allocations?per_page=500", node.Attributes.ID), c.appKey, nil, "", &allocations); err != nil {
			return nil, err
		}
		ids := make([]int, 0, len(ports))
		for _, port := range ports {
			id := 0
			for _, allocation := range allocations.Data {
				if allocation.Attributes.Port == port {
					if allocation.Attributes.Assigned {
						return nil, fmt.Errorf("port %d is already used by another game server on this node", port)
					}
					id = allocation.Attributes.ID
				}
			}
			if id == 0 {
				return nil, fmt.Errorf("port %d is not offered by the StackKits game node; re-run apply so the bootstrap converges", port)
			}
			ids = append(ids, id)
		}
		return ids, nil
	}
	return nil, errors.New("the StackKits game node is missing; re-run apply so the bootstrap converges")
}

func (c pterodactylClient) applyDefaults(ctx context.Context, identifier string, profile GameProfile, name string) error {
	if profile.WritesEULA {
		if _, err := c.do(ctx, http.MethodPost, "/api/client/servers/"+identifier+"/files/write?file=%2Feula.txt", c.userKey, "eula=true\n", "text/plain", nil); err != nil {
			return fmt.Errorf("record the owner's EULA acceptance: %w", err)
		}
	}
	if profile.PropertiesFile == "" {
		return nil
	}
	file := url.QueryEscape(profile.PropertiesFile)
	current := ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.baseURL, "/")+"/api/client/servers/"+identifier+"/files/contents?file="+file, nil)
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
	if _, err := c.do(ctx, http.MethodPost, "/api/client/servers/"+identifier+"/files/write?file="+file, c.userKey, updated, "text/plain", nil); err != nil {
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
func observeGameProtocol(ctx context.Context, profile GameProfile, password string, limit time.Duration) (string, error) {
	deadline := time.Now().Add(limit)
	var lastErr error
	for time.Now().Before(deadline) {
		answer, err := profile.Probe("127.0.0.1", profile.ProbePort, password)
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
	return "", fmt.Errorf("game port %d did not answer its discovery protocol: %w", profile.ProbePort, lastErr)
}

// javaJoinCheck reads the server status, then starts a login as a joining
// player: an online-mode server must answer with an encryption request, the
// step that hands the join to Microsoft account authentication.
func javaJoinCheck(host string, port int, _ string) (string, error) {
	version, protocol, err := javaServerListPing(host, port)
	if err != nil {
		return "", err
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	handshake := append(minecraftVarint(0), minecraftVarint(protocol)...)
	handshake = append(handshake, minecraftVarint(len(host))...)
	handshake = append(handshake, host...)
	handshake = binary.BigEndian.AppendUint16(handshake, uint16(port))
	handshake = append(handshake, minecraftVarint(2)...)
	name := "StackKitsProbe"
	login := append(minecraftVarint(0), minecraftVarint(len(name))...)
	login = append(login, name...)
	login = append(login, make([]byte, 16)...)
	packet := append(minecraftVarint(len(handshake)), handshake...)
	packet = append(packet, minecraftVarint(len(login))...)
	packet = append(packet, login...)
	if _, err := conn.Write(packet); err != nil {
		return "", err
	}
	reader := bufio.NewReader(conn)
	if _, err := binary.ReadUvarint(reader); err != nil {
		return "", err
	}
	id, err := binary.ReadUvarint(reader)
	if err != nil {
		return "", err
	}
	switch id {
	case 0x01:
		return "minecraft-java " + version + " online-mode login", nil
	case 0x02, 0x03:
		return "", errors.New("the Minecraft server admitted an unauthenticated login; online mode is off")
	}
	return "", fmt.Errorf("unexpected Minecraft login answer 0x%02x", id)
}

func minecraftVarint(n int) []byte {
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

func javaServerListPing(host string, port int) (string, int, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return "", 0, err
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
		return "", 0, err
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
		return "", 0, err
	}
	if _, err := readVarint(); err != nil {
		return "", 0, err
	}
	length, err := readVarint()
	if err != nil || length <= 0 || length > 1<<20 {
		return "", 0, errors.New("invalid status length")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return "", 0, err
	}
	var status struct {
		Version struct {
			Name     string `json:"name"`
			Protocol int    `json:"protocol"`
		} `json:"version"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return "", 0, err
	}
	return status.Version.Name, status.Version.Protocol, nil
}

func bedrockRakNetPing(host string, port int) (string, error) {
	conn, err := net.DialTimeout("udp", net.JoinHostPort(host, strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	magic := rakNetMagic
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

// worldSlug is a filesystem-safe world name within the game's length limit.
func worldSlug(name string, limit int) string {
	slug := slugGameName(name)
	if len(slug) > limit {
		slug = strings.TrimRight(slug[:limit], "-")
	}
	return slug
}

// validateGamePassword admits a join password friends can type: 8 to 20
// printable characters without quotes, not contained in the server name
// (Valheim refuses such passwords).
func validateGamePassword(password, name string) error {
	if len(password) < 8 || len(password) > 20 {
		return errors.New("a join password of 8 to 20 characters is required (password)")
	}
	for _, r := range password {
		if r < 0x21 || r > 0x7e || r == '"' || r == '\'' || r == '\\' {
			return errors.New("the join password may only use printable ASCII characters without spaces, quotes or backslashes")
		}
	}
	if strings.Contains(strings.ToLower(name), strings.ToLower(password)) {
		return errors.New("the join password must not be part of the server name")
	}
	return nil
}

// terrariaHandshake sends a client hello and accepts only the password
// challenge: a Terraria server that admits a client without a password is
// not a curated server.
func terrariaHandshake(host string, port int, password string) (string, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	version := "Terraria326" // 1.4.5 protocol
	payload := append([]byte{1, byte(len(version))}, version...)
	packet := binary.LittleEndian.AppendUint16(nil, uint16(len(payload)+2))
	packet = append(packet, payload...)
	if _, err := conn.Write(packet); err != nil {
		return "", err
	}
	header := make([]byte, 3)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", err
	}
	switch header[2] {
	case 37:
	case 2:
		// The server rejected the probe's client version, which still proves
		// a Terraria server answers; setup reads the password setting back.
		return "terraria answered (client protocol differs)", nil
	case 3:
		return "", errors.New("the Terraria server admitted a client without a password")
	default:
		return "", fmt.Errorf("unexpected Terraria answer type %d", header[2])
	}
	if length := int(binary.LittleEndian.Uint16(header[:2])); length > 3 {
		if _, err := io.CopyN(io.Discard, conn, int64(length-3)); err != nil {
			return "", err
		}
	}
	// Answer the challenge as a joining player would; the server assigns a
	// player slot only for the owner's password.
	if len(password) > 127 {
		return "", errors.New("join password is too long")
	}
	answer := append([]byte{38, byte(len(password))}, password...)
	if _, err := conn.Write(append(binary.LittleEndian.AppendUint16(nil, uint16(len(answer)+2)), answer...)); err != nil {
		return "", err
	}
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", err
	}
	if header[2] != 3 {
		return "", fmt.Errorf("the Terraria server did not admit the join password (answer type %d)", header[2])
	}
	return "terraria join accepted with password", nil
}

// steamNetworkingChallenge sends a Steam networking sockets challenge request
// to the game port. An unlisted Valheim server does not answer Steam A2S
// queries, but it answers this handshake with a reply echoing our connection.
func steamNetworkingChallenge(host string, port int, _ string) (string, error) {
	conn, err := net.DialTimeout("udp", net.JoinHostPort(host, strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	connectionID := make([]byte, 4)
	_, _ = rand.Read(connectionID)
	connectionID[0] |= 1
	// CMsgSteamSockets_UDP_ChallengeRequest: connection_id (fixed32, field 1),
	// my_timestamp (fixed64, field 3), protocol_version (varint, field 4).
	message := append([]byte{0x0d}, connectionID...)
	message = binary.LittleEndian.AppendUint64(append(message, 0x19), uint64(time.Now().UnixMicro()))
	message = append(message, 0x20, 11)
	packet := binary.LittleEndian.AppendUint16([]byte{32}, uint16(len(message)))
	packet = append(packet, message...)
	packet = append(packet, make([]byte, 512-len(packet))...)
	if _, err := conn.Write(packet); err != nil {
		return "", err
	}
	buffer := make([]byte, 2048)
	n, err := conn.Read(buffer)
	if err != nil {
		return "", err
	}
	if n < 6 || buffer[0] != 33 || buffer[1] != 0x0d || !bytes.Equal(buffer[2:6], connectionID) {
		return "", errors.New("the game port did not answer the Steam networking challenge")
	}
	return "steam-networking challenge-reply", nil
}

// verifyPasswordSet reads the rendered configuration back and fails unless a
// non-empty join password is in force. The value itself is never returned.
func (c pterodactylClient) verifyPasswordSet(ctx context.Context, identifier, file string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.baseURL, "/")+"/api/client/servers/"+identifier+"/files/contents?file="+url.QueryEscape(file), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.userKey)
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("read back the join password setting: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode != http.StatusOK {
		return fmt.Errorf("read back the join password setting: HTTP %d", response.StatusCode)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, found := strings.CutPrefix(strings.TrimSpace(line), "password="); found && value != "" {
			return nil
		}
	}
	return errors.New("the game server started without a join password; setup refuses an open server")
}

var rakNetMagic = []byte{0x00, 0xff, 0xff, 0x00, 0xfe, 0xfe, 0xfe, 0xfe, 0xfd, 0xfd, 0xfd, 0xfd, 0x12, 0x34, 0x56, 0x78}

// bedrockJoinCheck pings the server, then opens a RakNet connection as a
// joining client would; the server must offer its MTU in an open-connection
// reply.
func bedrockJoinCheck(host string, port int, _ string) (string, error) {
	answer, err := bedrockRakNetPing(host, port)
	if err != nil {
		return "", err
	}
	conn, err := net.DialTimeout("udp", net.JoinHostPort(host, strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	packet := append([]byte{0x05}, rakNetMagic...)
	packet = append(packet, 11)
	packet = append(packet, make([]byte, 1400-len(packet))...)
	if _, err := conn.Write(packet); err != nil {
		return "", err
	}
	buffer := make([]byte, 2048)
	n, err := conn.Read(buffer)
	if err != nil {
		return "", err
	}
	if n < 1 || buffer[0] != 0x06 {
		return "", errors.New("the Bedrock server did not accept a RakNet connection")
	}
	return answer + " connection accepted", nil
}

// OwnedGameServers lists the identifiers of every game server the owner's
// client key can operate, with its current power state.
func OwnedGameServers(ctx context.Context, client *http.Client, baseURL, clientKey string) (map[string]string, error) {
	c := pterodactylClient{http: client, baseURL: baseURL, userKey: clientKey}
	states := map[string]string{}
	for page := 1; page <= 20; page++ {
		var list struct {
			Data []struct {
				Attributes struct {
					Identifier string `json:"identifier"`
				} `json:"attributes"`
			} `json:"data"`
			Meta struct {
				Pagination struct {
					TotalPages int `json:"total_pages"`
				} `json:"pagination"`
			} `json:"meta"`
		}
		if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/client?page=%d", page), c.userKey, nil, "", &list); err != nil {
			return nil, fmt.Errorf("list game servers: %w", err)
		}
		for _, server := range list.Data {
			state, err := c.currentState(ctx, server.Attributes.Identifier)
			if err != nil {
				return nil, fmt.Errorf("read game server %s state: %w", server.Attributes.Identifier, err)
			}
			states[server.Attributes.Identifier] = state
		}
		if page >= list.Meta.Pagination.TotalPages {
			break
		}
	}
	return states, nil
}

// StopGameServer asks the game to shut down with its own stop command (which
// saves the world) and waits until Wings reports it offline.
func StopGameServer(ctx context.Context, client *http.Client, baseURL, clientKey, identifier string, limit time.Duration) error {
	c := pterodactylClient{http: client, baseURL: baseURL, userKey: clientKey}
	if _, err := c.do(ctx, http.MethodPost, "/api/client/servers/"+identifier+"/power", c.userKey, map[string]string{"signal": "stop"}, "", nil); err != nil {
		return fmt.Errorf("stop game server %s: %w", identifier, err)
	}
	return c.waitState(ctx, identifier, "offline", limit)
}

// StartGameServer sends the start signal without waiting for the world to load.
func StartGameServer(ctx context.Context, client *http.Client, baseURL, clientKey, identifier string) error {
	c := pterodactylClient{http: client, baseURL: baseURL, userKey: clientKey}
	if _, err := c.do(ctx, http.MethodPost, "/api/client/servers/"+identifier+"/power", c.userKey, map[string]string{"signal": "start"}, "", nil); err != nil {
		return fmt.Errorf("start game server %s: %w", identifier, err)
	}
	return nil
}

// GameServerSummary is the secret-free view of one owner game server.
type GameServerSummary struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
	State      string `json:"state"`
	Port       int    `json:"port,omitempty"`
}

// ListGameServers returns every game server the owner's client key operates.
func ListGameServers(ctx context.Context, client *http.Client, baseURL, clientKey string) ([]GameServerSummary, error) {
	c := pterodactylClient{http: client, baseURL: baseURL, userKey: clientKey}
	var out []GameServerSummary
	for page := 1; page <= 20; page++ {
		var list struct {
			Data []struct {
				Attributes struct {
					Identifier    string `json:"identifier"`
					Name          string `json:"name"`
					Relationships struct {
						Allocations struct {
							Data []struct {
								Attributes struct {
									Port      int  `json:"port"`
									IsDefault bool `json:"is_default"`
								} `json:"attributes"`
							} `json:"data"`
						} `json:"allocations"`
					} `json:"relationships"`
				} `json:"attributes"`
			} `json:"data"`
			Meta struct {
				Pagination struct {
					TotalPages int `json:"total_pages"`
				} `json:"pagination"`
			} `json:"meta"`
		}
		if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/client?page=%d", page), c.userKey, nil, "", &list); err != nil {
			return nil, fmt.Errorf("list game servers: %w", err)
		}
		for _, server := range list.Data {
			summary := GameServerSummary{Identifier: server.Attributes.Identifier, Name: server.Attributes.Name}
			for _, allocation := range server.Attributes.Relationships.Allocations.Data {
				if allocation.Attributes.IsDefault {
					summary.Port = allocation.Attributes.Port
				}
			}
			state, err := c.currentState(ctx, summary.Identifier)
			if err != nil {
				return nil, fmt.Errorf("read game server %s state: %w", summary.Identifier, err)
			}
			summary.State = state
			out = append(out, summary)
		}
		if page >= list.Meta.Pagination.TotalPages {
			break
		}
	}
	return out, nil
}

// GameServerPower sends one power signal and, for start/stop/restart, waits
// for the resulting state so the caller reports an observed outcome.
func GameServerPower(ctx context.Context, client *http.Client, baseURL, clientKey, identifier, signal string) (string, error) {
	want := map[string]string{"start": "running", "restart": "running", "stop": "offline"}[signal]
	if want == "" {
		return "", fmt.Errorf("power signal %q is not admitted; use start, stop or restart", signal)
	}
	c := pterodactylClient{http: client, baseURL: baseURL, userKey: clientKey}
	if _, err := c.do(ctx, http.MethodPost, "/api/client/servers/"+url.PathEscape(identifier)+"/power", c.userKey, map[string]string{"signal": signal}, "", nil); err != nil {
		return "", fmt.Errorf("send %s to game server %s: %w", signal, identifier, err)
	}
	if err := c.waitState(ctx, identifier, want, 8*time.Minute); err != nil {
		return "", err
	}
	return want, nil
}

// AllowGamePlayer admits one player on a curated allow-list server and reads
// the game's own allow list back. Servers not created by StackKits are
// refused, because their admission command is unknown.
func AllowGamePlayer(ctx context.Context, client *http.Client, baseURL, applicationKey, clientKey, identifier, player string) error {
	if !gamePlayerName.MatchString(player) {
		return fmt.Errorf("player name %q is not a valid game account name", player)
	}
	c := pterodactylClient{http: client, baseURL: baseURL, appKey: applicationKey, userKey: clientKey}
	var servers pterodactylList[struct {
		Identifier string `json:"identifier"`
		ExternalID string `json:"external_id"`
	}]
	if _, err := c.do(ctx, http.MethodGet, "/api/application/servers?per_page=500", c.appKey, nil, "", &servers); err != nil {
		return fmt.Errorf("look up the game server: %w", err)
	}
	var profile *GameProfile
	for _, server := range servers.Data {
		if server.Attributes.Identifier != identifier {
			continue
		}
		for id, candidate := range GameProfiles {
			if strings.HasPrefix(server.Attributes.ExternalID, "stackkit-game-"+id+"-") {
				candidate := candidate
				profile = &candidate
			}
		}
	}
	if profile == nil {
		return fmt.Errorf("game server %s was not created by stackkit setup game; manage its players in the Panel", identifier)
	}
	if profile.Access != "allow-list" {
		return fmt.Errorf("%s has no account allow list; players join with the server's join password", profile.DisplayName)
	}
	if _, err := c.do(ctx, http.MethodPost, "/api/client/servers/"+url.PathEscape(identifier)+"/command", c.userKey, map[string]string{"command": fmt.Sprintf(profile.AllowCommand, player)}, "", nil); err != nil {
		return fmt.Errorf("admit player %q (the server must be running): %w", player, err)
	}
	missing, err := c.verifyAllowList(ctx, identifier, *profile, []string{player})
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return fmt.Errorf("the game did not admit %s; check the exact %s", player, profile.AccountHint)
	}
	return nil
}
