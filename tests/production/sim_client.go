//go:build production

package production

import (
	"fmt"
	"net/http"
	"time"
)

// Simulation represents a simulation returned by the Sim API.
type Simulation struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// simulationListResponse wraps the paginated list response from the Sim API.
type simulationListResponse struct {
	Data []Simulation `json:"data"`
}

// Node represents a VM node in a simulation.
type Node struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	SSHIP       string `json:"ssh_ip,omitempty"`
	SSHPort     int    `json:"ssh_port,omitempty"`
	SSHUser     string `json:"ssh_user,omitempty"`
	SSHPassword string `json:"ssh_password,omitempty"`
	SSHKeyPath  string `json:"ssh_key_path,omitempty"`
	ProxyJump   string `json:"proxy_jump,omitempty"`
}

// NodeSSHInfo is the structured response from the /nodes/:id/ssh endpoint.
// It provides connection details for SSH access to a node, including the
// server-side key path and ProxyJump hint for external access.
type NodeSSHInfo struct {
	Host        string `json:"host"`         // Docker/internal hostname (e.g. "host.docker.internal")
	Port        int    `json:"port"`         // SSH port (e.g. 30001 — NAT-forwarded on host)
	User        string `json:"user"`         // SSH username (e.g. "kombify-sim")
	KeyPath     string `json:"key_path"`     // Server-side PEM path in kombify-sim container
	PublicKey   string `json:"public_key"`   // SSH public key (informational)
	AuthMethod  string `json:"auth_method"`  // "key" or "password"
	DisplayHost string `json:"display_host"` // Public hostname for display (e.g. "simulate.kombify.io")
	ProxyJump   string `json:"proxy_jump"`   // Bastion hint for external runners (e.g. "root@simulate.kombify.io")
	NodePrivateIP string `json:"node_private_ip,omitempty"` // Private IP inside the simulation network
}

// HealthResponse from the Sim API health endpoint.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
}

// SimClient provides typed access to the Sim API through Kong gateway.
type SimClient struct {
	client *Client
}

// NewSimClient creates a Sim API client using the given production client.
func NewSimClient(c *Client) *SimClient {
	return &SimClient{client: c}
}

// Health checks the Sim API health endpoint.
func (s *SimClient) Health() (*HealthResponse, error) {
	var result HealthResponse
	resp, err := s.client.Get("/v1/simulation/health", &result)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("health check failed: status %d", resp.StatusCode)
	}
	return &result, nil
}

// ListSimulations returns all simulations for the authenticated user.
func (s *SimClient) ListSimulations() ([]Simulation, error) {
	var result simulationListResponse
	resp, err := s.client.Get("/v1/simulation/simulations", &result)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list simulations failed: status %d", resp.StatusCode)
	}
	return result.Data, nil
}

// CreateSimulationRequest is the body for creating a simulation.
type CreateSimulationRequest struct {
	Name string `json:"name"`
}

// CreateSimulation creates a new simulation.
func (s *SimClient) CreateSimulation(name string) (*Simulation, error) {
	var result Simulation
	resp, err := s.client.Post("/v1/simulation/simulations", CreateSimulationRequest{Name: name}, &result)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("create simulation failed: status %d", resp.StatusCode)
	}
	return &result, nil
}

// DeleteSimulation deletes a simulation by ID.
func (s *SimClient) DeleteSimulation(simID string) error {
	resp, err := s.client.Delete("/v1/simulation/simulations/" + simID)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("delete simulation failed: status %d", resp.StatusCode)
	}
	return nil
}

// CreateNodeRequest is the body for creating a node.
type CreateNodeRequest struct {
	Name     string            `json:"name"`
	Template string            `json:"template,omitempty"`
	Config   map[string]string `json:"config,omitempty"`
}

// CreateNode creates a new node in a simulation.
func (s *SimClient) CreateNode(simID string, req CreateNodeRequest) (*Node, error) {
	var result Node
	path := fmt.Sprintf("/v1/simulation/simulations/%s/nodes", simID)
	resp, err := s.client.Post(path, req, &result)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("create node failed: status %d", resp.StatusCode)
	}
	return &result, nil
}

// StartNode starts a node.
func (s *SimClient) StartNode(nodeID string) (*Node, error) {
	var result Node
	path := fmt.Sprintf("/v1/simulation/nodes/%s/start", nodeID)
	resp, err := s.client.Post(path, nil, &result)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("start node failed: status %d", resp.StatusCode)
	}
	return &result, nil
}

// StopNode stops a node.
func (s *SimClient) StopNode(nodeID string) (*Node, error) {
	var result Node
	path := fmt.Sprintf("/v1/simulation/nodes/%s/stop", nodeID)
	resp, err := s.client.Post(path, nil, &result)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("stop node failed: status %d", resp.StatusCode)
	}
	return &result, nil
}

// DeleteNode deletes a node.
func (s *SimClient) DeleteNode(nodeID string) error {
	path := fmt.Sprintf("/v1/simulation/nodes/%s", nodeID)
	resp, err := s.client.Delete(path)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("delete node failed: status %d", resp.StatusCode)
	}
	return nil
}

// GetNodeSSH returns full SSH connection info for a node.
// The returned NodeSSHInfo contains the server-side key path, ProxyJump hint,
// and internal host/port. Use extractNodeSSHKey to read the key from the
// Sim container, then NewSSHSessionFromInfo to connect.
func (s *SimClient) GetNodeSSH(nodeID string) (*NodeSSHInfo, error) {
	var result NodeSSHInfo
	path := fmt.Sprintf("/v1/simulation/nodes/%s/ssh", nodeID)
	resp, err := s.client.Get(path, &result)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get SSH info failed: status %d", resp.StatusCode)
	}
	return &result, nil
}

// WaitForNode polls until the node status is "running" or timeout expires.
func (s *SimClient) WaitForNode(simID, nodeID string, timeout time.Duration) (*Node, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		node, err := s.GetNode(nodeID)
		if err == nil && node.Status == "running" {
			return node, nil
		}
		time.Sleep(5 * time.Second)
	}
	return nil, fmt.Errorf("node %s did not reach running status within %s", nodeID, timeout)
}

// CreateVPSNode is a convenience wrapper that creates an Ubuntu VPS node with
// external access enabled.
func (s *SimClient) CreateVPSNode(simID, name string) (*Node, error) {
	return s.CreateNode(simID, CreateNodeRequest{
		Name:     name,
		Template: "ubuntu",
		Config: map[string]string{
			"location":        "vps",
			"os":              "ubuntu",
			"external_access": "true",
		},
	})
}
func (s *SimClient) GetNode(nodeID string) (*Node, error) {
	var result Node
	path := fmt.Sprintf("/v1/simulation/nodes/%s", nodeID)
	resp, err := s.client.Get(path, &result)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get node failed: status %d", resp.StatusCode)
	}
	return &result, nil
}
