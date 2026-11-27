package ownapplication

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	contract "github.com/kombifyio/stackkits/contracts/ownapplication"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

const maxComposeProjectServices = 8

type observedComposeMember struct {
	service   string
	container observedContainer
	image     observedImage
	tcp       map[int]bool
	published map[int]bool
	bindings  []portBinding
	secrets   map[string]string
	emptyEnv  map[string]bool
	dataRef   string
}

// observeComposeProject inspects every container of a multi-service Docker
// Compose project (2..8 services) and builds a homelab.own-application/v3
// adoption plan when --adopt-compose-project is requested.
func observeComposeProject(ctx context.Context, docker nativehost.OwnAdoptionDocker, req AdoptionRequest, project string, target observedContainer, targetImage observedImage, peerIDs []string) (*adoptionPlan, error) {
	p := &adoptionPlan{
		composeProject: project,
		serviceSources: map[string]nativehost.OwnAdoptionMount{},
		secrets:        map[string]string{},
		running:        target.State.Running,
		restart:        restartPolicy(target),
	}
	p.result = AdoptionResult{
		APIVersion:     AdoptionResultVersion,
		Phase:          req.Phase,
		ContainerID:    target.ID,
		ContainerName:  strings.TrimPrefix(target.Name, "/"),
		Rows:           []AdoptionRow{},
		HiddenKeys:     []string{},
		SecretKeys:     []string{},
		PublishedPorts: []contract.PublishedPort{},
	}

	name := deriveAdoptedName(req.Name, project)
	if !contract.ValidName(name) || strings.HasPrefix(name, "kombify") || strings.HasPrefix(name, "stackkit") {
		p.refuse("refused_name", "name", name, "choose --name: 1..40 lowercase DNS characters starting with a letter, not kombify* or stackkit*", "name", name)
	}
	p.result.Name = name

	signIn := true
	app := contract.Application{
		APIVersion: contract.APIVersionV3,
		Name:       name,
		SignIn:     &signIn,
	}

	if len(peerIDs) > maxComposeProjectServices {
		p.refuse("refused_compose_too_many_services", "compose", fmt.Sprintf("%d services", len(peerIDs)), fmt.Sprintf("an own-application/v3 project supports at most %d services", maxComposeProjectServices), "project", project, "count", strconv.Itoa(len(peerIDs)), "limit", strconv.Itoa(maxComposeProjectServices))
		p.result.Application = &app
		return p, nil
	}

	idPattern := regexp.MustCompile(`^[a-f0-9]{64}$`)
	peerSet := make(map[string]bool, len(peerIDs))
	members := make([]observedComposeMember, 0, len(peerIDs))
	for _, id := range peerIDs {
		var c observedContainer
		var img observedImage
		if id == target.ID {
			c, img = target, targetImage
		} else {
			raw, err := docker.InspectContainer(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("inspect compose sibling %s: %w", id, err)
			}
			var containers []observedContainer
			if err := json.Unmarshal(raw, &containers); err != nil {
				return nil, fmt.Errorf("Docker container inspection is not a Docker Engine answer: %w", err)
			}
			if len(containers) != 1 || !idPattern.MatchString(containers[0].ID) {
				return nil, fmt.Errorf("Docker inspection did not return exactly one container for %s", id)
			}
			c = containers[0]
			rawImage, err := docker.InspectImage(ctx, c.Image)
			if err != nil {
				return nil, fmt.Errorf("inspect image of compose sibling %s: %w", id, err)
			}
			var images []observedImage
			if json.Unmarshal(rawImage, &images) != nil || len(images) != 1 || images[0].ID != c.Image {
				return nil, fmt.Errorf("Docker image inspection did not match compose sibling %s", id)
			}
			img = images[0]
		}
		peerSet[c.ID] = true
		members = append(members, observedComposeMember{
			service:   c.Config.Labels["com.docker.compose.service"],
			container: c,
			image:     img,
			tcp:       map[int]bool{},
			published: map[int]bool{},
			secrets:   map[string]string{},
			emptyEnv:  map[string]bool{},
		})
	}

	sort.Slice(members, func(i, j int) bool {
		if members[i].service != members[j].service {
			return members[i].service < members[j].service
		}
		return members[i].container.ID < members[j].container.ID
	})

	// Check user-defined networks across the project: sibling containers in the
	// same Compose project are expected peers; only external containers refuse.
	projectNetworks := map[string]bool{}
	for _, m := range members {
		for netName := range m.container.NetworkSettings.Networks {
			if netName == "bridge" || netName == "host" || netName == "none" {
				continue
			}
			projectNetworks[netName] = true
		}
	}
	sortedNetworks := make([]string, 0, len(projectNetworks))
	for netName := range projectNetworks {
		sortedNetworks = append(sortedNetworks, netName)
	}
	sort.Strings(sortedNetworks)
	for _, netName := range sortedNetworks {
		peers, err := docker.NetworkPeers(ctx, netName)
		if err != nil {
			return nil, fmt.Errorf("inspect network %s: %w", netName, err)
		}
		others := 0
		for _, peer := range peers {
			if !peerSet[peer] {
				others++
			}
		}
		if others != 0 {
			p.refuse("refused_shared_user_network", "network "+netName, fmt.Sprintf("%d other containers", others), "containers outside this Compose project share the network; rebuilding the project on its own private network would break them", "network", netName, "containers", strconv.Itoa(others))
		}
	}

	seenServices := map[string]bool{}
	services := make([]contract.Service, 0, len(members))
	serviceNames := make([]string, 0, len(members))
	allEmptyEnv := map[string]bool{}
	projectManaged := strings.HasPrefix(project, "stackkit-")
	if projectManaged {
		p.refuse("refused_already_managed", "source", project, "StackKits already manages this Compose project", "container", project)
	}

	for idx := range members {
		m := &members[idx]
		c := m.container
		img := m.image
		cname := strings.TrimPrefix(c.Name, "/")
		svcName := m.service
		fieldPrefix := svcName
		if fieldPrefix == "" {
			fieldPrefix = cname
		}

		if !projectManaged {
			managed := strings.HasPrefix(cname, "stackkit-")
			for key := range c.Config.Labels {
				if strings.HasPrefix(key, "io.stackkit.") {
					managed = true
				}
			}
			if managed {
				p.refuse("refused_already_managed", fieldPrefix+".source", cname, "StackKits already manages this container", "container", cname)
			}
		}

		if !contract.ValidServiceName(svcName) {
			p.refuse("refused_name", "service "+fieldPrefix, svcName, "Compose service name must be 1..40 lowercase DNS characters starting with a letter", "name", svcName)
		} else if seenServices[svcName] {
			p.refuse("refused_name", "service "+svcName, svcName, "duplicate Compose service name (scaled replicas are not admitted in a single v3 project)", "name", svcName)
		} else {
			seenServices[svcName] = true
		}

		svc := contract.Service{Name: svcName}

		// Image: digest-pinned registry image.
		pinned, _ := pinnedImage(c.Config.Image, img.RepoDigests)
		if pinned == "" {
			p.refuse("refused_local_only_image", fieldPrefix+".image", c.Config.Image, "no registry digest: a local-only or locally built image cannot be adopted; push it to a public registry first", "image", c.Config.Image, "service", svcName)
		} else if _, _, err := contract.SplitImage(pinned); err != nil {
			p.refuse("refused_image_reference", fieldPrefix+".image", c.Config.Image, err.Error(), "image", c.Config.Image, "service", svcName)
		} else {
			svc.Image = pinned
			p.row("image_pinned_by_digest", fieldPrefix+".image", RowKept, c.Config.Image, pinned, "pinned by the digest the original runs; updates and rollback stay digest-pinned", "image", c.Config.Image, "pinned", pinned, "service", svcName)
		}
		if !slices.Equal(c.Config.Cmd, img.Config.Cmd) || !slices.Equal(c.Config.Entrypoint, img.Config.Entrypoint) {
			p.refuse("refused_custom_command", fieldPrefix+".command", fmt.Sprintf("custom entrypoint/command (%d arguments)", len(c.Config.Entrypoint)+len(c.Config.Cmd)), "a custom command or entrypoint is not admitted; own applications run the image's own command", "arguments", strconv.Itoa(len(c.Config.Entrypoint)+len(c.Config.Cmd)), "service", svcName)
		}
		if c.Config.User != img.Config.User {
			p.refuse("refused_user_override", fieldPrefix+".user", c.Config.User, "a user override is not admitted; own applications run the image's USER", "user", c.Config.User, "service", svcName)
		}

		// Privilege and host namespaces.
		switch mode := c.HostConfig.NetworkMode; {
		case mode == "host":
			p.refuse("refused_host_network", fieldPrefix+".network", mode, "host networking is not admitted", "service", svcName)
		case strings.HasPrefix(mode, "container:"):
			p.refuse("refused_container_network", fieldPrefix+".network", mode, "sharing another container's network is not admitted", "mode", mode, "service", svcName)
		}
		if c.HostConfig.Privileged {
			p.refuse("refused_privileged", fieldPrefix+".privileged", "true", "privileged containers are not admitted", "service", svcName)
		}
		if len(c.HostConfig.CapAdd) != 0 {
			p.refuse("refused_added_capabilities", fieldPrefix+".capabilities", strings.Join(c.HostConfig.CapAdd, ","), "added capabilities are not admitted", "capabilities", strings.Join(c.HostConfig.CapAdd, ","), "service", svcName)
		}
		if len(c.HostConfig.Devices) != 0 || len(c.HostConfig.DeviceRequests) != 0 {
			p.refuse("refused_devices", fieldPrefix+".devices", strconv.Itoa(len(c.HostConfig.Devices)+len(c.HostConfig.DeviceRequests)), "device passthrough is not admitted", "count", strconv.Itoa(len(c.HostConfig.Devices)+len(c.HostConfig.DeviceRequests)), "service", svcName)
		}
		for _, ns := range []struct{ field, mode string }{{"pid", c.HostConfig.PidMode}, {"ipc", c.HostConfig.IpcMode}, {"uts", c.HostConfig.UTSMode}} {
			if ns.mode == "host" || strings.HasPrefix(ns.mode, "container:") {
				p.refuse("refused_shared_namespace", fieldPrefix+"."+ns.field, ns.mode, "sharing a host or container namespace is not admitted", "namespace", ns.field, "mode", ns.mode, "service", svcName)
			}
		}
		for _, option := range c.HostConfig.SecurityOpt {
			if strings.Contains(option, "unconfined") || strings.HasPrefix(option, "label:disable") {
				p.refuse("refused_relaxed_security", fieldPrefix+".security", option, "a relaxed security profile is not admitted", "option", option, "service", svcName)
			}
		}
		if len(c.HostConfig.VolumesFrom) != 0 {
			p.refuse("refused_volumes_from", fieldPrefix+".volumes-from", strings.Join(c.HostConfig.VolumesFrom, ","), "shared volumes from another container are not admitted", "containers", strings.Join(c.HostConfig.VolumesFrom, ","), "service", svcName)
		}
		if c.HostConfig.Runtime != "" && c.HostConfig.Runtime != "runc" {
			p.refuse("refused_custom_runtime", fieldPrefix+".runtime", c.HostConfig.Runtime, "a custom container runtime is not admitted", "runtime", c.HostConfig.Runtime, "service", svcName)
		}
		if len(c.HostConfig.Links) != 0 {
			p.refuse("refused_legacy_links", fieldPrefix+".links", strconv.Itoa(len(c.HostConfig.Links))+" links", "legacy container links would break when the project is rebuilt", "count", strconv.Itoa(len(c.HostConfig.Links)), "service", svcName)
		}
		if c.HostConfig.UsernsMode == "host" {
			p.refuse("refused_host_user_namespace", fieldPrefix+".userns", "host", "the host user namespace is not admitted", "service", svcName)
		}
		if c.HostConfig.CgroupnsMode == "host" {
			p.row("cgroupns_daemon_default", fieldPrefix+".cgroupns", RowChanged, "host", "daemon default", "own applications take the daemon default cgroup namespace", "service", svcName)
		}
		if len(c.HostConfig.CapDrop) != 0 {
			p.row("capabilities_fixed_policy", fieldPrefix+".cap-drop", RowChanged, strings.Join(c.HostConfig.CapDrop, ","), "ALL, then CHOWN, SETUID, SETGID, NET_BIND_SERVICE", "own applications use the fixed capability policy", "dropped", strings.Join(c.HostConfig.CapDrop, ","), "service", svcName)
		}
		for _, ov := range []struct {
			field string
			count int
		}{
			{"extra-hosts", len(c.HostConfig.ExtraHosts)},
			{"dns", len(c.HostConfig.DNS)},
			{"sysctls", len(c.HostConfig.Sysctls)},
			{"tmpfs", len(c.HostConfig.Tmpfs)},
			{"group-add", len(c.HostConfig.GroupAdd)},
			{"ulimits", len(c.HostConfig.Ulimits)},
		} {
			if ov.count != 0 {
				p.row("host_override_dropped", fieldPrefix+"."+ov.field, RowChanged, strconv.Itoa(ov.count)+" entries", "dropped", "own applications take no host-level overrides", "override", ov.field, "count", strconv.Itoa(ov.count), "service", svcName)
			}
		}
		if c.HostConfig.ReadonlyRootfs {
			p.row("read_only_root_dropped", fieldPrefix+".read-only-root", RowChanged, "true", "false", "own applications have a writable root filesystem", "service", svcName)
		}

		// Data: at most one named volume or bind below an admitted data root per service.
		var data []int
		for i, mt := range c.Mounts {
			switch {
			case strings.HasSuffix(mt.Source, "docker.sock") || strings.HasSuffix(mt.Destination, "docker.sock"):
				p.refuse("refused_docker_socket", fieldPrefix+".mount "+mt.Destination, mt.Source, "the Docker socket is never mounted into an own application", "mountPath", mt.Destination, "service", svcName)
			case mt.Type == "tmpfs":
				p.row("tmpfs_mount_dropped", fieldPrefix+".mount "+mt.Destination, RowChanged, "tmpfs", "dropped", "own applications keep no tmpfs mounts", "mountPath", mt.Destination, "service", svcName)
			case mt.Type == "bind" && !mt.RW && (mt.Source == "/etc/localtime" || mt.Source == "/etc/timezone"):
				p.row("timezone_mount_dropped", fieldPrefix+".mount "+mt.Destination, RowChanged, mt.Source, "dropped", "set the TZ environment variable for the time zone", "mountPath", mt.Destination, "source", mt.Source, "service", svcName)
			case mt.Type == "bind" && (mt.Source == "/" || !contract.AdoptableBindSource(mt.Source)):
				p.refuse("refused_bind_outside_data_roots", fieldPrefix+".mount "+mt.Destination, mt.Source, "a bind mount outside "+strings.Join(contract.AdoptedDataRoots, ", ")+" is not admitted", "mountPath", mt.Destination, "source", mt.Source, "dataRoots", strings.Join(contract.AdoptedDataRoots, ","), "service", svcName)
			case mt.Type == "bind" || mt.Type == "volume":
				data = append(data, i)
			default:
				p.refuse("refused_mount_type", fieldPrefix+".mount "+mt.Destination, mt.Type, "unsupported mount type", "mountPath", mt.Destination, "type", mt.Type, "service", svcName)
			}
		}
		if len(data) > 1 {
			p.refuse("refused_multiple_data_mounts", fieldPrefix+".data", strconv.Itoa(len(data))+" data mounts", "more than one data mount per service is not admitted; each v3 service keeps at most one named volume", "count", strconv.Itoa(len(data)), "service", svcName)
		}
		if len(data) == 1 {
			mt := c.Mounts[data[0]]
			source := nativehost.OwnAdoptionMount{Type: mt.Type, Source: mt.Source}
			ref := "bind:" + mt.Source
			if mt.Type == "volume" {
				source.Source, ref = mt.Name, "volume:"+mt.Name
			}
			m.dataRef = ref
			p.serviceSources[svcName] = source
			svc.Volume = &contract.Volume{MountPath: mt.Destination}
			reason := "copied into the service's named volume; the original stays untouched"
			if !mt.RW {
				reason += "; the mount becomes writable"
			}
			p.row("volume_copied_checksummed", fieldPrefix+".data", RowKept, ref+" at "+mt.Destination, "named volume at "+mt.Destination, reason, "source", ref, "mountPath", mt.Destination, "madeWritable", strconv.FormatBool(!mt.RW), "service", svcName)
			p.result.ServiceData = append(p.result.ServiceData, AdoptionData{Service: svcName, Source: ref, MountPath: mt.Destination})
		}
		declaredVols := make([]string, 0, len(img.Config.Volumes))
		for declared := range img.Config.Volumes {
			declaredVols = append(declaredVols, declared)
		}
		sort.Strings(declaredVols)
		for _, declared := range declaredVols {
			if svc.Volume == nil || declared != svc.Volume.MountPath {
				p.refuse("refused_image_volume", fieldPrefix+".image volume "+declared, declared, "the image declares a volume the service does not keep as its one named volume", "mountPath", declared, "service", svcName)
			}
		}

		// Environment & secrets per service.
		imageEnv := map[string]bool{}
		for _, entry := range img.Config.Env {
			imageEnv[entry] = true
		}
		environment := map[string]string{}
		for _, entry := range c.Config.Env {
			if imageEnv[entry] {
				continue
			}
			key, value, found := strings.Cut(entry, "=")
			if !found || !envName.MatchString(key) || len(key) > 128 {
				p.refuse("refused_environment_entry", fieldPrefix+".environment", "an entry without a valid NAME=value form", "an environment entry without a valid name is not admitted", "service", svcName)
				continue
			}
			if strings.ContainsAny(value, "\x00\r\n") {
				p.refuse("refused_multiline_environment", fieldPrefix+".environment "+key, "", "a multi-line environment value is not admitted", "name", key, "service", svcName)
				continue
			}
			if value == "" {
				environment[key] = ""
				m.emptyEnv[key] = true
				allEmptyEnv[contract.SecretSlotKey(svcName, key)] = true
				continue
			}
			if len(value) > 8192 {
				p.refuse("refused_environment_value_size", fieldPrefix+".environment "+key, "", "an environment value is limited to 8192 bytes", "name", key, "limitBytes", "8192", "service", svcName)
				continue
			}
			m.secrets[key] = value
			slot := contract.SecretSlotKey(svcName, key)
			p.secrets[slot] = value
		}
		if len(environment) > 64 || len(m.secrets) > 64 {
			p.refuse("refused_environment_count", fieldPrefix+".environment", strconv.Itoa(len(environment))+" variables, "+strconv.Itoa(len(m.secrets))+" secrets", "at most 64 environment variables and 64 secrets per service", "variables", strconv.Itoa(len(environment)), "secrets", strconv.Itoa(len(m.secrets)), "limit", "64", "service", svcName)
		}
		if len(environment) != 0 {
			svc.Environment = environment
			keys := make([]string, 0, len(environment))
			for key := range environment {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			p.row("environment_empty_kept", fieldPrefix+".environment", RowKept, strings.Join(keys, ","), "empty values", "", "names", strings.Join(keys, ","), "service", svcName)
		}
		if len(m.secrets) != 0 {
			svc.SecretRefs = map[string]string{}
			hiddenKeys := make([]string, 0, len(m.secrets))
			for key := range m.secrets {
				slot := contract.SecretSlotKey(svcName, key)
				svc.SecretRefs[key] = "secret://own-" + name + "/" + svcName + "/" + key
				hiddenKeys = append(hiddenKeys, key)
				p.result.HiddenKeys = append(p.result.HiddenKeys, slot)
				if secretKey.MatchString(strings.ToUpper(key)) {
					p.result.SecretKeys = append(p.result.SecretKeys, slot)
				}
			}
			sort.Strings(hiddenKeys)
			p.row("environment_values_to_custody", fieldPrefix+".environment-values", RowChanged, strings.Join(hiddenKeys, ","), "secretRefs", "every value moves into encrypted node custody (0600, application-scoped); plans, logs and receipts carry names only and values never leave the node", "names", strings.Join(hiddenKeys, ","), "service", svcName)
		}

		// Ports observed on this service.
		for spec := range c.Config.ExposedPorts {
			if port, proto, ok := parsePortSpec(spec); ok && proto == "tcp" {
				m.tcp[port] = true
			}
		}
		for spec, list := range c.HostConfig.PortBindings {
			port, proto, ok := parsePortSpec(spec)
			if !ok {
				continue
			}
			if proto == "tcp" {
				m.tcp[port] = true
			}
			for _, b := range list {
				m.bindings = append(m.bindings, portBinding{spec: spec, ip: b.HostIP, host: b.HostPort, port: port, proto: proto})
				if proto == "tcp" {
					m.published[port] = true
				}
			}
		}
		sort.Slice(m.bindings, func(i, j int) bool {
			return m.bindings[i].spec+m.bindings[i].ip+m.bindings[i].host < m.bindings[j].spec+m.bindings[j].ip+m.bindings[j].host
		})
		for _, b := range m.bindings {
			address := b.ip
			if address == "" {
				address = "*"
			}
			observed := address + ":" + b.host + "->" + b.spec
			p.row("port_dropped_v3_project", fieldPrefix+".port "+b.spec, RowChanged, observed, "dropped (routed via https://own-<name>.<domain>)", "homelab.own-application/v3 projects keep all services on an isolated private network without host port bindings", "binding", observed, "service", svcName)
		}

		// Resources per service.
		memory := req.MemoryMiB
		observedMemory := "unlimited"
		if c.HostConfig.Memory > 0 {
			observedMemory = strconv.FormatInt(c.HostConfig.Memory>>20, 10) + " MiB"
			if memory == 0 {
				memory = int(c.HostConfig.Memory >> 20)
			}
		}
		if memory == 0 {
			memory = adoptedDefaultMemoryMiB
		}
		memory = min(max(memory, 64), 8192)
		cpus := req.CPUs
		observedCPUs := "unlimited"
		if c.HostConfig.NanoCpus > 0 {
			observedCPUs = strconv.FormatFloat(float64(c.HostConfig.NanoCpus)/1e9, 'f', -1, 64)
			if cpus == 0 {
				cpus = float64(c.HostConfig.NanoCpus) / 1e9
			}
		}
		if cpus == 0 {
			cpus = adoptedDefaultCPUs
		}
		cpus = min(max(cpus, 0.1), 4)
		svc.Resources = contract.Resources{MemoryMiB: memory, CPUs: cpus}
		limits := strconv.Itoa(memory) + " MiB, " + strconv.FormatFloat(cpus, 'f', -1, 64) + " CPUs"
		if observedMemory+", "+observedCPUs+" CPUs" != limits {
			p.row("resources_bounded", fieldPrefix+".resources", RowChanged, observedMemory+", "+observedCPUs+" CPUs", limits, "own applications are bounded (64..8192 MiB, 0.1..4 CPUs); set --memory-mib and --cpus to choose", "memoryMiB", strconv.Itoa(memory), "cpus", strconv.FormatFloat(cpus, 'f', -1, 64), "service", svcName)
		}

		if len(c.Config.Healthcheck) != 0 && string(c.Config.Healthcheck) != "null" && string(c.Config.Healthcheck) != string(img.Config.Healthcheck) {
			p.row("healthcheck_from_image", fieldPrefix+".healthcheck", RowChanged, "container override", "image healthcheck", "own applications use the image's healthcheck and an HTTP readiness check", "service", svcName)
		}

		p.originals = append(p.originals, AdoptionOriginalContainer{
			Service:       svcName,
			ContainerID:   c.ID,
			Running:       c.State.Running,
			RestartPolicy: restartPolicy(c),
			DataSource:    m.dataRef,
		})
		services = append(services, svc)
		serviceNames = append(serviceNames, svcName)
	}

	sort.Strings(p.result.HiddenKeys)
	for _, key := range req.SecretEnv {
		matched := false
		if _, hidden := p.secrets[key]; hidden {
			matched = true
			if !slices.Contains(p.result.SecretKeys, key) {
				p.result.SecretKeys = append(p.result.SecretKeys, key)
			}
		} else if allEmptyEnv[key] {
			matched = true
		} else if !strings.Contains(key, ".") {
			for _, m := range members {
				slot := contract.SecretSlotKey(m.service, key)
				if _, hidden := p.secrets[slot]; hidden {
					matched = true
					if !slices.Contains(p.result.SecretKeys, slot) {
						p.result.SecretKeys = append(p.result.SecretKeys, slot)
					}
				} else if m.emptyEnv[key] {
					matched = true
				}
			}
		}
		if !matched {
			p.refuse("refused_unknown_secret_env", "secret "+key, "", "--secret-env names a variable no service in the project sets", "name", key)
		}
	}
	sort.Strings(p.result.SecretKeys)
	if len(p.result.SecretKeys) != 0 {
		p.row("secrets_classified", "secrets", RowKept, strings.Join(p.result.SecretKeys, ","), "classified secret", "named like a credential or marked with --secret-env; never shown, logged or returned, and rotated with app own --action update --secrets-file", "names", strings.Join(p.result.SecretKeys, ","))
	}

	// Select the single routed HTTP service for homelab.own-application/v3.
	routedIdx := -1
	if req.HTTPService != "" {
		for i, m := range members {
			if m.service == req.HTTPService {
				routedIdx = i
				break
			}
		}
		if routedIdx == -1 {
			p.refuse("refused_http_service_ambiguous", "http-service", req.HTTPService, "--http-service does not match any service in Compose project "+project+"; choose one of: "+strings.Join(serviceNames, ", "), "candidates", strings.Join(serviceNames, ","))
		}
	} else {
		var publishedCandidates, exposedCandidates []int
		for i, m := range members {
			if len(m.published) > 0 {
				publishedCandidates = append(publishedCandidates, i)
			}
			if len(m.tcp) > 0 {
				exposedCandidates = append(exposedCandidates, i)
			}
		}
		switch {
		case len(publishedCandidates) == 1:
			routedIdx = publishedCandidates[0]
		case len(publishedCandidates) > 1:
			names := make([]string, 0, len(publishedCandidates))
			for _, i := range publishedCandidates {
				names = append(names, members[i].service)
			}
			p.refuse("refused_http_service_ambiguous", "http-service", strings.Join(names, ","), "multiple services publish TCP ports; choose the routed web service with --http-service", "candidates", strings.Join(names, ","))
		case len(exposedCandidates) == 1:
			routedIdx = exposedCandidates[0]
		default:
			names := make([]string, 0, len(exposedCandidates))
			for _, i := range exposedCandidates {
				names = append(names, members[i].service)
			}
			observed := strings.Join(names, ",")
			if observed == "" {
				observed = "none"
			}
			p.refuse("refused_http_service_ambiguous", "http-service", observed, "choose the routed web service with --http-service; an own-application/v3 project routes HTTP to one service", "candidates", strings.Join(names, ","))
		}
	}

	if routedIdx != -1 {
		rm := members[routedIdx]
		httpPort := req.HTTPPort
		candidates := sortedPorts(rm.tcp)
		if httpPort == 0 {
			switch {
			case len(candidates) == 1:
				httpPort = candidates[0]
			case len(sortedPorts(rm.published)) == 1:
				httpPort = sortedPorts(rm.published)[0]
			}
		}
		if httpPort < 1 || httpPort > 65535 {
			p.refuse("refused_http_port_ambiguous", rm.service+".http-port", joinPorts(candidates), "choose the web port with --http-port; own applications serve HTTP on one port", "candidates", joinPorts(candidates), "service", rm.service)
		} else {
			services[routedIdx].HTTPPort = httpPort
			p.row("http_port_routed", rm.service+".http-port", RowKept, joinPorts(candidates), strconv.Itoa(httpPort), "routed on https://own-<name>.<domain> behind Homelab sign-in; readiness needs HTTP 2xx/3xx on /", "port", strconv.Itoa(httpPort), "candidates", joinPorts(candidates), "service", rm.service)
		}
		p.running = rm.container.State.Running
		p.address = ""
		for _, network := range rm.container.NetworkSettings.Networks {
			if p.address == "" && network.IPAddress != "" {
				p.address = network.IPAddress
			}
		}
	}

	p.row("compose_project_adopted", "compose", RowKept, project+" ("+strings.Join(serviceNames, ", ")+")", fmt.Sprintf("homelab.own-application/v3 (%d services)", len(serviceNames)), "all services of the Compose project are adopted together on an isolated project bridge so inter-service DNS names keep working", "project", project, "services", strings.Join(serviceNames, ","), "count", strconv.Itoa(len(serviceNames)))
	p.row("own_private_network", "networks", RowChanged, strings.Join(sortedNetworks, ","), "isolated project bridge network", "services reach each other by Compose service name on the project's private network with no outbound Internet", "networks", strings.Join(sortedNetworks, ","))
	p.row("hostname_route_with_sign_in", "access", RowChanged, "Compose project", "https://own-<name>.<domain> with Homelab sign-in", "the routed service is served behind Homelab sign-in; all other services stay private on the project bridge")
	p.row("container_recreated", "recreation", RowChanged, fmt.Sprintf("%d containers in %s", len(members), project), "recreated managed v3 project", "all original project containers are stopped, never edited, and retained with restart policy no for 7-day --restore-original", "containerId", target.ID[:12])

	app.Services = services
	p.result.Application = &app
	return p, nil
}

func deriveAdoptedName(explicit, source string) string {
	if explicit != "" {
		return explicit
	}
	name := strings.Trim(nameCleaner.ReplaceAllString(strings.ToLower(source), "-"), "-")
	if name != "" && (name[0] < 'a' || name[0] > 'z') {
		name = "app-" + name
	}
	if len(name) > 40 {
		name = strings.Trim(name[:40], "-")
	}
	return name
}
