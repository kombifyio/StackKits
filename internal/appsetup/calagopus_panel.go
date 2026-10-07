package appsetup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// calagopusPanel speaks the Calagopus Admin and Client APIs (ADR-0048). The
// setup key carries the administrative permissions the bootstrap granted; the
// owner's client key only server read, power, console and files.
type calagopusPanel struct{ panelHTTP }

type calagopusPage[T any] struct {
	Total   int `json:"total"`
	PerPage int `json:"per_page"`
	Page    int `json:"page"`
	Data    []T `json:"data"`
}

type calagopusServer struct {
	UUID       string  `json:"uuid"`
	UUIDShort  string  `json:"uuid_short"`
	Name       string  `json:"name"`
	ExternalID *string `json:"external_id"`
	Status     *string `json:"status"`
	Allocation *struct {
		Port int `json:"port"`
	} `json:"allocation"`
}

// record maps the server status: null once installed, installing while the
// install script runs, install_failed after a failed install.
func (s calagopusServer) record() gameServerRecord {
	status := ""
	if s.Status != nil {
		status = *s.Status
	}
	return gameServerRecord{ref: s.UUID, UUID: s.UUID, Identifier: s.UUIDShort, Installed: status == "", Installing: status == "installing"}
}

func (c calagopusPanel) lookupServer(ctx context.Context, externalID string) (gameServerRecord, bool, error) {
	var found struct {
		Server calagopusServer `json:"server"`
	}
	err := c.do(ctx, http.MethodGet, "/api/admin/servers/external/"+url.PathEscape(externalID), c.setupKey, nil, "", &found)
	if errors.Is(err, errPanelNotFound) {
		return gameServerRecord{}, false, nil
	}
	if err != nil {
		return gameServerRecord{}, false, err
	}
	return found.Server.record(), true, nil
}

type calagopusEgg struct {
	UUID            string
	StartupCommands map[string]string
	Variables       map[string]string
}

// startup is the Egg's default startup command.
func (egg calagopusEgg) startup() string {
	if command, ok := egg.StartupCommands["Default"]; ok {
		return command
	}
	keys := make([]string, 0, len(egg.StartupCommands))
	for key := range egg.StartupCommands {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return egg.StartupCommands[keys[0]]
}

func (egg calagopusEgg) variables(overlay map[string]string) []map[string]string {
	values := pterodactylEgg{Variables: egg.Variables}.environment(overlay)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]map[string]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, map[string]string{"env_variable": key, "value": values[key]})
	}
	return out
}

func (c calagopusPanel) findEgg(ctx context.Context, profile GameProfile) (calagopusEgg, error) {
	var nests struct {
		Nests calagopusPage[struct {
			UUID string `json:"uuid"`
			Name string `json:"name"`
		}] `json:"nests"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/admin/nests?per_page=100", c.setupKey, nil, "", &nests); err != nil {
		return calagopusEgg{}, err
	}
	for _, nest := range nests.Nests.Data {
		if nest.Name != profile.NestName {
			continue
		}
		var eggs struct {
			Eggs calagopusPage[struct {
				UUID            string            `json:"uuid"`
				Name            string            `json:"name"`
				StartupCommands map[string]string `json:"startup_commands"`
			}] `json:"eggs"`
		}
		if err := c.do(ctx, http.MethodGet, "/api/admin/nests/"+nest.UUID+"/eggs?per_page=100", c.setupKey, nil, "", &eggs); err != nil {
			return calagopusEgg{}, err
		}
		for _, egg := range eggs.Eggs.Data {
			if egg.Name != profile.EggName {
				continue
			}
			var variables struct {
				Variables []struct {
					EnvVariable  string  `json:"env_variable"`
					DefaultValue *string `json:"default_value"`
				} `json:"variables"`
			}
			if err := c.do(ctx, http.MethodGet, "/api/admin/nests/"+nest.UUID+"/eggs/"+egg.UUID+"/variables", c.setupKey, nil, "", &variables); err != nil {
				return calagopusEgg{}, err
			}
			defaults := map[string]string{}
			for _, variable := range variables.Variables {
				if variable.DefaultValue != nil {
					defaults[variable.EnvVariable] = *variable.DefaultValue
				} else {
					defaults[variable.EnvVariable] = ""
				}
			}
			return calagopusEgg{UUID: egg.UUID, StartupCommands: egg.StartupCommands, Variables: defaults}, nil
		}
	}
	return calagopusEgg{}, fmt.Errorf("curated Egg %q is not installed; re-run apply so the bootstrap imports it", profile.EggName)
}

func (c calagopusPanel) ownerUUID(ctx context.Context, ownerEmail string) (string, error) {
	var users struct {
		Users calagopusPage[struct {
			UUID  string `json:"uuid"`
			Email string `json:"email"`
		}] `json:"users"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/admin/users?per_page=100&search="+url.QueryEscape(ownerEmail), c.setupKey, nil, "", &users); err != nil {
		return "", fmt.Errorf("find the Panel owner: %w", err)
	}
	for _, user := range users.Users.Data {
		if strings.EqualFold(user.Email, ownerEmail) {
			return user.UUID, nil
		}
	}
	return "", errors.New("the Panel owner account is missing; re-run apply so the bootstrap converges")
}

// freeAllocations reserves the profile's ports on the StackKits node and
// refuses a server the node's remaining game memory cannot hold.
func (c calagopusPanel) freeAllocations(ctx context.Context, profile GameProfile, ports []int) (string, []string, error) {
	var nodes struct {
		Nodes calagopusPage[struct {
			UUID   string `json:"uuid"`
			Name   string `json:"name"`
			Memory int    `json:"memory"`
		}] `json:"nodes"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/admin/nodes?per_page=100", c.setupKey, nil, "", &nodes); err != nil {
		return "", nil, err
	}
	for _, node := range nodes.Nodes.Data {
		if node.Name != "stackkit-node" {
			continue
		}
		var capacity struct {
			Allocated struct {
				Memory int `json:"memory"`
			} `json:"allocated"`
		}
		if err := c.do(ctx, http.MethodGet, "/api/admin/nodes/"+node.UUID+"/capacity", c.setupKey, nil, "", &capacity); err != nil {
			return "", nil, err
		}
		if err := checkGameMemory(profile, node.Memory, capacity.Allocated.Memory); err != nil {
			return "", nil, err
		}
		var allocations struct {
			Allocations calagopusPage[struct {
				UUID   string           `json:"uuid"`
				Port   int              `json:"port"`
				Server *calagopusServer `json:"server"`
			}] `json:"allocations"`
		}
		if err := c.do(ctx, http.MethodGet, "/api/admin/nodes/"+node.UUID+"/allocations?per_page=100", c.setupKey, nil, "", &allocations); err != nil {
			return "", nil, err
		}
		ids := make([]string, 0, len(ports))
		for _, port := range ports {
			id := ""
			for _, allocation := range allocations.Allocations.Data {
				if allocation.Port == port {
					if allocation.Server != nil {
						return "", nil, fmt.Errorf("port %d is already used by another game server on this node", port)
					}
					id = allocation.UUID
				}
			}
			if id == "" {
				return "", nil, fmt.Errorf("port %d is not offered by the StackKits game node; re-run apply so the bootstrap converges", port)
			}
			ids = append(ids, id)
		}
		return node.UUID, ids, nil
	}
	return "", nil, errors.New("the StackKits game node is missing; re-run apply so the bootstrap converges")
}

func (c calagopusPanel) createServer(ctx context.Context, profile GameProfile, name, password, externalID, ownerEmail string) (gameServerRecord, error) {
	owner, err := c.ownerUUID(ctx, ownerEmail)
	if err != nil {
		return gameServerRecord{}, err
	}
	egg, err := c.findEgg(ctx, profile)
	if err != nil {
		return gameServerRecord{}, err
	}
	node, allocations, err := c.freeAllocations(ctx, profile, append([]int{profile.Port}, profile.ExtraPorts...))
	if err != nil {
		return gameServerRecord{}, err
	}
	image, err := prepareLocalGameImage(ctx, profile.DockerImage)
	if err != nil {
		return gameServerRecord{}, err
	}
	body := map[string]any{
		"node_uuid": node, "owner_uuid": owner, "egg_uuid": egg.UUID,
		"allocation_uuid": allocations[0], "allocation_uuids": allocations[1:],
		"start_on_completion": false, "skip_installer": false, "external_id": externalID, "name": name,
		"limits":      map[string]int{"cpu": profile.CPUPercent, "memory": profile.MemoryMB, "memory_overhead": 0, "swap": 0, "disk": profile.DiskMB},
		"pinned_cpus": []int{}, "startup": egg.startup(), "image": image,
		"hugepages_passthrough_enabled": false, "kvm_passthrough_enabled": false,
		"feature_limits": map[string]int{"allocations": len(allocations), "databases": 0, "backups": 3, "schedules": 0},
		"variables":      egg.variables(profile.Environment(name, password)),
	}
	var created struct {
		Server calagopusServer `json:"server"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/admin/servers", c.setupKey, body, "", &created); err != nil {
		return gameServerRecord{}, fmt.Errorf("create game server: %w", err)
	}
	return created.Server.record(), nil
}

func (c calagopusPanel) convergeStartup(ctx context.Context, server gameServerRecord, profile GameProfile, name, password string) error {
	egg, err := c.findEgg(ctx, profile)
	if err != nil {
		return err
	}
	image, err := prepareLocalGameImage(ctx, profile.DockerImage)
	if err != nil {
		return err
	}
	if err := c.do(ctx, http.MethodPatch, "/api/admin/servers/"+server.ref, c.setupKey, map[string]any{"startup": egg.startup(), "image": image}, "", nil); err != nil {
		return fmt.Errorf("converge the curated game settings: %w", err)
	}
	variables := map[string]any{"variables": egg.variables(profile.Environment(name, password))}
	if err := c.do(ctx, http.MethodPut, "/api/admin/servers/"+server.ref+"/variables", c.setupKey, variables, "", nil); err != nil {
		return fmt.Errorf("converge the curated game variables: %w", err)
	}
	return nil
}

func (c calagopusPanel) refreshServer(ctx context.Context, server gameServerRecord) (gameServerRecord, error) {
	var current struct {
		Server calagopusServer `json:"server"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/admin/servers/"+server.ref, c.setupKey, nil, "", &current); err != nil {
		return gameServerRecord{}, err
	}
	return current.Server.record(), nil
}

// reinstall reruns the install script through the owner's server API with
// the setup key, which holds the settings.install permission.
func (c calagopusPanel) reinstall(ctx context.Context, server gameServerRecord) error {
	return c.do(ctx, http.MethodPost, "/api/client/servers/"+url.PathEscape(server.Identifier)+"/settings/install", c.setupKey, map[string]any{"truncate_directory": false, "start_on_completion": false}, "", nil)
}

func (c calagopusPanel) externalIDOf(ctx context.Context, identifier string) (string, error) {
	for page := 1; page <= 20; page++ {
		var servers struct {
			Servers calagopusPage[calagopusServer] `json:"servers"`
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/admin/servers?per_page=100&page=%d", page), c.setupKey, nil, "", &servers); err != nil {
			return "", err
		}
		for _, server := range servers.Servers.Data {
			if server.UUIDShort == identifier && server.ExternalID != nil {
				return *server.ExternalID, nil
			}
		}
		if page*servers.Servers.PerPage >= servers.Servers.Total {
			break
		}
	}
	return "", nil
}

func (c calagopusPanel) state(ctx context.Context, identifier string) (string, error) {
	var resources struct {
		Resources struct {
			State string `json:"state"`
		} `json:"resources"`
	}
	err := c.do(ctx, http.MethodGet, "/api/client/servers/"+url.PathEscape(identifier)+"/resources", c.clientKey, nil, "", &resources)
	return resources.Resources.State, err
}

func (c calagopusPanel) power(ctx context.Context, identifier, signal string) error {
	return c.do(ctx, http.MethodPost, "/api/client/servers/"+url.PathEscape(identifier)+"/power", c.clientKey, map[string]string{"action": signal}, "", nil)
}

func (c calagopusPanel) command(ctx context.Context, identifier, command string) error {
	return c.do(ctx, http.MethodPost, "/api/client/servers/"+url.PathEscape(identifier)+"/command", c.clientKey, map[string]string{"command": command}, "", nil)
}

func (c calagopusPanel) readFile(ctx context.Context, identifier, path string) (string, bool, error) {
	var data string
	err := c.do(ctx, http.MethodGet, "/api/client/servers/"+url.PathEscape(identifier)+"/files/contents?file="+url.QueryEscape(path), c.clientKey, nil, "", &data)
	if errors.Is(err, errPanelNotFound) {
		return "", false, nil
	}
	return data, err == nil, err
}

func (c calagopusPanel) writeFile(ctx context.Context, identifier, path, body string) error {
	return c.do(ctx, http.MethodPost, "/api/client/servers/"+url.PathEscape(identifier)+"/files/write?file="+url.QueryEscape(path), c.clientKey, body, "text/plain", nil)
}

func (c calagopusPanel) listServers(ctx context.Context) ([]GameServerSummary, error) {
	var out []GameServerSummary
	for page := 1; page <= 20; page++ {
		var servers struct {
			Servers calagopusPage[calagopusServer] `json:"servers"`
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/client/servers?per_page=100&page=%d", page), c.clientKey, nil, "", &servers); err != nil {
			return nil, err
		}
		for _, server := range servers.Servers.Data {
			summary := GameServerSummary{Identifier: server.UUIDShort, Name: server.Name}
			if server.Allocation != nil {
				summary.Port = server.Allocation.Port
			}
			state, err := c.state(ctx, summary.Identifier)
			if err != nil {
				return nil, fmt.Errorf("read game server %s state: %w", summary.Identifier, err)
			}
			summary.State = state
			out = append(out, summary)
		}
		if servers.Servers.PerPage == 0 || page*servers.Servers.PerPage >= servers.Servers.Total {
			break
		}
	}
	return out, nil
}
