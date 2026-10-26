package architecturev2renderer

import (
	"errors"
	"fmt"
	"regexp"
)

var runtipiActionDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// RenderRuntipiWorkloadOpenTofu orders the existing node-owned API operation.
// Runtipi has no packaged provider: this is an explicit API bridge, not a
// provider-resource claim. Only public identities and a request digest enter
// configuration/state. The signed request and credentials remain in node custody.
func RenderRuntipiWorkloadOpenTofu(project, requestDigest string) ([]byte, error) {
	if !composePayloadProjectPattern.MatchString(project) || len(project) > 50 || !runtipiActionDigest.MatchString(requestDigest) {
		return nil, errors.New("Runtipi API root requires its exact node project and signed request digest")
	}
	return []byte(fmt.Sprintf(`terraform {
  required_version = "~> 1.12.0"
}

# API ownership remains with the existing native Runtipi owner. OpenTofu
# orders that real operation; it does not merely record its digest afterward.
resource "terraform_data" "runtipi_workload" {
  triggers_replace = [%q]
  input = {
    project = %q
    request_digest = %q
  }
  provisioner "local-exec" {
    command = "\"$STACKKIT_RUNTIPI_BRIDGE_BINARY\" runtipi-workload-action --project ${self.input.project} --request-digest ${self.input.request_digest}"
    quiet = true
  }
}
`, requestDigest, project, requestDigest)), nil
}
