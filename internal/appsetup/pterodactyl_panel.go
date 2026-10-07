package appsetup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// pterodactylPanel speaks the Pterodactyl Application and Client APIs.
// Pelican keeps their shape without nests: its Eggs are a top-level list.
type pterodactylPanel struct {
	panelHTTP
	pelican bool
}

type pterodactylList[T any] struct {
	Data []struct {
		Attributes T `json:"attributes"`
	} `json:"data"`
	Meta struct {
		Pagination struct {
			TotalPages int `json:"total_pages"`
		} `json:"pagination"`
	} `json:"meta"`
}

type pterodactylServer struct {
	ID         int    `json:"id"`
	UUID       string `json:"uuid"`
	Identifier string `json:"identifier"`
	Status     string `json:"status"`
	ExternalID string `json:"external_id"`
	Container  struct {
		Installed any `json:"installed"`
	} `json:"container"`
}

func (s pterodactylServer) record() gameServerRecord {
	installed := false
	switch value := s.Container.Installed.(type) {
	case bool:
		installed = value
	case float64:
		installed = value == 1
	}
	return gameServerRecord{ref: strconv.Itoa(s.ID), UUID: s.UUID, Identifier: s.Identifier, Installed: installed, Installing: s.Status == "installing"}
}

func (c pterodactylPanel) lookupServer(ctx context.Context, externalID string) (gameServerRecord, bool, error) {
	var existing struct {
		Attributes pterodactylServer `json:"attributes"`
	}
	err := c.do(ctx, http.MethodGet, "/api/application/servers/external/"+url.PathEscape(externalID), c.setupKey, nil, "", &existing)
	if errors.Is(err, errPanelNotFound) {
		return gameServerRecord{}, false, nil
	}
	if err != nil {
		return gameServerRecord{}, false, err
	}
	return existing.Attributes.record(), true, nil
}

func (c pterodactylPanel) createServer(ctx context.Context, profile GameProfile, name, password, externalID, ownerEmail string) (gameServerRecord, error) {
	var users pterodactylList[struct {
		ID    int    `json:"id"`
		Email string `json:"email"`
	}]
	if err := c.do(ctx, http.MethodGet, "/api/application/users?filter[email]="+url.QueryEscape(ownerEmail), c.setupKey, nil, "", &users); err != nil {
		return gameServerRecord{}, fmt.Errorf("find the Panel owner: %w", err)
	}
	userID := 0
	for _, user := range users.Data {
		if strings.EqualFold(user.Attributes.Email, ownerEmail) {
			userID = user.Attributes.ID
		}
	}
	if userID == 0 {
		return gameServerRecord{}, errors.New("the Panel owner account is missing; re-run apply so the bootstrap converges")
	}
	egg, err := c.findEgg(ctx, profile)
	if err != nil {
		return gameServerRecord{}, err
	}
	allocations, err := c.freeAllocations(ctx, profile, append([]int{profile.Port}, profile.ExtraPorts...))
	if err != nil {
		return gameServerRecord{}, err
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
	if err := c.do(ctx, http.MethodPost, "/api/application/servers", c.setupKey, body, "", &createdServer); err != nil {
		return gameServerRecord{}, fmt.Errorf("create game server: %w", err)
	}
	return createdServer.Attributes.record(), nil
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

type pterodactylEggList = pterodactylList[struct {
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

func (c pterodactylPanel) findEgg(ctx context.Context, profile GameProfile) (pterodactylEgg, error) {
	var lists []string
	if c.pelican {
		lists = []string{"/api/application/eggs?include=variables&per_page=100"}
	} else {
		var nests pterodactylList[struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}]
		if err := c.do(ctx, http.MethodGet, "/api/application/nests?per_page=100", c.setupKey, nil, "", &nests); err != nil {
			return pterodactylEgg{}, err
		}
		for _, nest := range nests.Data {
			if nest.Attributes.Name == profile.NestName {
				lists = append(lists, fmt.Sprintf("/api/application/nests/%d/eggs?include=variables", nest.Attributes.ID))
			}
		}
	}
	for _, path := range lists {
		var eggs pterodactylEggList
		if err := c.do(ctx, http.MethodGet, path, c.setupKey, nil, "", &eggs); err != nil {
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

func (c pterodactylPanel) convergeStartup(ctx context.Context, server gameServerRecord, profile GameProfile, name, password string) error {
	egg, err := c.findEgg(ctx, profile)
	if err != nil {
		return err
	}
	body := map[string]any{
		"startup": egg.Startup, "egg": egg.ID, "image": profile.DockerImage, "skip_scripts": false,
		"environment": egg.environment(profile.Environment(name, password)),
	}
	if err := c.do(ctx, http.MethodPatch, "/api/application/servers/"+server.ref+"/startup", c.setupKey, body, "", nil); err != nil {
		return fmt.Errorf("converge the curated game settings: %w", err)
	}
	return nil
}

// freeAllocations reserves the profile's ports and refuses a server the node
// cannot hold next to the existing ones: the Panel enforces node capacity
// only for automatic deployment, not for an explicit allocation.
func (c pterodactylPanel) freeAllocations(ctx context.Context, profile GameProfile, ports []int) ([]int, error) {
	var nodes pterodactylList[struct {
		ID                 int    `json:"id"`
		Name               string `json:"name"`
		Memory             int    `json:"memory"`
		AllocatedResources struct {
			Memory int `json:"memory"`
		} `json:"allocated_resources"`
	}]
	if err := c.do(ctx, http.MethodGet, "/api/application/nodes", c.setupKey, nil, "", &nodes); err != nil {
		return nil, err
	}
	for _, node := range nodes.Data {
		if node.Attributes.Name != "stackkit-node" {
			continue
		}
		if err := checkGameMemory(profile, node.Attributes.Memory, node.Attributes.AllocatedResources.Memory); err != nil {
			return nil, err
		}
		var allocations pterodactylList[struct {
			ID       int  `json:"id"`
			Port     int  `json:"port"`
			Assigned bool `json:"assigned"`
		}]
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/application/nodes/%d/allocations?per_page=500", node.Attributes.ID), c.setupKey, nil, "", &allocations); err != nil {
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

func (c pterodactylPanel) refreshServer(ctx context.Context, server gameServerRecord) (gameServerRecord, error) {
	var current struct {
		Attributes pterodactylServer `json:"attributes"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/application/servers/"+server.ref, c.setupKey, nil, "", &current); err != nil {
		return gameServerRecord{}, err
	}
	return current.Attributes.record(), nil
}

func (c pterodactylPanel) reinstall(ctx context.Context, server gameServerRecord) error {
	return c.do(ctx, http.MethodPost, "/api/application/servers/"+server.ref+"/reinstall", c.setupKey, nil, "", nil)
}

func (c pterodactylPanel) externalIDOf(ctx context.Context, identifier string) (string, error) {
	var servers pterodactylList[pterodactylServer]
	if err := c.do(ctx, http.MethodGet, "/api/application/servers?per_page=500", c.setupKey, nil, "", &servers); err != nil {
		return "", err
	}
	for _, server := range servers.Data {
		if server.Attributes.Identifier == identifier {
			return server.Attributes.ExternalID, nil
		}
	}
	return "", nil
}

func (c pterodactylPanel) state(ctx context.Context, identifier string) (string, error) {
	var resources struct {
		Attributes struct {
			State string `json:"current_state"`
		} `json:"attributes"`
	}
	err := c.do(ctx, http.MethodGet, "/api/client/servers/"+url.PathEscape(identifier)+"/resources", c.clientKey, nil, "", &resources)
	return resources.Attributes.State, err
}

func (c pterodactylPanel) power(ctx context.Context, identifier, signal string) error {
	return c.do(ctx, http.MethodPost, "/api/client/servers/"+url.PathEscape(identifier)+"/power", c.clientKey, map[string]string{"signal": signal}, "", nil)
}

func (c pterodactylPanel) command(ctx context.Context, identifier, command string) error {
	return c.do(ctx, http.MethodPost, "/api/client/servers/"+url.PathEscape(identifier)+"/command", c.clientKey, map[string]string{"command": command}, "", nil)
}

func (c pterodactylPanel) readFile(ctx context.Context, identifier, path string) (string, bool, error) {
	var data string
	err := c.do(ctx, http.MethodGet, "/api/client/servers/"+url.PathEscape(identifier)+"/files/contents?file="+url.QueryEscape(path), c.clientKey, nil, "", &data)
	if errors.Is(err, errPanelNotFound) {
		return "", false, nil
	}
	return data, err == nil, err
}

func (c pterodactylPanel) writeFile(ctx context.Context, identifier, path, body string) error {
	return c.do(ctx, http.MethodPost, "/api/client/servers/"+url.PathEscape(identifier)+"/files/write?file="+url.QueryEscape(path), c.clientKey, body, "text/plain", nil)
}

func (c pterodactylPanel) listServers(ctx context.Context) ([]GameServerSummary, error) {
	var out []GameServerSummary
	for page := 1; page <= 20; page++ {
		var list pterodactylList[struct {
			Identifier    string `json:"identifier"`
			Name          string `json:"name"`
			Relationships struct {
				Allocations pterodactylList[struct {
					Port      int  `json:"port"`
					IsDefault bool `json:"is_default"`
				}] `json:"allocations"`
			} `json:"relationships"`
		}]
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/client?page=%d", page), c.clientKey, nil, "", &list); err != nil {
			return nil, err
		}
		for _, server := range list.Data {
			summary := GameServerSummary{Identifier: server.Attributes.Identifier, Name: server.Attributes.Name}
			for _, allocation := range server.Attributes.Relationships.Allocations.Data {
				if allocation.Attributes.IsDefault {
					summary.Port = allocation.Attributes.Port
				}
			}
			state, err := c.state(ctx, summary.Identifier)
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

// checkGameMemory refuses a server the node's remaining game memory cannot hold.
func checkGameMemory(profile GameProfile, nodeMemory, allocated int) error {
	if free := nodeMemory - allocated; profile.MemoryMB > free {
		return fmt.Errorf("the game node has %d MB of game memory left and %s needs %d MB; delete an unused server in the Panel or add memory to the node", max(free, 0), profile.DisplayName, profile.MemoryMB)
	}
	return nil
}
