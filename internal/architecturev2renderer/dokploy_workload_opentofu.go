package architecturev2renderer

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const DokployProviderVersion = "1.10.0"

// DokployWorkloadSpec is the preflighted existing service, never a name-based
// adoption request. The executor supplies authentication and encrypted state.
type DokployWorkloadSpec struct {
	ProjectName, EnvironmentID, ServerID, ComposeID, AppName string
	ComposePath, ComposeSHA256                               string
	Description                                              string
}

var dokployProviderID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var dokployPhysicalProject = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,55}-[a-z]{6}$`)
var dokployComposeDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// RenderDokployWorkloadOpenTofu uses the published provider's own deployment
// lifecycle. Unlike Komodo it needs no Terraform Action or API action bridge.
// This prerequisite imports only a held service and blocks replacement and
// deletion until the real-host transition/removal contract has parity.
func RenderDokployWorkloadOpenTofu(spec DokployWorkloadSpec) ([]byte, error) {
	directory := filepath.Dir(spec.ComposePath)
	nodeSource := filepath.Base(directory) == spec.ProjectName ||
		(filepath.Base(directory) == "opentofu" && filepath.Base(filepath.Dir(directory)) == spec.ProjectName)
	if !composePayloadProjectPattern.MatchString(spec.ProjectName) ||
		!dokployProviderID.MatchString(spec.EnvironmentID) || !dokployProviderID.MatchString(spec.ComposeID) ||
		(spec.ServerID != "" && !dokployProviderID.MatchString(spec.ServerID)) ||
		!dokployPhysicalProject.MatchString(spec.AppName) || !dokployComposeDigest.MatchString(spec.ComposeSHA256) ||
		!filepath.IsAbs(spec.ComposePath) || filepath.Clean(spec.ComposePath) != spec.ComposePath ||
		filepath.Base(spec.ComposePath) != "compose.yaml" || !nodeSource {
		return nil, errors.New("Dokploy provider root requires the exact custodied service, physical project and generated node Compose path/digest")
	}
	q := func(value string) string {
		raw, _ := json.Marshal(value)
		return strings.ReplaceAll(strings.ReplaceAll(string(raw), "${", "$${"), "%{", "%%{")
	}
	server := ""
	if spec.ServerID != "" {
		server = "  server_id = " + q(spec.ServerID) + "\n"
	}
	prefix := spec.AppName[:len(spec.AppName)-7]
	description := spec.Description
	if description == "" {
		description = "Managed by StackKits recipe " + spec.ComposeSHA256
	}
	return []byte(fmt.Sprintf(`terraform {
  required_version = "~> 1.12.0"
  required_providers {
    dokploy = {
      source = "vanillauys/dokploy"
      version = "%s"
    }
  }
  # The existing executor supplies the method through TF_ENCRYPTION.
  # Preparation must never become a plaintext-state execution path.
  encryption {
    state { enforced = true }
    plan { enforced = true }
  }
}

# DOKPLOY_ENDPOINT and DOKPLOY_API_KEY come from encrypted node custody.
provider "dokploy" {}

locals {
  compose_path = %s
  compose_sha256 = %s
}

import {
  to = dokploy_compose.workload
  id = %s
}

resource "dokploy_compose" "workload" {
  name = %s
  description = %s
  app_name_prefix = %s
  environment_id = %s
%s  compose_type = "docker-compose"
  raw = { compose_file = file(local.compose_path) }
  auto_deploy = false
  deploy_on_change = true
  create_env_file = false
  delete_volumes = false
  fresh_volumes = false
  randomize = false
  lifecycle {
    prevent_destroy = true
    precondition {
      condition = filesha256(local.compose_path) == local.compose_sha256
      error_message = "Dokploy Compose source changed after authenticated preparation; prepare it again."
    }
    postcondition {
      condition = self.id == %s && self.app_name == %s
      error_message = "Dokploy physical identity changed; explicit reconciliation is required."
    }
  }
}
`, DokployProviderVersion, q(spec.ComposePath), q(spec.ComposeSHA256), q(spec.ComposeID), q(spec.ProjectName), q(description), q(prefix), q(spec.EnvironmentID), server,
		q(spec.ComposeID), q(spec.AppName))), nil
}
