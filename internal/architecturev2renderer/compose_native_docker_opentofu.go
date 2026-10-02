package architecturev2renderer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	"gopkg.in/yaml.v3"
)

// Stage 2 pilot of ADR-0045 (addendum 2026-10-01): a workload root whose
// containers, networks, volumes and images are native kreuzwerker/docker
// resources instead of the Stage 1 Compose wrapper. The root is translated
// from the same closed workload Compose document the wrapper embeds, so both
// targets describe the same containers; any Compose key outside the closed
// pilot subset fails closed.
const (
	// NativeDockerProviderVersion is the exact kreuzwerker/docker release
	// every native root pins; offline packaging mirrors exactly this one.
	NativeDockerProviderVersion = "4.6.0"
	// NativeDockerWaitTimeoutSeconds bounds the provider's health wait, like
	// the wrapper's `docker compose up --wait-timeout`.
	NativeDockerWaitTimeoutSeconds = 600
	// NativeDockerSecretDir is the owner-only directory beside compose.yaml
	// that holds one secret env file per container.
	NativeDockerSecretDir = "secrets"
	// nativeDockerSecretMountDir is where a container reads its env file.
	nativeDockerSecretMountDir = "/run/stackkit/env"
	// nativeDockerStopGraceSeconds matches Compose's default stop timeout.
	nativeDockerStopGraceSeconds = 10
	// NativeDockerParityGapOOMScoreAdj names the governed OOM bias the
	// provider cannot express (no oom_score_adj attribute in 4.6.0).
	NativeDockerParityGapOOMScoreAdj = "oom_score_adj"
)

// nativeDockerPilotModules is the closed set of modules admitted to the
// native pilot, with the variable through which each container reads its
// secret env file. A container with secret environment values and no entry
// here cannot run natively: its values would otherwise enter the state.
var nativeDockerPilotModules = map[string]map[string]string{
	// Vaultwarden loads ENV_FILE with dotenv semantics before it reads the
	// process environment (single-quoted values are literal).
	"stackkits-vaultwarden-runtime": {"vaultwarden": "ENV_FILE"},
}

// NativeDockerPilotModule reports whether moduleRef has a native renderer.
func NativeDockerPilotModule(moduleRef string) bool {
	_, ok := nativeDockerPilotModules[moduleRef]
	return ok
}

// NativeDockerSpec is the complete input of one native workload root.
type NativeDockerSpec struct {
	ModuleRef   string
	ProjectName string
	// Compose is the exact workload Compose document the wrapper would embed.
	Compose []byte
	// Wait makes the provider wait for container health, like `up --wait`.
	Wait bool
}

// NativeDockerSecretBinding maps one container variable to the private .env
// interpolation variable that holds its value.
type NativeDockerSecretBinding struct {
	Name     string
	Variable string
}

// NativeDockerSecretEnvFile is one per-container secret env file the
// executor writes from the private .env before apply; the root only mounts
// it and records its digest.
type NativeDockerSecretEnvFile struct {
	Service  string
	RelPath  string
	Bindings []NativeDockerSecretBinding
}

// NativeDockerRoot is a rendered native root and what it needs beside it.
type NativeDockerRoot struct {
	Config         []byte
	SecretEnvFiles []NativeDockerSecretEnvFile
	// ParityGaps lists governed Compose settings the provider cannot express.
	ParityGaps []string
}

type nativeComposeDocument struct {
	Name     string                          `yaml:"name"`
	Services map[string]nativeComposeService `yaml:"services"`
	Networks map[string]nativeComposeNetwork `yaml:"networks"`
	Volumes  map[string]map[string]any       `yaml:"volumes"`
}

type nativeComposeService struct {
	Image   string `yaml:"image"`
	Restart string `yaml:"restart"`
	Logging *struct {
		Driver  string            `yaml:"driver"`
		Options map[string]string `yaml:"options"`
	} `yaml:"logging"`
	OOMScoreAdj *int `yaml:"oom_score_adj"`
	Deploy      *struct {
		Resources struct {
			Limits       *nativeComposeBounds `yaml:"limits"`
			Reservations *nativeComposeBounds `yaml:"reservations"`
		} `yaml:"resources"`
	} `yaml:"deploy"`
	Environment map[string]string `yaml:"environment"`
	Volumes     []string          `yaml:"volumes"`
	Networks    []string          `yaml:"networks"`
	Ports       []string          `yaml:"ports"`
	ExtraHosts  []string          `yaml:"extra_hosts"`
	Labels      map[string]string `yaml:"labels"`
}

type nativeComposeBounds struct {
	Memory string `yaml:"memory"`
}

type nativeComposeNetwork struct {
	Name     string `yaml:"name"`
	External bool   `yaml:"external"`
	Internal bool   `yaml:"internal"`
}

var (
	nativeSecretReferencePattern = regexp.MustCompile(`^\$\{([A-Z][A-Z0-9_]{0,127}):\?required\}$`)
	nativeIdentifierPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	nativeBindSourcePattern      = regexp.MustCompile(`^\./[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
	nativeLoopbackPortPattern    = regexp.MustCompile(`^127\.0\.0\.1::([0-9]{1,5})$`)
	nativeMemoryPattern          = regexp.MustCompile(`^([1-9][0-9]{0,6})([mg])$`)
	nativeResourceNamePattern    = regexp.MustCompile(`[^a-z0-9_]`)
)

// RenderNativeDockerOpenTofu translates one closed workload Compose document
// into a native root. Secret environment values (`${VAR:?required}`) are
// bound to a mounted env file; literal values keep Compose's `$$` escape
// semantics. Containers keep the Compose project labels, so the StackKits
// observation, backup quiesce and removal owners keep finding them.
//
//nolint:gocyclo // One closed translation boundary; every branch fails closed.
func RenderNativeDockerOpenTofu(spec NativeDockerSpec) (NativeDockerRoot, error) {
	path := "renderer.native-docker"
	readers, admitted := nativeDockerPilotModules[spec.ModuleRef]
	if !admitted {
		return NativeDockerRoot{}, fail(ErrRendererFailure, path+".moduleRef", "module %q has no native Docker rendering", spec.ModuleRef)
	}
	if !composePayloadProjectPattern.MatchString(spec.ProjectName) {
		return NativeDockerRoot{}, fail(ErrRendererFailure, path+".projectName", "must be a Docker Compose project name")
	}
	var document nativeComposeDocument
	decoder := yaml.NewDecoder(bytes.NewReader(spec.Compose))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return NativeDockerRoot{}, wrap(ErrRendererFailure, path+".compose", "decode the closed pilot Compose subset", err)
	}
	if document.Name != spec.ProjectName || len(document.Services) == 0 {
		return NativeDockerRoot{}, fail(ErrRendererFailure, path+".compose.name", "Compose project differs from the workload project")
	}

	file := hclwrite.NewEmptyFile()
	body := file.Body()
	terraform := body.AppendNewBlock("terraform", nil).Body()
	terraform.SetAttributeValue("required_version", cty.StringVal(ComposePayloadOpenTofuRequiredVersion))
	terraform.AppendNewBlock("required_providers", nil).Body().SetAttributeValue("docker", cty.ObjectVal(map[string]cty.Value{
		"source": cty.StringVal("kreuzwerker/docker"), "version": cty.StringVal("= " + NativeDockerProviderVersion),
	}))
	body.AppendNewline()
	body.AppendNewBlock("provider", []string{"docker"})

	networkRefs := map[string]hclwrite.Tokens{}
	for _, key := range sortedKeys(document.Networks) {
		network := document.Networks[key]
		if !nativeIdentifierPattern.MatchString(key) {
			return NativeDockerRoot{}, fail(ErrRendererFailure, path+".networks", "network %q is not a portable name", key)
		}
		if network.External {
			if network.Name == "" || network.Internal || !nativeIdentifierPattern.MatchString(network.Name) {
				return NativeDockerRoot{}, fail(ErrRendererFailure, path+".networks."+key, "an external network needs exactly one portable name")
			}
			// Owned by the core root; referenced by name only.
			networkRefs[key] = hclwrite.TokensForValue(cty.StringVal(network.Name))
			continue
		}
		if network.Name != "" {
			return NativeDockerRoot{}, fail(ErrRendererFailure, path+".networks."+key, "a project network carries no explicit name")
		}
		resource := nativeResourceName("network", key)
		body.AppendNewline()
		block := body.AppendNewBlock("resource", []string{"docker_network", resource}).Body()
		block.SetAttributeValue("name", cty.StringVal(spec.ProjectName+"_"+key))
		if network.Internal {
			block.SetAttributeValue("internal", cty.True)
		}
		appendNativeLabels(block, map[string]string{
			"com.docker.compose.project": spec.ProjectName, "com.docker.compose.network": key,
		})
		networkRefs[key] = hclwrite.TokensForTraversal(hcl.Traversal{
			hcl.TraverseRoot{Name: "docker_network"}, hcl.TraverseAttr{Name: resource}, hcl.TraverseAttr{Name: "name"},
		})
	}

	volumeRefs := map[string]hclwrite.Tokens{}
	for _, key := range sortedKeys(document.Volumes) {
		if !nativeIdentifierPattern.MatchString(key) || len(document.Volumes[key]) != 0 {
			return NativeDockerRoot{}, fail(ErrRendererFailure, path+".volumes", "volume %q must be a plain project volume", key)
		}
		resource := nativeResourceName("volume", key)
		body.AppendNewline()
		block := body.AppendNewBlock("resource", []string{"docker_volume", resource}).Body()
		block.SetAttributeValue("name", cty.StringVal(spec.ProjectName+"_"+key))
		appendNativeLabels(block, map[string]string{
			"com.docker.compose.project": spec.ProjectName, "com.docker.compose.volume": key,
		})
		// Data outlives the root like `docker compose down` without -v, and a
		// volume Compose created (with its per-version labels) is adopted
		// instead of replaced. Destroy runs with -suppress-forget-errors.
		lifecycle := block.AppendNewBlock("lifecycle", nil).Body()
		lifecycle.SetAttributeValue("destroy", cty.False)
		lifecycle.SetAttributeRaw("ignore_changes", hclwrite.TokensForTuple([]hclwrite.Tokens{hclwrite.TokensForIdentifier("labels")}))
		volumeRefs[key] = hclwrite.TokensForTraversal(hcl.Traversal{
			hcl.TraverseRoot{Name: "docker_volume"}, hcl.TraverseAttr{Name: resource}, hcl.TraverseAttr{Name: "name"},
		})
	}

	root := NativeDockerRoot{}
	gaps := map[string]struct{}{}
	for _, name := range sortedKeys(document.Services) {
		service := document.Services[name]
		servicePath := path + ".services." + name
		if !nativeIdentifierPattern.MatchString(name) {
			return NativeDockerRoot{}, fail(ErrRendererFailure, servicePath, "service name is not portable")
		}
		ref, digest, pinned := strings.Cut(service.Image, "@sha256:")
		if !pinned || ref == "" || len(digest) != 64 {
			return NativeDockerRoot{}, fail(ErrRendererFailure, servicePath+".image", "image must be pinned by digest")
		}
		imageResource := nativeResourceName("image", name)
		body.AppendNewline()
		image := body.AppendNewBlock("resource", []string{"docker_image", imageResource}).Body()
		image.SetAttributeValue("name", cty.StringVal(service.Image))
		image.SetAttributeValue("keep_locally", cty.True)

		environment := []string{}
		var secrets []NativeDockerSecretBinding
		for _, key := range sortedKeys(service.Environment) {
			value := service.Environment[key]
			if match := nativeSecretReferencePattern.FindStringSubmatch(value); match != nil {
				secrets = append(secrets, NativeDockerSecretBinding{Name: key, Variable: match[1]})
				continue
			}
			literal, err := unescapeComposeLiteral(value)
			if err != nil {
				return NativeDockerRoot{}, wrap(ErrRendererFailure, servicePath+".environment."+key, "environment value is not a literal", err)
			}
			environment = append(environment, key+"="+literal)
		}

		body.AppendNewline()
		block := body.AppendNewBlock("resource", []string{"docker_container", nativeResourceName("container", name)}).Body()
		block.SetAttributeValue("name", cty.StringVal(spec.ProjectName+"-"+name+"-1"))
		// The pinned reference, not the image ID: the container then reports
		// ref@digest like a Compose container, which the StackKits
		// observation compares with the governed digest.
		block.SetAttributeTraversal("image", hcl.Traversal{
			hcl.TraverseRoot{Name: "docker_image"}, hcl.TraverseAttr{Name: imageResource}, hcl.TraverseAttr{Name: "name"},
		})
		if service.Restart != "" {
			if service.Restart != "unless-stopped" && service.Restart != "no" && service.Restart != "always" && service.Restart != "on-failure" {
				return NativeDockerRoot{}, fail(ErrRendererFailure, servicePath+".restart", "unsupported restart policy")
			}
			block.SetAttributeValue("restart", cty.StringVal(service.Restart))
		}
		if spec.Wait {
			block.SetAttributeValue("wait", cty.True)
			block.SetAttributeValue("wait_timeout", cty.NumberIntVal(NativeDockerWaitTimeoutSeconds))
		}
		block.SetAttributeValue("destroy_grace_seconds", cty.NumberIntVal(nativeDockerStopGraceSeconds))
		if service.Deploy != nil {
			if limits := service.Deploy.Resources.Limits; limits != nil && limits.Memory != "" {
				megabytes, err := nativeMemoryMegabytes(limits.Memory)
				if err != nil {
					return NativeDockerRoot{}, wrap(ErrRendererFailure, servicePath+".deploy", "memory limit", err)
				}
				block.SetAttributeValue("memory", cty.NumberIntVal(megabytes))
				// Docker's effective default when only a limit is set; rendering
				// it keeps a re-plan empty.
				block.SetAttributeValue("memory_swap", cty.NumberIntVal(2*megabytes))
			}
			if reservations := service.Deploy.Resources.Reservations; reservations != nil && reservations.Memory != "" {
				megabytes, err := nativeMemoryMegabytes(reservations.Memory)
				if err != nil {
					return NativeDockerRoot{}, wrap(ErrRendererFailure, servicePath+".deploy", "memory reservation", err)
				}
				block.SetAttributeValue("memory_reservation", cty.NumberIntVal(megabytes))
			}
		}
		if service.Logging != nil {
			block.SetAttributeValue("log_driver", cty.StringVal(service.Logging.Driver))
			if len(service.Logging.Options) > 0 {
				options := map[string]cty.Value{}
				for key, value := range service.Logging.Options {
					options[key] = cty.StringVal(value)
				}
				block.SetAttributeValue("log_opts", cty.MapVal(options))
			}
		}
		if service.OOMScoreAdj != nil && *service.OOMScoreAdj != 0 {
			gaps[NativeDockerParityGapOOMScoreAdj] = struct{}{}
		}

		labels := map[string]string{}
		for key, value := range service.Labels {
			if strings.HasPrefix(key, "com.docker.compose.") {
				return NativeDockerRoot{}, fail(ErrRendererFailure, servicePath+".labels", "the workload may not set Compose-owned labels")
			}
			labels[key] = value
		}
		serviceJSON, err := json.Marshal(service)
		if err != nil {
			return NativeDockerRoot{}, wrap(ErrRendererFailure, servicePath, "digest service", err)
		}
		serviceDigest := sha256.Sum256(serviceJSON)
		labels["com.docker.compose.project"] = spec.ProjectName
		labels["com.docker.compose.service"] = name
		labels["com.docker.compose.container-number"] = "1"
		labels["com.docker.compose.oneoff"] = "False"
		// Compose lists a container only when it carries a config hash.
		labels["com.docker.compose.config-hash"] = hex.EncodeToString(serviceDigest[:])

		if len(secrets) > 0 {
			variable, governed := readers[name]
			if !governed {
				return NativeDockerRoot{}, fail(ErrRendererFailure, servicePath+".environment", "service has secret values but no governed file-based secret input")
			}
			if _, conflict := service.Environment[variable]; conflict {
				return NativeDockerRoot{}, fail(ErrRendererFailure, servicePath+".environment", "secret env file variable conflicts with a declared variable")
			}
			relPath := NativeDockerSecretDir + "/" + name + ".env"
			target := nativeDockerSecretMountDir + "/" + name + ".env"
			environment = append(environment, variable+"="+target)
			sort.Strings(environment)
			root.SecretEnvFiles = append(root.SecretEnvFiles, NativeDockerSecretEnvFile{Service: name, RelPath: relPath, Bindings: secrets})
			appendNativeBindMount(block, relPath, target)
		}
		envValues := make([]cty.Value, 0, len(environment))
		for _, entry := range environment {
			envValues = append(envValues, cty.StringVal(entry))
		}
		if len(envValues) > 0 {
			block.SetAttributeValue("env", cty.SetVal(envValues))
		}

		for _, mount := range service.Volumes {
			parts := strings.Split(mount, ":")
			readOnly := false
			if len(parts) == 3 && parts[2] == "ro" {
				readOnly = true
				parts = parts[:2]
			}
			if len(parts) != 2 || !strings.HasPrefix(parts[1], "/") {
				return NativeDockerRoot{}, fail(ErrRendererFailure, servicePath+".volumes", "unsupported volume form %q", mount)
			}
			if nativeBindSourcePattern.MatchString(parts[0]) && !strings.Contains(parts[0], "..") {
				if !readOnly {
					return NativeDockerRoot{}, fail(ErrRendererFailure, servicePath+".volumes", "a project file mount must be read-only")
				}
				appendNativeBindMount(block, strings.TrimPrefix(parts[0], "./"), parts[1])
				continue
			}
			source, known := volumeRefs[parts[0]]
			if !known {
				return NativeDockerRoot{}, fail(ErrRendererFailure, servicePath+".volumes", "volume %q is not a project volume", parts[0])
			}
			mounts := block.AppendNewBlock("mounts", nil).Body()
			mounts.SetAttributeValue("type", cty.StringVal("volume"))
			mounts.SetAttributeRaw("source", source)
			mounts.SetAttributeValue("target", cty.StringVal(parts[1]))
			if readOnly {
				mounts.SetAttributeValue("read_only", cty.True)
			}
		}
		networks := append([]string(nil), service.Networks...)
		sort.Strings(networks)
		for _, network := range networks {
			reference, known := networkRefs[network]
			if !known {
				return NativeDockerRoot{}, fail(ErrRendererFailure, servicePath+".networks", "network %q is not declared", network)
			}
			block.AppendNewBlock("networks_advanced", nil).Body().SetAttributeRaw("name", reference)
		}
		for _, port := range service.Ports {
			match := nativeLoopbackPortPattern.FindStringSubmatch(port)
			number, err := strconv.Atoi(firstSubmatch(match))
			if match == nil || err != nil || number < 1 || number > 65535 {
				return NativeDockerRoot{}, fail(ErrRendererFailure, servicePath+".ports", "only the loopback health port form is admitted, got %q", port)
			}
			ports := block.AppendNewBlock("ports", nil).Body()
			ports.SetAttributeValue("internal", cty.NumberIntVal(int64(number)))
			ports.SetAttributeValue("ip", cty.StringVal("127.0.0.1"))
		}
		for _, entry := range service.ExtraHosts {
			host, address, ok := strings.Cut(entry, ":")
			if !ok || host == "" || address == "" {
				return NativeDockerRoot{}, fail(ErrRendererFailure, servicePath+".extra_hosts", "malformed host entry")
			}
			hosts := block.AppendNewBlock("host", nil).Body()
			hosts.SetAttributeValue("host", cty.StringVal(host))
			hosts.SetAttributeValue("ip", cty.StringVal(address))
		}
		for _, file := range root.SecretEnvFiles {
			if file.Service != name {
				continue
			}
			// A rotated secret replaces the container, like Compose recreating
			// a service whose interpolated environment changed. Only the
			// digest enters the state.
			digest := block.AppendNewBlock("labels", nil).Body()
			digest.SetAttributeValue("label", cty.StringVal("io.stackkit.secret-env-digest"))
			digest.SetAttributeRaw("value", nativeFileDigestTokens(file.RelPath))
		}
		appendNativeLabels(block, labels)
	}
	for gap := range gaps {
		root.ParityGaps = append(root.ParityGaps, gap)
	}
	sort.Strings(root.ParityGaps)
	root.Config = hclwrite.Format(file.Bytes())
	return root, nil
}

// RenderNativeDockerSecretEnvFile renders one container's secret env file
// from the private .env the native preparation wrote. Values are single
// quoted (dotenv literal form); a value that cannot be expressed literally
// fails closed instead of being reinterpreted.
func RenderNativeDockerSecretEnvFile(file NativeDockerSecretEnvFile, dotenv []byte) ([]byte, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(dotenv))
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		variable, value, ok := strings.Cut(line, "=")
		if !ok || variable == "" {
			return nil, fail(ErrRendererFailure, "renderer.native-docker.env", "private .env has a malformed line")
		}
		values[variable] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.native-docker.env", "read private .env", err)
	}
	var out bytes.Buffer
	for _, binding := range file.Bindings {
		value, ok := values[binding.Variable]
		if !ok || value == "" {
			return nil, fail(ErrRendererFailure, "renderer.native-docker.env", "private .env has no value for a bound secret")
		}
		if strings.ContainsAny(value, "'\r\n\x00") {
			return nil, fail(ErrRendererFailure, "renderer.native-docker.env", "secret value cannot be written as a literal dotenv value")
		}
		fmt.Fprintf(&out, "%s='%s'\n", binding.Name, value)
	}
	return out.Bytes(), nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func nativeResourceName(kind, key string) string {
	return kind + "_" + nativeResourceNamePattern.ReplaceAllString(key, "_")
}

func appendNativeLabels(block *hclwrite.Body, labels map[string]string) {
	for _, key := range sortedKeys(labels) {
		label := block.AppendNewBlock("labels", nil).Body()
		label.SetAttributeValue("label", cty.StringVal(key))
		label.SetAttributeValue("value", cty.StringVal(labels[key]))
	}
}

// nativeProjectPathTokens is abspath("${path.module}/../<rel>"): a file in
// the workload project directory beside compose.yaml.
func nativeProjectPathTokens(rel string) hclwrite.Tokens {
	return hclwrite.TokensForFunctionCall("abspath", nativeProjectTemplateTokens(rel))
}

func nativeFileDigestTokens(rel string) hclwrite.Tokens {
	return hclwrite.TokensForFunctionCall("filesha256", nativeProjectTemplateTokens(rel))
}

func nativeProjectTemplateTokens(rel string) hclwrite.Tokens {
	return hclwrite.Tokens{
		{Type: hclsyntax.TokenOQuote, Bytes: []byte(`"`)},
		{Type: hclsyntax.TokenTemplateInterp, Bytes: []byte(`${`)},
		{Type: hclsyntax.TokenIdent, Bytes: []byte(`path.module`)},
		{Type: hclsyntax.TokenTemplateSeqEnd, Bytes: []byte(`}`)},
		{Type: hclsyntax.TokenQuotedLit, Bytes: []byte("/../" + escapeHCLTemplateLiteral(rel))},
		{Type: hclsyntax.TokenCQuote, Bytes: []byte(`"`)},
	}
}

func appendNativeBindMount(block *hclwrite.Body, rel, target string) {
	mounts := block.AppendNewBlock("mounts", nil).Body()
	mounts.SetAttributeValue("type", cty.StringVal("bind"))
	mounts.SetAttributeRaw("source", nativeProjectPathTokens(rel))
	mounts.SetAttributeValue("target", cty.StringVal(target))
	mounts.SetAttributeValue("read_only", cty.True)
}

func nativeMemoryMegabytes(value string) (int64, error) {
	match := nativeMemoryPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, fmt.Errorf("unsupported memory value %q", value)
	}
	amount, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, err
	}
	if match[2] == "g" {
		amount *= 1024
	}
	return amount, nil
}

// unescapeComposeLiteral undoes the renderer's `$` → `$$` escape; any other
// `$` would be a Compose interpolation and is refused.
func unescapeComposeLiteral(value string) (string, error) {
	var out strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '$' {
			out.WriteByte(value[index])
			continue
		}
		if index+1 < len(value) && value[index+1] == '$' {
			out.WriteByte('$')
			index++
			continue
		}
		return "", fmt.Errorf("unescaped interpolation")
	}
	return out.String(), nil
}

func firstSubmatch(match []string) string {
	if len(match) < 2 {
		return ""
	}
	return match[1]
}
