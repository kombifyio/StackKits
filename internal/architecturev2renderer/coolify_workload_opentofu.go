package architecturev2renderer

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const CoolifyProviderVersion = "0.1.27"

// CoolifyWorkloadSpec binds the provider to an explicitly imported node-owned
// service. Compose contains host file references, never the private .env values.
type CoolifyWorkloadSpec struct {
	ProjectName, ProjectID, ServerID, ServiceID string
	Compose, ArtifactDigest, EnvironmentDigest  string
}

var coolifyProviderID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,125}$`)
var coolifyWorkloadDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func RenderCoolifyWorkloadOpenTofu(spec CoolifyWorkloadSpec) ([]byte, error) {
	if !composePayloadProjectPattern.MatchString(spec.ProjectName) ||
		!coolifyProviderID.MatchString(spec.ProjectID) || !coolifyProviderID.MatchString(spec.ServerID) ||
		!coolifyProviderID.MatchString(spec.ServiceID) || strings.TrimSpace(spec.Compose) == "" ||
		!coolifyWorkloadDigest.MatchString(spec.ArtifactDigest) || !coolifyWorkloadDigest.MatchString(spec.EnvironmentDigest) {
		return nil, errors.New("Coolify provider requires an exact held service, node recipe and workload digests")
	}
	q := func(value string) string {
		raw, _ := json.Marshal(value)
		return strings.ReplaceAll(strings.ReplaceAll(string(raw), "${", "$${"), "%{", "%%{")
	}
	return []byte(fmt.Sprintf(`terraform {
  required_version = "~> 1.12.0"
  required_providers {
    coolify = {
      source = "coolify-terraform/coolify"
      version = "%s"
    }
  }
}

# Authentication is delivered only to the provider process from node custody.
provider "coolify" {}

resource "coolify_service" "workload" {
  name = %s
  project_uuid = %s
  server_uuid = %s
  environment_name = "production"
  docker_compose_raw = %s
  instant_deploy = false
  connect_to_docker_network = false
  is_container_label_escape_enabled = false
  lifecycle {
    prevent_destroy = true
    postcondition {
      condition = self.uuid == %s
      error_message = "Coolify service identity changed; an explicit ownership transition is required."
    }
  }
}

# At the pinned Coolify 4.4.6, RestartService activates the saved recipe even
# when stopped. Delete of this action is a no-op; updates never delete services.
resource "coolify_resource_action" "activate" {
  resource_uuid = coolify_service.workload.uuid
  resource_type = "service"
  action = "restart"
  triggers = {
    recipe = sha256(coolify_service.workload.docker_compose_raw)
    artifact = %s
    environment = %s
  }
}
`, CoolifyProviderVersion, q(spec.ProjectName), q(spec.ProjectID), q(spec.ServerID), q(spec.Compose),
		q(spec.ServiceID), q(spec.ArtifactDigest), q(spec.EnvironmentDigest))), nil
}
