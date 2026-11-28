package engine

import (
	"sort"
	"strings"
)

// AdapterKind is the namespace of an adapter reference.
type AdapterKind string

const (
	// AdapterSetup is an owner-triggered application setup action (the
	// optional tier of the two-tier lifecycle).
	AdapterSetup AdapterKind = "setup"
	// AdapterHook is a backup or restore hook around a snapshot.
	AdapterHook AdapterKind = "hook"
	// AdapterProbe is a readiness or consistency probe.
	AdapterProbe AdapterKind = "probe"
)

// Adapter is one entry of the closed adapter registry. Owner names the
// package that executes it today; the engine only admits the reference.
type Adapter struct {
	Ref   string
	Kind  AdapterKind
	Owner string
}

// adapters is the closed AdapterRegistry. An #AppModuleV3 may reference only
// these; a new adapter is added here together with its executing owner.
var adapters = map[string]Adapter{
	"setup/cloudreve-owner-bootstrap":      {Ref: "setup/cloudreve-owner-bootstrap", Kind: AdapterSetup, Owner: "internal/appsetup (cloudreve_owner.go)"},
	"setup/immich-owner-bootstrap":         {Ref: "setup/immich-owner-bootstrap", Kind: AdapterSetup, Owner: "internal/appsetup (immich_owner.go)"},
	"setup/home-assistant-owner-bootstrap": {Ref: "setup/home-assistant-owner-bootstrap", Kind: AdapterSetup, Owner: "internal/appsetup (home_assistant_owner.go)"},
	// Native v2 backups stop the workload's containers, snapshot the volumes
	// crash-consistently and start them again (internal/backupexec).
	"hook/quiesce-stop": {Ref: "hook/quiesce-stop", Kind: AdapterHook, Owner: "internal/backupexec"},
	// The route health gate's HTTP status probe through the loopback health
	// port (internal/runtimeexecutor/nativehost readiness).
	"probe/http-status": {Ref: "probe/http-status", Kind: AdapterProbe, Owner: "internal/runtimeexecutor/nativehost"},
}

// LookupAdapter returns the registered adapter for ref and verifies its
// namespace.
func LookupAdapter(kind AdapterKind, ref string) (Adapter, bool) {
	adapter, ok := adapters[ref]
	if !ok || adapter.Kind != kind || !strings.HasPrefix(ref, string(kind)+"/") {
		return Adapter{}, false
	}
	return adapter, true
}

// AdapterRefs lists the closed registry in order.
func AdapterRefs() []string {
	refs := make([]string, 0, len(adapters))
	for ref := range adapters {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}
