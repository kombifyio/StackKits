package architecturev2renderer

import (
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/kombifyio/stackkits/internal/secretexec"
)

type nativePort struct {
	number   int
	protocol string
	listener bool
}

// nativePorts parses the admitted port forms: the loopback health port
// ("127.0.0.1::N") and a listener on the same host and container port
// ("N:N/tcp" or "N:N/udp"). They are ordered by port and protocol, the order
// the provider reads them back in, so a re-plan settles.
func nativePorts(servicePath string, entries []string) ([]nativePort, error) {
	ports := make([]nativePort, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		port := nativePort{protocol: "tcp"}
		if match := nativeLoopbackPortPattern.FindStringSubmatch(entry); match != nil {
			port.number, _ = strconv.Atoi(match[1])
		} else if match := nativeListenerPortPattern.FindStringSubmatch(entry); match != nil && match[1] == match[2] {
			port.number, _ = strconv.Atoi(match[1])
			port.protocol, port.listener = match[3], true
		} else {
			return nil, fail(ErrRendererFailure, servicePath+".ports", "only the loopback health port and same-port listeners are admitted, got %q", entry)
		}
		key := strconv.Itoa(port.number) + "/" + port.protocol
		if port.number < 1 || port.number > 65535 || seen[key] {
			return nil, fail(ErrRendererFailure, servicePath+".ports", "port %q is out of range or published twice", entry)
		}
		seen[key] = true
		ports = append(ports, port)
	}
	slices.SortFunc(ports, func(a, b nativePort) int {
		if a.number != b.number {
			return a.number - b.number
		}
		return strings.Compare(a.protocol, b.protocol)
	})
	return ports, nil
}

// shimStart returns the container's entrypoint and command. For a container
// whose governed reader is the entrypoint shim, the shim becomes the
// entrypoint: it reads each secret file into the process environment and
// execs the start the container would otherwise have (the Compose
// entrypoint and command, or the image's own, pinned with its digest).
// Arguments stay Compose literals ($$ escaped) like every other value.
func (r *nativeDockerRender) shimStart(servicePath, name, digest string, service WorkloadComposeService, secrets []NativeDockerSecretBinding) ([]string, []string, bool, error) {
	reader := r.readers[name]
	if reader.Shim == nil || len(secrets) == 0 {
		return service.Entrypoint, service.Command, false, nil
	}
	if "sha256:"+digest != reader.Shim.ImageDigest {
		return nil, nil, false, fail(ErrRendererFailure, servicePath+".image", "the entrypoint shim is governed only for its pinned image")
	}
	if !filepath.IsAbs(r.spec.SecretExecBinary) || filepath.Clean(r.spec.SecretExecBinary) != r.spec.SecretExecBinary {
		return nil, nil, false, fail(ErrRendererFailure, servicePath, "the entrypoint shim needs the absolute path of its binary")
	}
	escape := func(values []string) []string {
		escaped := make([]string, len(values))
		for index, value := range values {
			escaped[index] = strings.ReplaceAll(value, "$", "$$")
		}
		return escaped
	}
	original, command := service.Entrypoint, service.Command
	if len(original) == 0 {
		original = escape(reader.Shim.Entrypoint)
		if len(command) == 0 {
			command = escape(reader.Shim.Command)
		}
	}
	entrypoint := []string{secretexec.MountPath}
	for _, binding := range secrets {
		entrypoint = append(entrypoint, "--env", binding.Name+"="+nativeDockerSecretMountDir+"/"+name+"."+binding.Name)
	}
	entrypoint = append(append(entrypoint, "--"), original...)
	return entrypoint, command, true, nil
}

// dependencies renders depends_on. Every condition the Compose renderer
// emits has an exact native order: a started daemon (created, and with Wait
// also healthy), a one-shot that completed with exit 0 (attached, with the
// exit-code postcondition), or a healthy daemon (needs Wait and a health
// check). Anything else fails closed.
func (r *nativeDockerRender) dependencies(servicePath string, block *hclwrite.Body, service WorkloadComposeService) error {
	if len(service.DependsOn) == 0 {
		return nil
	}
	references := make([]hclwrite.Tokens, 0, len(service.DependsOn))
	for _, dependency := range sortedKeys(service.DependsOn) {
		target, known := r.document.Services[dependency]
		if !known {
			return fail(ErrRendererFailure, servicePath+".depends_on", "dependency %q is not a service of the project", dependency)
		}
		switch condition := service.DependsOn[dependency].Condition; condition {
		case "service_started":
			if r.oneShot[dependency] {
				return fail(ErrRendererFailure, servicePath+".depends_on."+dependency, "a one-shot dependency must complete successfully")
			}
		case "service_completed_successfully":
			if !r.oneShot[dependency] {
				return fail(ErrRendererFailure, servicePath+".depends_on."+dependency, "only a one-shot component completes")
			}
		case "service_healthy":
			if r.oneShot[dependency] || target.Healthcheck == nil || !r.spec.Wait {
				return fail(ErrRendererFailure, servicePath+".depends_on."+dependency, "a healthy dependency needs a waited daemon with a health check")
			}
		default:
			return fail(ErrRendererFailure, servicePath+".depends_on."+dependency, "unsupported dependency condition %q", condition)
		}
		references = append(references, nativeTraversal("docker_container", nativeResourceName("container", dependency)))
	}
	block.SetAttributeRaw("depends_on", hclwrite.TokensForTuple(references))
	return nil
}

// secretEnvironment binds the secret environment values of one container to
// its governed file-based reader and adds the reader variables.

func (r *nativeDockerRender) mounts(servicePath string, block *hclwrite.Body, volumes []any) error {
	for _, entry := range volumes {
		mount, short := entry.(string)
		if !short {
			return fail(ErrRendererFailure, servicePath+".volumes", "only the short volume form is admitted")
		}
		parts := strings.Split(mount, ":")
		readOnly := false
		if len(parts) == 3 && parts[2] == "ro" {
			readOnly = true
			parts = parts[:2]
		}
		if len(parts) != 2 || !strings.HasPrefix(parts[1], "/") {
			return fail(ErrRendererFailure, servicePath+".volumes", "unsupported volume form %q", mount)
		}
		if nativeBindSourcePattern.MatchString(parts[0]) && !strings.Contains(parts[0], "..") {
			if !readOnly {
				return fail(ErrRendererFailure, servicePath+".volumes", "a project file mount must be read-only")
			}
			appendNativeBindMount(block, strings.TrimPrefix(parts[0], "./"), parts[1])
			continue
		}
		source, known := r.volumeRefs[parts[0]]
		if !known {
			return fail(ErrRendererFailure, servicePath+".volumes", "volume %q is not a project volume", parts[0])
		}
		mounts := block.AppendNewBlock("mounts", nil).Body()
		mounts.SetAttributeValue("type", cty.StringVal("volume"))
		mounts.SetAttributeRaw("source", source)
		mounts.SetAttributeValue("target", cty.StringVal(parts[1]))
		if readOnly {
			mounts.SetAttributeValue("read_only", cty.True)
		}
	}
	return nil
}

func nativeResources(servicePath string, block *hclwrite.Body, deploy *WorkloadComposeDeploy) error {
	if deploy == nil {
		return nil
	}
	for _, bounds := range []*WorkloadComposeResourceBounds{deploy.Resources.Limits, deploy.Resources.Reservations} {
		if bounds != nil && (bounds.CPUs != "" || len(bounds.Devices) != 0) {
			return fail(ErrRendererFailure, servicePath+".deploy", "CPU limits and device reservations have no native translation")
		}
	}
	if limits := deploy.Resources.Limits; limits != nil && limits.Memory != "" {
		megabytes, err := nativeMemoryMegabytes(limits.Memory)
		if err != nil {
			return wrap(ErrRendererFailure, servicePath+".deploy", "memory limit", err)
		}
		block.SetAttributeValue("memory", cty.NumberIntVal(megabytes))
		// Docker's effective default when only a limit is set; rendering it
		// keeps a re-plan empty.
		block.SetAttributeValue("memory_swap", cty.NumberIntVal(2*megabytes))
	}
	if reservations := deploy.Resources.Reservations; reservations != nil && reservations.Memory != "" {
		megabytes, err := nativeMemoryMegabytes(reservations.Memory)
		if err != nil {
			return wrap(ErrRendererFailure, servicePath+".deploy", "memory reservation", err)
		}
		block.SetAttributeValue("memory_reservation", cty.NumberIntVal(megabytes))
	}
	return nil
}

func nativeHealthcheck(servicePath string, block *hclwrite.Body, check *WorkloadComposeHealthcheck) error {
	if check == nil {
		return nil
	}
	if len(check.Test) < 2 || (check.Test[0] != "CMD" && check.Test[0] != "CMD-SHELL") || check.Retries < 0 {
		return fail(ErrRendererFailure, servicePath+".healthcheck", "health check must be a CMD or CMD-SHELL test")
	}
	test, err := nativeLiteralList(check.Test)
	if err != nil {
		return wrap(ErrRendererFailure, servicePath+".healthcheck", "health check argument is not a literal", err)
	}
	health := block.AppendNewBlock("healthcheck", nil).Body()
	health.SetAttributeValue("test", test)
	for _, duration := range [][2]string{{"interval", check.Interval}, {"timeout", check.Timeout}, {"start_period", check.StartPeriod}, {"start_interval", check.StartInterval}} {
		if duration[1] == "" {
			continue
		}
		if !nativeDurationPattern.MatchString(duration[1]) {
			return fail(ErrRendererFailure, servicePath+".healthcheck."+duration[0], "duration %q is not a plain duration", duration[1])
		}
		health.SetAttributeValue(duration[0], cty.StringVal(duration[1]))
	}
	if check.Retries > 0 {
		health.SetAttributeValue("retries", cty.NumberIntVal(int64(check.Retries)))
	}
	return nil
}

// nativeUntranslatedServiceField names the first set service field the
// native renderer does not translate, by its Compose key.
func nativeUntranslatedServiceField(service WorkloadComposeService) string {
	value := reflect.ValueOf(service)
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		if _, translated := nativeDockerTranslatedServiceFields[field.Name]; translated || value.Field(index).IsZero() {
			continue
		}
		key, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		return key
	}
	return ""
}

// nativeServiceConfigDigest is the per-service Compose config hash label.
// The pilot (S2.0) digested a subset document; a service inside that subset
// keeps the same digest, so a root rendered before S2.1 re-renders byte for
// byte (Advanced restore compares the restored root with its re-rendering)
// and its container is not replaced by the upgrade. Settings S2.1 added are
// digested after it, only when set.
func nativeServiceConfigDigest(service WorkloadComposeService) ([32]byte, error) {
	type bounds struct{ Memory string }
	pilot := struct {
		Image       string
		Restart     string
		Logging     *WorkloadComposeLogging
		OOMScoreAdj *int
		Deploy      *struct {
			Resources struct{ Limits, Reservations *bounds }
		}
		Environment map[string]string
		Volumes     []any
		Networks    []string
		Ports       []string
		ExtraHosts  []string
		Labels      map[string]string
	}{
		Image: service.Image, Restart: service.Restart, Logging: service.Logging, OOMScoreAdj: service.OOMScoreAdj,
		Environment: service.Environment, Volumes: service.Volumes, Networks: service.Networks, Ports: service.Ports,
		ExtraHosts: service.ExtraHosts, Labels: service.Labels,
	}
	if service.Deploy != nil {
		pilot.Deploy = &struct {
			Resources struct{ Limits, Reservations *bounds }
		}{}
		if limits := service.Deploy.Resources.Limits; limits != nil {
			pilot.Deploy.Resources.Limits = &bounds{Memory: limits.Memory}
		}
		if reservations := service.Deploy.Resources.Reservations; reservations != nil {
			pilot.Deploy.Resources.Reservations = &bounds{Memory: reservations.Memory}
		}
	}
	encoded, err := json.Marshal(pilot)
	if err != nil {
		return [32]byte{}, err
	}
	added := WorkloadComposeService{
		Command: service.Command, Entrypoint: service.Entrypoint, DependsOn: service.DependsOn,
		StopSignal: service.StopSignal, Init: service.Init, Healthcheck: service.Healthcheck, Secrets: service.Secrets,
		Devices: service.Devices, User: service.User,
	}
	if !reflect.ValueOf(added).IsZero() {
		extension, err := json.Marshal(added)
		if err != nil {
			return [32]byte{}, err
		}
		encoded = append(append(encoded, 0), extension...)
	}
	return sha256.Sum256(encoded), nil
}

func nativeLiteralList(values []string) (cty.Value, error) {
	list := make([]cty.Value, 0, len(values))
	for _, value := range values {
		literal, err := unescapeComposeLiteral(value)
		if err != nil {
			return cty.NilVal, err
		}
		list = append(list, cty.StringVal(literal))
	}
	return cty.ListVal(list), nil
}

func nativeTraversal(names ...string) hclwrite.Tokens {
	traversal := hcl.Traversal{hcl.TraverseRoot{Name: names[0]}}
	for _, name := range names[1:] {
		traversal = append(traversal, hcl.TraverseAttr{Name: name})
	}
	return hclwrite.TokensForTraversal(traversal)
}
