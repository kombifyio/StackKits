package cue

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
)

// ModuleContract represents a full extracted #ModuleContract from a module's CUE definition.
type ModuleContract struct {
	Metadata     ModuleMetadata
	Requires     *RequiresSpec
	Provides     *ProvidesSpec
	Settings     *SettingsSpec
	Services     map[string]ServiceDef
	Provisioners map[string]ProvisionerDef
	Enabled      bool
}

// ModuleMetadata identifies a module.
type ModuleMetadata struct {
	Name        string
	DisplayName string
	Version     string
	Layer       string
	Description string
	Core        bool
}

// RequiresSpec declares what a module needs from other modules and infrastructure.
type RequiresSpec struct {
	Services       map[string]RequiredService
	Infrastructure InfraRequirements
}

// RequiredService is a dependency on another module.
type RequiredService struct {
	MinVersion string
	Provides   []string
	Optional   bool
}

// InfraRequirements declares infrastructure needs.
type InfraRequirements struct {
	Docker            bool
	Network           string
	DockerSocket      bool
	PersistentStorage bool
	MinMemory         string
	Arch              string
}

// ProvidesSpec declares what a module offers.
type ProvidesSpec struct {
	Capabilities map[string]bool
	Middleware   map[string]MiddlewareDef
	Endpoints    map[string]EndpointDef
}

// MiddlewareDef represents a Traefik middleware provided by a module.
type MiddlewareDef struct {
	Type        string
	Description string
}

// EndpointDef represents an endpoint provided by a module.
type EndpointDef struct {
	URL         string
	Internal    bool
	Description string
}

// SettingsSpec holds perma (immutable) and flexible (changeable) settings.
type SettingsSpec struct {
	Perma    map[string]any
	Flexible map[string]any
}

// ProvisionerDef represents a one-shot provisioner container.
type ProvisionerDef struct {
	Image       string
	Command     string
	DependsOn   string
	Networks    []string
	Environment map[string]string
}

// ModuleReader reads and extracts ModuleContracts from CUE module definitions.
type ModuleReader struct {
	ctx *cue.Context
}

// NewModuleReader creates a new ModuleReader.
func NewModuleReader() *ModuleReader {
	return &ModuleReader{
		ctx: cuecontext.New(),
	}
}

// ReadModule reads a single module from the given directory (must contain module.cue).
func (r *ModuleReader) ReadModule(modulePath string) (ModuleContract, error) {
	return r.readModule(modulePath)
}

// ReadAllModules scans the modules directory and extracts all ModuleContracts.
func (r *ModuleReader) ReadAllModules(modulesDir string) ([]ModuleContract, error) {
	modulePaths, err := discoverModulePaths(modulesDir)
	if err != nil {
		return nil, err
	}

	var contracts []ModuleContract
	for _, mp := range modulePaths {
		contract, err := r.readModule(mp.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to read module %s: %w", mp.Name, err)
		}
		contracts = append(contracts, contract)
	}

	return contracts, nil
}

// readModule loads a single module's CUE and extracts its Contract.
func (r *ModuleReader) readModule(modulePath string) (ModuleContract, error) {
	cfg := cueLoadConfig(modulePath, modulePath)

	instances := load.Instances([]string{"."}, cfg)
	if len(instances) == 0 {
		return ModuleContract{}, fmt.Errorf("no CUE files found in %s", modulePath)
	}

	inst := instances[0]
	if inst.Err != nil {
		return ModuleContract{}, fmt.Errorf("failed to load CUE instance: %w", inst.Err)
	}

	value := r.ctx.BuildInstance(inst)
	if err := value.Err(); err != nil {
		return ModuleContract{}, fmt.Errorf("failed to build CUE value: %w", err)
	}

	contract := value.LookupPath(cue.ParsePath("Contract"))
	if !contract.Exists() {
		return ModuleContract{}, fmt.Errorf("module at %s has no Contract definition", modulePath)
	}

	return r.extractContract(contract)
}

// extractContract extracts a ModuleContract from a CUE Contract value.
func (r *ModuleReader) extractContract(v cue.Value) (ModuleContract, error) {
	mc := ModuleContract{
		Enabled:  true,
		Services: make(map[string]ServiceDef),
	}

	// Metadata
	if meta := v.LookupPath(cue.ParsePath("metadata")); meta.Exists() {
		mc.Metadata = r.extractMetadata(meta)
	}

	// Enabled
	if en := v.LookupPath(cue.ParsePath("enabled")); en.Exists() {
		b, _ := en.Bool()
		mc.Enabled = b
	}

	// Requires
	if req := v.LookupPath(cue.ParsePath("requires")); req.Exists() {
		mc.Requires = r.extractRequires(req)
	}

	// Provides
	if prov := v.LookupPath(cue.ParsePath("provides")); prov.Exists() {
		mc.Provides = r.extractProvides(prov)
	}

	// Settings
	if settings := v.LookupPath(cue.ParsePath("settings")); settings.Exists() {
		mc.Settings = r.extractSettings(settings)
	}

	// Services
	if services := v.LookupPath(cue.ParsePath("services")); services.Exists() {
		iter, err := services.Fields(cue.Optional(true))
		if err != nil {
			return mc, fmt.Errorf("failed to iterate services: %w", err)
		}
		for iter.Next() {
			svcName := strings.Trim(iter.Selector().String(), "\"")
			svc, err := r.extractServiceDef(iter.Value())
			if err != nil {
				return mc, fmt.Errorf("failed to extract service %s: %w", svcName, err)
			}
			mc.Services[svcName] = svc
		}
	}

	// Provisioners
	if provs := v.LookupPath(cue.ParsePath("provisioners")); provs.Exists() {
		mc.Provisioners = make(map[string]ProvisionerDef)
		iter, err := provs.Fields(cue.Optional(true))
		if err != nil {
			return mc, fmt.Errorf("failed to iterate provisioners: %w", err)
		}
		for iter.Next() {
			name := strings.Trim(iter.Selector().String(), "\"")
			prov := r.extractProvisioner(iter.Value())
			mc.Provisioners[name] = prov
		}
	}

	return mc, nil
}

func (r *ModuleReader) extractMetadata(v cue.Value) ModuleMetadata {
	meta := ModuleMetadata{Core: true}
	meta.Name = stringField(v, "name")
	meta.DisplayName = stringField(v, "displayName")
	meta.Version = stringField(v, "version")
	meta.Layer = stringField(v, "layer")
	meta.Description = stringField(v, "description")
	if core := v.LookupPath(cue.ParsePath("core")); core.Exists() {
		b, _ := core.Bool()
		meta.Core = b
	}
	return meta
}

func (r *ModuleReader) extractRequires(v cue.Value) *RequiresSpec {
	req := &RequiresSpec{
		Services: make(map[string]RequiredService),
	}

	if svcs := v.LookupPath(cue.ParsePath("services")); svcs.Exists() {
		iter, _ := svcs.Fields(cue.Optional(true))
		for iter.Next() {
			name := strings.Trim(iter.Selector().String(), "\"")
			rs := RequiredService{}
			rs.MinVersion = stringField(iter.Value(), "minVersion")
			rs.Optional = false
			if opt := iter.Value().LookupPath(cue.ParsePath("optional")); opt.Exists() {
				b, _ := opt.Bool()
				rs.Optional = b
			}
			if provides := iter.Value().LookupPath(cue.ParsePath("provides")); provides.Exists() {
				listIter, _ := provides.List()
				for listIter.Next() {
					s, _ := listIter.Value().String()
					rs.Provides = append(rs.Provides, s)
				}
			}
			req.Services[name] = rs
		}
	}

	if infra := v.LookupPath(cue.ParsePath("infrastructure")); infra.Exists() {
		req.Infrastructure = InfraRequirements{}
		if docker := infra.LookupPath(cue.ParsePath("docker")); docker.Exists() {
			b, _ := docker.Bool()
			req.Infrastructure.Docker = b
		}
		req.Infrastructure.Network = stringField(infra, "network")
		if ds := infra.LookupPath(cue.ParsePath("dockerSocket")); ds.Exists() {
			b, _ := ds.Bool()
			req.Infrastructure.DockerSocket = b
		}
		if ps := infra.LookupPath(cue.ParsePath("persistentStorage")); ps.Exists() {
			b, _ := ps.Bool()
			req.Infrastructure.PersistentStorage = b
		}
		req.Infrastructure.MinMemory = stringField(infra, "minMemory")
		req.Infrastructure.Arch = stringField(infra, "arch")
	}

	return req
}

func (r *ModuleReader) extractProvides(v cue.Value) *ProvidesSpec {
	prov := &ProvidesSpec{
		Capabilities: make(map[string]bool),
		Middleware:   make(map[string]MiddlewareDef),
		Endpoints:    make(map[string]EndpointDef),
	}

	if caps := v.LookupPath(cue.ParsePath("capabilities")); caps.Exists() {
		iter, _ := caps.Fields(cue.Optional(true))
		for iter.Next() {
			name := strings.Trim(iter.Selector().String(), "\"")
			b, _ := iter.Value().Bool()
			prov.Capabilities[name] = b
		}
	}

	if mw := v.LookupPath(cue.ParsePath("middleware")); mw.Exists() {
		iter, _ := mw.Fields(cue.Optional(true))
		for iter.Next() {
			name := strings.Trim(iter.Selector().String(), "\"")
			md := MiddlewareDef{
				Type:        stringField(iter.Value(), "type"),
				Description: stringField(iter.Value(), "description"),
			}
			prov.Middleware[name] = md
		}
	}

	if eps := v.LookupPath(cue.ParsePath("endpoints")); eps.Exists() {
		iter, _ := eps.Fields(cue.Optional(true))
		for iter.Next() {
			name := strings.Trim(iter.Selector().String(), "\"")
			ep := EndpointDef{
				URL:         stringField(iter.Value(), "url"),
				Description: stringField(iter.Value(), "description"),
			}
			if internal := iter.Value().LookupPath(cue.ParsePath("internal")); internal.Exists() {
				b, _ := internal.Bool()
				ep.Internal = b
			}
			prov.Endpoints[name] = ep
		}
	}

	return prov
}

func (r *ModuleReader) extractSettings(v cue.Value) *SettingsSpec {
	s := &SettingsSpec{
		Perma:    make(map[string]any),
		Flexible: make(map[string]any),
	}

	if perma := v.LookupPath(cue.ParsePath("perma")); perma.Exists() {
		iter, _ := perma.Fields(cue.Optional(true))
		for iter.Next() {
			name := strings.Trim(iter.Selector().String(), "\"")
			s.Perma[name] = cueValueToGo(iter.Value())
		}
	}

	if flex := v.LookupPath(cue.ParsePath("flexible")); flex.Exists() {
		iter, _ := flex.Fields(cue.Optional(true))
		for iter.Next() {
			name := strings.Trim(iter.Selector().String(), "\"")
			s.Flexible[name] = cueValueToGo(iter.Value())
		}
	}

	return s
}

func (r *ModuleReader) extractServiceDef(v cue.Value) (ServiceDef, error) {
	// Reuse the existing extractService logic from Extractor
	ext := &Extractor{ctx: r.ctx}
	return ext.extractService(v)
}

func (r *ModuleReader) extractProvisioner(v cue.Value) ProvisionerDef {
	p := ProvisionerDef{
		Environment: make(map[string]string),
	}
	p.Image = stringField(v, "image")
	p.Command = stringField(v, "command")
	p.DependsOn = stringField(v, "dependsOn")

	if nets := v.LookupPath(cue.ParsePath("networks")); nets.Exists() {
		iter, _ := nets.List()
		for iter.Next() {
			s, _ := iter.Value().String()
			p.Networks = append(p.Networks, s)
		}
	}

	if env := v.LookupPath(cue.ParsePath("environment")); env.Exists() {
		iter, _ := env.Fields()
		for iter.Next() {
			k := strings.Trim(iter.Selector().String(), "\"")
			s, _ := iter.Value().String()
			p.Environment[k] = s
		}
	}

	return p
}

// cueValueToGo extracts a concrete Go value from a CUE value.
func cueValueToGo(v cue.Value) any {
	if s, err := v.String(); err == nil {
		return s
	}
	if b, err := v.Bool(); err == nil {
		return b
	}
	if n, err := v.Int64(); err == nil {
		return n
	}
	if f, err := v.Float64(); err == nil {
		return f
	}
	return nil
}

// ModulesByName returns a map of module name → ModuleContract for quick lookup.
func ModulesByName(modules []ModuleContract) map[string]ModuleContract {
	m := make(map[string]ModuleContract, len(modules))
	for _, mod := range modules {
		m[mod.Metadata.Name] = mod
	}
	return m
}
