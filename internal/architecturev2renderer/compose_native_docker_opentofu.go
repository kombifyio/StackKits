package architecturev2renderer

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	"gopkg.in/yaml.v3"

	"github.com/kombifyio/stackkits/internal/secretexec"
)

// Stage 2 of ADR-0045 (addendum 2026-10-01): a workload root whose
// containers, networks, volumes and images are native kreuzwerker/docker
// resources instead of the Stage 1 Compose wrapper. The root is translated
// from the typed workload Compose model (workload_compose_model.go) the
// wrapper's compose.yaml is rendered from, so both targets describe the same
// containers; any setting without an exact native translation fails closed.
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

type NativeDockerSpec struct {
	ModuleRef   string
	ProjectName string
	// Compose is the exact workload Compose document the wrapper would embed.
	Compose []byte
	// Wait makes the provider wait for container health, like `up --wait`.
	Wait bool
	// SecretExecBinary is the host path of the static governed entrypoint
	// shim (the stackkit binary, internal/secretexec). Only a container
	// whose governed reader is the shim needs it; without it such a root
	// fails closed.
	SecretExecBinary string
	// PilotNetworkAliases renders the S2.0 pilot's network attachments,
	// which lacked the Compose service aliases. Only Advanced restore uses
	// it, to recognise a checkpoint root the pilot release rendered.
	PilotNetworkAliases bool
}

// NativeDockerSecretBinding maps one container variable to the private .env
// interpolation variable that holds its value. In a YAML file Name is the
// dotted key path; in a dotenv file it is the variable the file sets.
type NativeDockerSecretBinding struct {
	Name     string
	Variable string
}

// Formats of a secret file.
const (
	// NativeDockerSecretFormatDotenv is one dotenv file with every binding.
	NativeDockerSecretFormatDotenv = "dotenv"
	// NativeDockerSecretFormatValue is exactly one value, no newline.
	NativeDockerSecretFormatValue = "value"
	// NativeDockerSecretFormatYAML is a YAML mapping of the bound values
	// under their dotted key paths, each a double-quoted scalar.
	NativeDockerSecretFormatYAML = "yaml"
	// NativeDockerSecretFormatPgpass is one libpq password file line that
	// matches every host, port, database and user.
	NativeDockerSecretFormatPgpass = "pgpass"
)

// NativeDockerSecretFile is one owner-only secret file the executor writes
// from the private .env before apply into the project's secrets directory;
// the root only mounts it read-only and records its digest.
type NativeDockerSecretFile struct {
	Service  string
	RelPath  string
	Format   string
	Bindings []NativeDockerSecretBinding
	// Uploaded marks a file the provider copies into the container (a
	// Compose secret file or a Files reader) at Target with Owner, Group
	// and Permissions instead of mounting it.
	Uploaded     bool
	Target       string
	Owner, Group string
	Permissions  int
}

// Mode is the file mode of the written secret file. A dotenv file is read
// by a root process (Vaultwarden); a mounted value file is read by the
// container's own user (postgres runs its entrypoint as uid 999), so it is
// readable inside the owner-only 0700 directory like a Compose secret
// (0444); host users cannot traverse to it. An uploaded file is read only by
// the OpenTofu process, which copies it in with the declared owner and mode.
func (f NativeDockerSecretFile) Mode() os.FileMode {
	if f.Uploaded {
		return 0o600
	}
	if f.Format == NativeDockerSecretFormatValue {
		return 0o444
	}
	return 0o600
}

// NativeDockerRoot is a rendered native root and what it needs beside it.
type NativeDockerRoot struct {
	Config      []byte
	SecretFiles []NativeDockerSecretFile
	// ParityGaps lists governed Compose settings the provider cannot express.
	ParityGaps []string
}

var (
	nativeSecretReferencePattern = regexp.MustCompile(`^\$\{([A-Z][A-Z0-9_]{0,127}):\?required\}$`)
	nativeSecretVariablePattern  = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
	nativeIdentifierPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	nativeBindSourcePattern      = regexp.MustCompile(`^\./[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
	nativeLoopbackPortPattern    = regexp.MustCompile(`^127\.0\.0\.1::([0-9]{1,5})$`)
	// A LAN listener or a published mail port: the same port on every host
	// address, as the Compose renderer emits it.
	nativeListenerPortPattern = regexp.MustCompile(`^([0-9]{1,5}):([0-9]{1,5})/(tcp|udp)$`)
	nativeDevicePattern       = regexp.MustCompile(`^(/dev/[A-Za-z0-9._/-]+):(/dev/[A-Za-z0-9._/-]+)$`)
	nativeMemoryPattern       = regexp.MustCompile(`^([1-9][0-9]{0,6})([mg])$`)
	nativeResourceNamePattern = regexp.MustCompile(`[^a-z0-9_]`)
	nativeDurationPattern     = regexp.MustCompile(`^[0-9]{1,6}(ms|s|m|h)$`)
	nativeStopSignalPattern   = regexp.MustCompile(`^SIG[A-Z0-9]{1,12}$`)
	nativeNumericIDPattern    = regexp.MustCompile(`^[0-9]{1,10}$`)
)

// nativeDockerTranslatedServiceFields are the WorkloadComposeService fields
// the native renderer translates. A service that sets any other field (a
// device, a sandbox runtime, a field the Compose renderer starts to emit
// later) fails closed instead of losing that setting.
var nativeDockerTranslatedServiceFields = map[string]struct{}{
	"Image": {}, "Restart": {}, "Logging": {}, "OOMScoreAdj": {}, "Deploy": {}, "Command": {}, "Entrypoint": {},
	"DependsOn": {}, "Environment": {}, "Volumes": {}, "Networks": {}, "Ports": {}, "ExtraHosts": {},
	"StopSignal": {}, "Init": {}, "Labels": {}, "Healthcheck": {}, "Secrets": {}, "Devices": {}, "User": {},
}

// nativeDockerRender is the state of one native root translation.
type nativeDockerRender struct {
	spec        NativeDockerSpec
	document    WorkloadComposeDocument
	body        *hclwrite.Body
	readers     map[string]nativeDockerSecretReader
	networkRefs map[string]hclwrite.Tokens
	volumeRefs  map[string]hclwrite.Tokens
	oneShot     map[string]bool
	root        NativeDockerRoot
	gaps        map[string]struct{}
}

// RenderNativeDockerOpenTofu translates one workload Compose document, decoded
// strictly into the typed workload model, into a native root. Secret
// environment values (`${VAR:?required}`) and Compose secret files are bound
// to owner-only files; literal values keep Compose's `$$` escape semantics.
// Containers keep the Compose project labels, so the StackKits observation,
// backup quiesce and removal owners keep finding them. Anything the provider
// cannot express exactly fails closed.
func RenderNativeDockerOpenTofu(spec NativeDockerSpec) (NativeDockerRoot, error) {
	path := "renderer.native-docker"
	if !composePayloadProjectPattern.MatchString(spec.ProjectName) {
		return NativeDockerRoot{}, fail(ErrRendererFailure, path+".projectName", "must be a Docker Compose project name")
	}
	render := &nativeDockerRender{
		spec: spec, readers: nativeDockerSecretReaders[spec.ModuleRef], networkRefs: map[string]hclwrite.Tokens{},
		volumeRefs: map[string]hclwrite.Tokens{}, oneShot: map[string]bool{}, gaps: map[string]struct{}{},
	}
	decoder := yaml.NewDecoder(bytes.NewReader(spec.Compose))
	decoder.KnownFields(true)
	if err := decoder.Decode(&render.document); err != nil {
		return NativeDockerRoot{}, wrap(ErrRendererFailure, path+".compose", "decode the typed workload Compose model", err)
	}
	if render.document.Name != spec.ProjectName || len(render.document.Services) == 0 {
		return NativeDockerRoot{}, fail(ErrRendererFailure, path+".compose.name", "Compose project differs from the workload project")
	}

	file := hclwrite.NewEmptyFile()
	render.body = file.Body()
	terraform := render.body.AppendNewBlock("terraform", nil).Body()
	terraform.SetAttributeValue("required_version", cty.StringVal(ComposePayloadOpenTofuRequiredVersion))
	terraform.AppendNewBlock("required_providers", nil).Body().SetAttributeValue("docker", cty.ObjectVal(map[string]cty.Value{
		"source": cty.StringVal("kreuzwerker/docker"), "version": cty.StringVal("= " + NativeDockerProviderVersion),
	}))
	render.body.AppendNewline()
	render.body.AppendNewBlock("provider", []string{"docker"})

	if err := render.networks(path); err != nil {
		return NativeDockerRoot{}, err
	}
	if err := render.volumes(path); err != nil {
		return NativeDockerRoot{}, err
	}
	for name, service := range render.document.Services {
		lifecycle := service.Labels[WorkloadComposeLifecycleLabel]
		if lifecycle != "" && lifecycle != "daemon" && lifecycle != WorkloadComposeLifecycleOnce {
			return NativeDockerRoot{}, fail(ErrRendererFailure, path+".services."+name+".labels", "unknown component lifecycle %q", lifecycle)
		}
		render.oneShot[name] = lifecycle == WorkloadComposeLifecycleOnce
	}
	for _, name := range sortedKeys(render.document.Services) {
		if err := render.service(path+".services."+name, name, render.document.Services[name]); err != nil {
			return NativeDockerRoot{}, err
		}
	}
	for gap := range render.gaps {
		render.root.ParityGaps = append(render.root.ParityGaps, gap)
	}
	sort.Strings(render.root.ParityGaps)
	render.root.Config = hclwrite.Format(file.Bytes())
	return render.root, nil
}

func (r *nativeDockerRender) networks(path string) error {
	for _, key := range sortedKeys(r.document.Networks) {
		network := r.document.Networks[key]
		if !nativeIdentifierPattern.MatchString(key) {
			return fail(ErrRendererFailure, path+".networks", "network %q is not a portable name", key)
		}
		if network.External {
			if network.Name == "" || network.Internal || !nativeIdentifierPattern.MatchString(network.Name) {
				return fail(ErrRendererFailure, path+".networks."+key, "an external network needs exactly one portable name")
			}
			// Owned by the core root or a peer workload; referenced by name only.
			r.networkRefs[key] = hclwrite.TokensForValue(cty.StringVal(network.Name))
			continue
		}
		if network.Name != "" {
			return fail(ErrRendererFailure, path+".networks."+key, "a project network carries no explicit name")
		}
		resource := nativeResourceName("network", key)
		r.body.AppendNewline()
		block := r.body.AppendNewBlock("resource", []string{"docker_network", resource}).Body()
		block.SetAttributeValue("name", cty.StringVal(r.spec.ProjectName+"_"+key))
		if network.Internal {
			block.SetAttributeValue("internal", cty.True)
		}
		appendNativeLabels(block, map[string]string{
			"com.docker.compose.project": r.spec.ProjectName, "com.docker.compose.network": key,
		})
		r.networkRefs[key] = nativeTraversal("docker_network", resource, "name")
	}
	return nil
}

func (r *nativeDockerRender) volumes(path string) error {
	for _, key := range sortedKeys(r.document.Volumes) {
		if !nativeIdentifierPattern.MatchString(key) || len(r.document.Volumes[key]) != 0 {
			return fail(ErrRendererFailure, path+".volumes", "volume %q must be a plain project volume", key)
		}
		resource := nativeResourceName("volume", key)
		r.body.AppendNewline()
		block := r.body.AppendNewBlock("resource", []string{"docker_volume", resource}).Body()
		block.SetAttributeValue("name", cty.StringVal(r.spec.ProjectName+"_"+key))
		appendNativeLabels(block, map[string]string{
			"com.docker.compose.project": r.spec.ProjectName, "com.docker.compose.volume": key,
		})
		// Data outlives the root like `docker compose down` without -v, and a
		// volume Compose created (with its per-version labels) is adopted
		// instead of replaced. Destroy runs with -suppress-forget-errors.
		lifecycle := block.AppendNewBlock("lifecycle", nil).Body()
		lifecycle.SetAttributeValue("destroy", cty.False)
		lifecycle.SetAttributeRaw("ignore_changes", hclwrite.TokensForTuple([]hclwrite.Tokens{hclwrite.TokensForIdentifier("labels")}))
		r.volumeRefs[key] = nativeTraversal("docker_volume", resource, "name")
	}
	return nil
}

// service renders one container. A daemon is kept running (and, with Wait,
// waited for until healthy, like `up --wait`); a one-shot component runs to
// completion during apply, and a non-zero exit fails the apply like Compose's
// service_completed_successfully.
//
//nolint:gocyclo // One closed translation boundary; every branch fails closed.
func (r *nativeDockerRender) service(servicePath, name string, service WorkloadComposeService) error {
	if !nativeIdentifierPattern.MatchString(name) {
		return fail(ErrRendererFailure, servicePath, "service name is not portable")
	}
	if field := nativeUntranslatedServiceField(service); field != "" {
		return fail(ErrRendererFailure, servicePath+"."+field, "Compose key has no native translation")
	}
	ref, digest, pinned := strings.Cut(service.Image, "@sha256:")
	if !pinned || ref == "" || len(digest) != 64 {
		return fail(ErrRendererFailure, servicePath+".image", "image must be pinned by digest")
	}
	oneShot := r.oneShot[name]
	imageResource := nativeResourceName("image", name)
	r.body.AppendNewline()
	image := r.body.AppendNewBlock("resource", []string{"docker_image", imageResource}).Body()
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
			return wrap(ErrRendererFailure, servicePath+".environment."+key, "environment value is not a literal", err)
		}
		environment = append(environment, key+"="+literal)
	}

	containerResource := nativeResourceName("container", name)
	r.body.AppendNewline()
	block := r.body.AppendNewBlock("resource", []string{"docker_container", containerResource}).Body()
	block.SetAttributeValue("name", cty.StringVal(r.spec.ProjectName+"-"+name+"-1"))
	// The pinned reference, not the image ID: the container then reports
	// ref@digest like a Compose container, which the StackKits observation
	// compares with the governed digest.
	block.SetAttributeTraversal("image", hcl.Traversal{
		hcl.TraverseRoot{Name: "docker_image"}, hcl.TraverseAttr{Name: imageResource}, hcl.TraverseAttr{Name: "name"},
	})
	if err := r.dependencies(servicePath, block, service); err != nil {
		return err
	}
	entrypoint, command, shim, err := r.shimStart(servicePath, name, digest, service, secrets)
	if err != nil {
		return err
	}
	for _, argument := range []struct {
		attribute string
		values    []string
	}{{"entrypoint", entrypoint}, {"command", command}} {
		if len(argument.values) == 0 {
			continue
		}
		list, err := nativeLiteralList(argument.values)
		if err != nil {
			return wrap(ErrRendererFailure, servicePath+"."+argument.attribute, "argument is not a literal", err)
		}
		block.SetAttributeValue(argument.attribute, list)
	}
	if service.Restart != "" {
		if service.Restart != "unless-stopped" && service.Restart != "no" && service.Restart != "always" && service.Restart != "on-failure" {
			return fail(ErrRendererFailure, servicePath+".restart", "unsupported restart policy")
		}
		if oneShot && service.Restart != "no" {
			return fail(ErrRendererFailure, servicePath+".restart", "a one-shot component is never restarted")
		}
		block.SetAttributeValue("restart", cty.StringVal(service.Restart))
	}
	if oneShot {
		if service.Healthcheck != nil {
			return fail(ErrRendererFailure, servicePath+".healthcheck", "a one-shot component has no health state")
		}
		// Created, started and attached until it exits; the exited container
		// is not drift.
		block.SetAttributeValue("must_run", cty.False)
		block.SetAttributeValue("attach", cty.True)
	} else if r.spec.Wait && (service.Healthcheck != nil || !nativeDockerNoImageHealthcheck[r.spec.ModuleRef][name]) {
		// The provider refuses to wait for a container without any health
		// check; for those `up --wait` only waits for running, which the
		// provider does anyway, and the StackKits completion probes the
		// HTTP health contract afterwards.
		block.SetAttributeValue("wait", cty.True)
		block.SetAttributeValue("wait_timeout", cty.NumberIntVal(NativeDockerWaitTimeoutSeconds))
	}
	block.SetAttributeValue("destroy_grace_seconds", cty.NumberIntVal(nativeDockerStopGraceSeconds))
	if service.StopSignal != "" {
		if !nativeStopSignalPattern.MatchString(service.StopSignal) {
			return fail(ErrRendererFailure, servicePath+".stop_signal", "stop signal is not a signal name")
		}
		block.SetAttributeValue("stop_signal", cty.StringVal(service.StopSignal))
	}
	if service.Init != nil {
		block.SetAttributeValue("init", cty.BoolVal(*service.Init))
	}
	if service.User != "" {
		if service.User != "0:0" {
			return fail(ErrRendererFailure, servicePath+".user", "only the governed root user override is admitted")
		}
		block.SetAttributeValue("user", cty.StringVal(service.User))
	}
	if err := nativeResources(servicePath, block, service.Deploy); err != nil {
		return err
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
		r.gaps[NativeDockerParityGapOOMScoreAdj] = struct{}{}
	}
	if err := nativeHealthcheck(servicePath, block, service.Healthcheck); err != nil {
		return err
	}

	labels := map[string]string{}
	for key, value := range service.Labels {
		if strings.HasPrefix(key, "com.docker.compose.") {
			return fail(ErrRendererFailure, servicePath+".labels", "the workload may not set Compose-owned labels")
		}
		labels[key] = value
	}
	serviceDigest, err := nativeServiceConfigDigest(service)
	if err != nil {
		return wrap(ErrRendererFailure, servicePath, "digest service", err)
	}
	labels["com.docker.compose.project"] = r.spec.ProjectName
	labels["com.docker.compose.service"] = name
	labels["com.docker.compose.container-number"] = "1"
	labels["com.docker.compose.oneoff"] = "False"
	// Compose lists a container only when it carries a config hash.
	labels["com.docker.compose.config-hash"] = hex.EncodeToString(serviceDigest[:])

	serviceFiles, err := r.secretEnvironment(servicePath, name, secrets, &environment)
	if err != nil {
		return err
	}
	for _, file := range serviceFiles {
		if file.Uploaded {
			appendNativeUpload(block, file)
			continue
		}
		appendNativeBindMount(block, file.RelPath, nativeDockerSecretMountDir+"/"+strings.TrimPrefix(file.RelPath, NativeDockerSecretDir+"/"))
	}
	if shim {
		mounts := block.AppendNewBlock("mounts", nil).Body()
		mounts.SetAttributeValue("type", cty.StringVal("bind"))
		mounts.SetAttributeValue("source", cty.StringVal(r.spec.SecretExecBinary))
		mounts.SetAttributeValue("target", cty.StringVal(secretexec.MountPath))
		mounts.SetAttributeValue("read_only", cty.True)
		// A new shim binary replaces the container, like a new image.
		label := block.AppendNewBlock("labels", nil).Body()
		label.SetAttributeValue("label", cty.StringVal("io.stackkit.secret-exec-digest"))
		label.SetAttributeRaw("value", hclwrite.TokensForFunctionCall("filesha256", hclwrite.TokensForValue(cty.StringVal(r.spec.SecretExecBinary))))
	}
	uploads, err := r.secretUploads(servicePath, name, block, service.Secrets)
	if err != nil {
		return err
	}
	serviceFiles = append(serviceFiles, uploads...)
	r.root.SecretFiles = append(r.root.SecretFiles, serviceFiles...)
	envValues := make([]cty.Value, 0, len(environment))
	for _, entry := range environment {
		envValues = append(envValues, cty.StringVal(entry))
	}
	if len(envValues) > 0 {
		block.SetAttributeValue("env", cty.SetVal(envValues))
	}

	if err := r.mounts(servicePath, block, service.Volumes); err != nil {
		return err
	}
	networks := append([]string(nil), service.Networks...)
	sort.Strings(networks)
	for _, network := range networks {
		reference, known := r.networkRefs[network]
		if !known {
			return fail(ErrRendererFailure, servicePath+".networks", "network %q is not declared", network)
		}
		attachment := block.AppendNewBlock("networks_advanced", nil).Body()
		attachment.SetAttributeRaw("name", reference)
		if !r.spec.PilotNetworkAliases {
			// Compose's aliases: peers reach a service by its service name.
			attachment.SetAttributeValue("aliases", cty.SetVal([]cty.Value{
				cty.StringVal(r.spec.ProjectName + "-" + name + "-1"), cty.StringVal(name),
			}))
		}
	}
	ports, err := nativePorts(servicePath, service.Ports)
	if err != nil {
		return err
	}
	for _, port := range ports {
		attributes := block.AppendNewBlock("ports", nil).Body()
		attributes.SetAttributeValue("internal", cty.NumberIntVal(int64(port.number)))
		if !port.listener {
			attributes.SetAttributeValue("ip", cty.StringVal("127.0.0.1"))
			continue
		}
		// Compose publishes "N:N/proto" on every host address (HostIp ""),
		// IPv6 included; the provider's default would bind 0.0.0.0 only.
		attributes.SetAttributeValue("external", cty.NumberIntVal(int64(port.number)))
		attributes.SetAttributeValue("protocol", cty.StringVal(port.protocol))
		attributes.SetAttributeValue("ip", cty.StringVal(""))
	}
	for _, device := range service.Devices {
		match := nativeDevicePattern.FindStringSubmatch(device)
		if match == nil || path.Clean(match[1]) != match[1] || path.Clean(match[2]) != match[2] {
			return fail(ErrRendererFailure, servicePath+".devices", "only a host device node mapped without permissions is admitted, got %q", device)
		}
		devices := block.AppendNewBlock("devices", nil).Body()
		devices.SetAttributeValue("host_path", cty.StringVal(match[1]))
		devices.SetAttributeValue("container_path", cty.StringVal(match[2]))
		// Compose's default cgroup permissions for a device mapping.
		devices.SetAttributeValue("permissions", cty.StringVal("rwm"))
	}
	for _, entry := range service.ExtraHosts {
		host, address, ok := strings.Cut(entry, ":")
		if !ok || host == "" || address == "" {
			return fail(ErrRendererFailure, servicePath+".extra_hosts", "malformed host entry")
		}
		hosts := block.AppendNewBlock("host", nil).Body()
		hosts.SetAttributeValue("host", cty.StringVal(host))
		hosts.SetAttributeValue("ip", cty.StringVal(address))
	}
	if len(serviceFiles) > 0 {
		// A rotated secret replaces the container, like Compose recreating
		// a service whose interpolated environment changed. Only the digest
		// enters the state: the file's own for one file, a digest over the
		// file digests for several.
		value := nativeFileDigestTokens(serviceFiles[0].RelPath)
		if len(serviceFiles) > 1 {
			digests := make([]hclwrite.Tokens, 0, len(serviceFiles))
			for _, file := range serviceFiles {
				digests = append(digests, nativeFileDigestTokens(file.RelPath))
			}
			value = hclwrite.TokensForFunctionCall("sha256", hclwrite.TokensForFunctionCall("join",
				hclwrite.TokensForValue(cty.StringVal(",")), hclwrite.TokensForTuple(digests)))
		}
		digest := block.AppendNewBlock("labels", nil).Body()
		digest.SetAttributeValue("label", cty.StringVal("io.stackkit.secret-env-digest"))
		digest.SetAttributeRaw("value", value)
	}
	appendNativeLabels(block, labels)
	if oneShot {
		lifecycle := block.AppendNewBlock("lifecycle", nil).Body()
		postcondition := lifecycle.AppendNewBlock("postcondition", nil).Body()
		postcondition.SetAttributeRaw("condition", hclwrite.Tokens{
			{Type: hclsyntax.TokenIdent, Bytes: []byte("self.exit_code")},
			{Type: hclsyntax.TokenEqualOp, Bytes: []byte("==")},
			{Type: hclsyntax.TokenNumberLit, Bytes: []byte("0")},
		})
		postcondition.SetAttributeValue("error_message", cty.StringVal("one-shot component "+name+" did not complete successfully"))
	}
	return nil
}

// NativeDockerRootConfig reports whether an OpenTofu root configuration is a
// native Docker-provider root (docker_container resources) rather than a
// Stage 1 wrapper (terraform_data triggers). Lifecycle callers use it to pick
// the convergence of a restored or drifted root: a wrapper is forced through
// its trigger, a native root converges with a plain apply. A root carrying
// both forms is refused.
func NativeDockerRootConfig(config []byte) (bool, error) {
	file, diagnostics := hclsyntax.ParseConfig(config, "main.tf", hcl.InitialPos)
	if diagnostics.HasErrors() {
		return false, fmt.Errorf("parse the OpenTofu root: %s", diagnostics.Error())
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return false, fmt.Errorf("OpenTofu root has no native HCL body")
	}
	containers, triggers := 0, 0
	for _, block := range body.Blocks {
		if block.Type != "resource" || len(block.Labels) != 2 {
			continue
		}
		switch block.Labels[0] {
		case "docker_container":
			containers++
		case "terraform_data":
			triggers++
		}
	}
	if containers > 0 && triggers > 0 {
		return false, fmt.Errorf("OpenTofu root mixes native containers and wrapper triggers")
	}
	return containers > 0, nil
}
