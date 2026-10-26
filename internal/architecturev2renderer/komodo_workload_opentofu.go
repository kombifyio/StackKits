package architecturev2renderer

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// KomodoProviderVersion is the packaged provider pin. OpenTofu 1.12 has no
// Terraform Action support; terraform_data orders only the missing API action.
const KomodoProviderVersion = "0.12.0"

// KomodoWorkloadSpec contains public host recipe coordinates, never file or
// credential contents. HeldID validates the independent witness shape; the
// root reads it from the current signed intent so first creation does not
// change the root configuration on the next Apply.
type KomodoWorkloadSpec struct {
	ProjectName, Directory, ServerID, HeldID string
	IgnoredServices                          []string
}

var komodoObjectID = regexp.MustCompile(`^[0-9a-f]{24}$`)

func RenderKomodoWorkloadOpenTofu(spec KomodoWorkloadSpec) ([]byte, error) {
	if !composePayloadProjectPattern.MatchString(spec.ProjectName) || !filepath.IsAbs(spec.Directory) ||
		filepath.Clean(spec.Directory) != spec.Directory || filepath.Base(spec.Directory) != spec.ProjectName ||
		!komodoObjectID.MatchString(spec.ServerID) || spec.HeldID != "" && !komodoObjectID.MatchString(spec.HeldID) {
		return nil, errors.New("Komodo provider root requires an exact host project, server and held stack identity")
	}
	ignored := spec.IgnoredServices
	if ignored == nil {
		ignored = []string{}
	}
	for _, service := range ignored {
		if !composePayloadProjectPattern.MatchString(service) {
			return nil, errors.New("invalid Komodo ignored service")
		}
	}
	q := func(value any) string {
		raw, _ := json.Marshal(value)
		return strings.ReplaceAll(strings.ReplaceAll(string(raw), "${", "$${"), "%{", "%%{")
	}
	// Lifecycle exists before deployment can start. Destroy reverses that edge:
	// deploy, then API teardown, then guarded configuration deletion. Updates replace only deploy,
	// which has no destroy provisioner and cannot interrupt the running stack.
	return []byte(fmt.Sprintf(`terraform {
  required_version = "~> 1.12.0"
  required_providers {
    komodo = {
      source = "sebastianfs82/komodo"
      version = "%s"
    }
  }
}

# Authentication is inherited from node custody in the provider process only.
provider "komodo" {}

resource "komodo_stack" "workload" {
  name = %s
  server_id = %s
  project_name = %s
  source {
    on_host_enabled = true
    directory = %s
    file_paths = ["compose.yaml"]
  }
  environment {
    file_path = ".env"
  }
  auto_pull_enabled = false
  destroy_mode_enabled = false
  poll_updates_enabled = false
  alerts_enabled = true
  ignore_services = %s
  provisioner "local-exec" {
    when = destroy
    command = "\"$STACKKIT_KOMODO_BRIDGE_BINARY\" komodo-workload-action verify-destroy --project ${self.project_name} --stack-id ${self.id}"
    quiet = true
  }
  lifecycle {
    postcondition {
      condition = jsondecode(file("${path.module}/komodo-action.json")).heldId == "" || self.id == jsondecode(file("${path.module}/komodo-action.json")).heldId
      error_message = "Komodo ID changed; explicit import/transition is required."
    }
  }
}

resource "terraform_data" "komodo_deploy" {
  depends_on = [terraform_data.komodo_lifecycle]
  triggers_replace = [
    komodo_stack.workload.id,
    filesha256("${path.module}/../compose.yaml"),
    filesha256("${path.module}/../.env"),
    filesha256("${path.module}/main.tf"),
    jsondecode(file("${path.module}/komodo-action.json")).deployment.artifactDigest,
  ]
  input = {
    stack_id = komodo_stack.workload.id
    project = %s
  }
  provisioner "local-exec" {
    command = "\"$STACKKIT_KOMODO_BRIDGE_BINARY\" komodo-workload-action deploy --project ${self.input.project} --stack-id ${self.input.stack_id}"
    quiet = true
  }
}

resource "terraform_data" "komodo_lifecycle" {
  depends_on = [komodo_stack.workload]
  input = {
    stack_id = komodo_stack.workload.id
    project = %s
  }
  provisioner "local-exec" {
    when = destroy
    command = "\"$STACKKIT_KOMODO_BRIDGE_BINARY\" komodo-workload-action destroy --project ${self.input.project} --stack-id ${self.input.stack_id}"
    quiet = true
  }
}
`, KomodoProviderVersion, q(spec.ProjectName), q(spec.ServerID), q(spec.ProjectName), q(spec.Directory), q(ignored), q(spec.ProjectName), q(spec.ProjectName))), nil
}
