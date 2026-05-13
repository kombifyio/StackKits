package kombifyme

import (
	"fmt"

	"github.com/kombifyio/stackkits/pkg/models"
)

// RegistryResponse is returned by the kombify.me instance registration endpoint.
type RegistryResponse struct {
	InstanceID string `json:"instance_id"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
}

// RegisterInstance registers a stackkit-server instance with kombify for Direct Connect.
// Cloudflare Edge uses this registry to discover and proxy requests to the instance.
func (c *Client) RegisterInstance(reg *models.InstanceRegistration) (*RegistryResponse, error) {
	var resp RegistryResponse
	if err := c.post("/registry/instances", reg, &resp); err != nil {
		return nil, fmt.Errorf("register instance: %w", err)
	}
	return &resp, nil
}

// Heartbeat sends a status update to kombify so Cloudflare Edge knows the instance is alive.
func (c *Client) Heartbeat(instanceID, status string) error {
	body := map[string]string{
		"instance_id": instanceID,
		"status":      status,
	}
	path := fmt.Sprintf("/registry/instances/%s/heartbeat", instanceID)
	return c.put(path, body)
}

// DeregisterInstance removes a stackkit-server instance from the registry.
func (c *Client) DeregisterInstance(instanceID string) error {
	path := fmt.Sprintf("/registry/instances/%s", instanceID)
	return c.delete(path)
}
