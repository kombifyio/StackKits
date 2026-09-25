package architecturev2renderer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// Stage 1 of ADR-0045: every Compose artifact has an OpenTofu twin that embeds
// the byte-identical Compose payload and applies it with `docker compose up
// --wait`. The OpenTofu root is installed by the executor at
// .stackkit/runtime/<runtime>/opentofu/main.tf and owns ../compose.yaml, which
// is the same file and Compose project the native executor manages. Applying
// the root over an existing native install therefore rewrites identical bytes
// and runs a no-op `up`, adopting the install into OpenTofu state without
// recreating containers.
const (
	// ComposePayloadLocalProviderVersion is the exact hashicorp/local release
	// every wrapper root pins, so offline packaging can mirror one provider.
	ComposePayloadLocalProviderVersion = "2.5.3"
	// ComposePayloadOpenTofuRequiredVersion is the minimum OpenTofu release.
	ComposePayloadOpenTofuRequiredVersion = ">= 1.10.0"
	// ComposePayloadWaitTimeoutSeconds bounds `docker compose up --wait`.
	ComposePayloadWaitTimeoutSeconds = 600

	composePayloadHeredocDelimiter = "YAML"
)

var (
	composePayloadResourcePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	composePayloadProjectPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	composePayloadEnvFilePattern  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

// ComposePayloadSpec is the complete input of one Stage 1 wrapper root.
type ComposePayloadSpec struct {
	// ResourcePrefix names the OpenTofu resources (HCL identifier).
	ResourcePrefix string
	// ProjectName is the `docker compose --project-name` the native executor
	// uses for the same runtime directory.
	ProjectName string
	// Compose is the exact Compose artifact. It must end with a newline.
	Compose []byte
	// EnvFile optionally names a file beside compose.yaml that the native
	// executor passes as `--env-file` (standalone workload projects). The
	// root references the file and replaces its apply when the file's digest
	// changes; the content never enters the root or its state.
	EnvFile string
	// NoWait drops `--wait` from `up`, mirroring the native executor for a
	// project with a component that may run degraded.
	NoWait bool
}

// RenderComposePayloadOpenTofu emits one OpenTofu root that writes the Compose
// payload to ../compose.yaml and applies it with Docker Compose. The payload is
// embedded in a plain heredoc with only HCL template escapes (`${` and `%{`),
// so OpenTofu evaluates `content` back to exactly spec.Compose.
//
// The root has three deterministic resources:
//
//   - local_file.<prefix>_compose writes the payload.
//   - terraform_data.<prefix>_up carries the payload (and env file) digests
//     in triggers_replace and runs only the create-time `up`. It has no
//     destroy provisioner, so a payload change replaces it with a single `up`
//     that recreates only the changed services, like the native executor.
//     Rollback targets this address with `-replace`.
//   - terraform_data.<prefix>_lifecycle has no triggers and runs `down`
//     (never removing volumes) only when the root is destroyed. It depends on
//     <prefix>_up, so `tofu destroy` runs `down` before anything else goes.
func RenderComposePayloadOpenTofu(spec ComposePayloadSpec) ([]byte, error) {
	if !composePayloadResourcePattern.MatchString(spec.ResourcePrefix) {
		return nil, fail(ErrRendererFailure, "renderer.compose-payload.resourcePrefix", "must be a lowercase HCL identifier")
	}
	if !composePayloadProjectPattern.MatchString(spec.ProjectName) {
		return nil, fail(ErrRendererFailure, "renderer.compose-payload.projectName", "must be a Docker Compose project name")
	}
	if spec.EnvFile != "" && (!composePayloadEnvFilePattern.MatchString(spec.EnvFile) || spec.EnvFile == "." || spec.EnvFile == "..") {
		return nil, fail(ErrRendererFailure, "renderer.compose-payload.envFile", "must be a plain file name beside compose.yaml")
	}
	if err := validateComposePayload(spec.Compose); err != nil {
		return nil, err
	}
	envFileArg := ""
	if spec.EnvFile != "" {
		envFileArg = ` --env-file \"${self.input.project_dir}/` + spec.EnvFile + `\"`
	}
	compose := "docker compose --project-name " + spec.ProjectName + envFileArg + ` -f \"${self.input.compose_file}\"`
	triggers := "sha256(local_file." + spec.ResourcePrefix + "_compose.content)"
	if spec.EnvFile != "" {
		triggers += `, filesha256("${path.module}/../` + spec.EnvFile + `")`
	}
	up := fmt.Sprintf("up -d --wait --wait-timeout %d", ComposePayloadWaitTimeoutSeconds)
	if spec.NoWait {
		up = "up -d"
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, `terraform {
  required_version = %q
  required_providers {
    local = {
      source  = "hashicorp/local"
      version = "= %s"
    }
  }
}

resource "local_file" "%[3]s_compose" {
  filename             = "${path.module}/../compose.yaml"
  file_permission      = "0600"
  directory_permission = "0700"
  content              = <<%[4]s
%[5]s%[4]s
}

resource "terraform_data" "%[3]s_up" {
  triggers_replace = [%[8]s]

  input = {
    compose_file = abspath(local_file.%[3]s_compose.filename)
    project_dir  = abspath("${path.module}/..")
    custody_dir  = abspath("${path.module}/../../../custody")
  }

  provisioner "local-exec" {
    working_dir = self.input.project_dir
    command     = "%[6]s %[7]s"
    environment = {
      STACKKIT_CUSTODY_DIR = self.input.custody_dir
    }
  }
}

resource "terraform_data" "%[3]s_lifecycle" {
  depends_on = [terraform_data.%[3]s_up]

  input = {
    compose_file = abspath(local_file.%[3]s_compose.filename)
    project_dir  = abspath("${path.module}/..")
    custody_dir  = abspath("${path.module}/../../../custody")
  }

  provisioner "local-exec" {
    when        = destroy
    working_dir = self.input.project_dir
    command     = "%[6]s down"
    environment = {
      STACKKIT_CUSTODY_DIR = self.input.custody_dir
    }
  }
}
`, ComposePayloadOpenTofuRequiredVersion, ComposePayloadLocalProviderVersion, spec.ResourcePrefix,
		composePayloadHeredocDelimiter, escapeHCLTemplateLiteral(string(spec.Compose)), compose, up, triggers)
	return out.Bytes(), nil
}

// ExtractComposePayload returns the Compose payload a Stage 1 wrapper root
// writes: the literal content of its single local_file resource, recovered
// through a real HCL parse and evaluation without variables or functions.
// Because RenderComposePayloadOpenTofu embeds the Compose artifact byte for
// byte, the result is exactly the Compose artifact the compose target renders
// for the same plan. It fails closed on a root with no or more than one
// local_file, a non-literal content expression, or a payload the renderer
// would not have emitted.
func ExtractComposePayload(mainTF []byte) ([]byte, error) {
	file, diagnostics := hclsyntax.ParseConfig(mainTF, "main.tf", hcl.InitialPos)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("parse the OpenTofu root: %s", diagnostics.Error())
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil, errors.New("OpenTofu root has no native HCL body")
	}
	var payload []byte
	found := false
	for _, block := range body.Blocks {
		if block.Type != "resource" || len(block.Labels) != 2 || block.Labels[0] != "local_file" {
			continue
		}
		if found {
			return nil, errors.New("OpenTofu root declares more than one local_file payload")
		}
		found = true
		attribute, ok := block.Body.Attributes["content"]
		if !ok {
			return nil, errors.New("OpenTofu root local_file has no content")
		}
		value, diagnostics := attribute.Expr.Value(nil)
		if diagnostics.HasErrors() || !value.IsKnown() || value.IsNull() || !value.Type().IsPrimitiveType() ||
			value.Type().FriendlyName() != "string" {
			return nil, errors.New("OpenTofu root local_file content is not a literal payload")
		}
		payload = []byte(value.AsString())
	}
	if !found {
		return nil, errors.New("OpenTofu root declares no Compose payload")
	}
	if err := validateComposePayload(payload); err != nil {
		return nil, fmt.Errorf("OpenTofu root Compose payload: %w", err)
	}
	return payload, nil
}

func validateComposePayload(compose []byte) error {
	if len(compose) == 0 || compose[len(compose)-1] != '\n' {
		return fail(ErrRendererFailure, "renderer.compose-payload.compose", "payload must be non-empty and newline-terminated")
	}
	if bytes.IndexByte(compose, '\r') >= 0 {
		return fail(ErrRendererFailure, "renderer.compose-payload.compose", "payload must use LF line endings")
	}
	for _, line := range strings.Split(string(compose), "\n") {
		if strings.TrimSpace(line) == composePayloadHeredocDelimiter {
			return fail(ErrRendererFailure, "renderer.compose-payload.compose", "payload contains the heredoc delimiter line")
		}
	}
	return nil
}

// escapeHCLTemplateLiteral escapes the two HCL template introducers so the
// heredoc evaluates to the literal input.
func escapeHCLTemplateLiteral(value string) string {
	return strings.NewReplacer("${", "$${", "%{", "%%{").Replace(value)
}

// composePayloadOpenTofuRenderer renders a module's Compose artifact through
// the module's own Compose pipeline and wraps exactly those bytes.
type composePayloadOpenTofuRenderer struct {
	contract       RendererContract
	outputRef      string
	resourcePrefix string
	projectName    string
	compose        func(context.Context, RenderUnit) ([]byte, error)
}

func (r composePayloadOpenTofuRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	compose, err := r.compose(ctx, unit)
	if err != nil {
		return nil, err
	}
	root, err := RenderComposePayloadOpenTofu(ComposePayloadSpec{ResourcePrefix: r.resourcePrefix, ProjectName: r.projectName, Compose: compose})
	if err != nil {
		return nil, err
	}
	return []UnitOutput{{Ref: r.outputRef, Bytes: root}}, nil
}

var _ UnitRenderer = composePayloadOpenTofuRenderer{}
