package architecturev2renderer

// GameDataTarget is where Wings and its configuration writer see the game
// data volume; the executor additionally mounts it at its own host path.
const GameDataTarget = "/stackkit/game-data"

// GameDataHostPathEnv names the host path of Wings' data volume, which the
// executor exports to the components that write Wings' configuration.
const GameDataHostPathEnv = "STACKKIT_GAME_DATA_HOST_PATH"

// GameNodeModule is the ADR-0043 game-node shape of one admitted Game
// platform module (ADR-0048). Its Wings component alone owns game containers
// through the approved Docker socket and keeps its data at its own host path.
type GameNodeModule struct {
	ModuleRef string
	// WingsComponent receives the Docker socket and the self-path data volume.
	WingsComponent string
	// ConfigWriter mounts Wings' data volume to converge Wings' configuration.
	ConfigWriter string
	// ConfigWriterShares are the other components' volumes ConfigWriter
	// mounts, as "component/volume"; Wings' data is always among them.
	ConfigWriterShares []string
	// LoopbackComponent resolves the route host to loopback; empty when the
	// Panel proxies the node itself.
	LoopbackComponent string
	// RootComponent runs as root although its image declares another user,
	// because it writes into the root-owned Wings data volume.
	RootComponent string
	// SIGTERMComponents replace an image stop signal the backup quiesce owner
	// does not admit.
	SIGTERMComponents []string
	// ReadableConfigFiles makes the governed startup files readable by an
	// unprivileged image user; they carry no secret material, which reaches
	// the containers only as custody files.
	ReadableConfigFiles bool
}

var gameNodeModules = map[string]GameNodeModule{
	pterodactylWorkloadModuleID: {
		ModuleRef: pterodactylWorkloadModuleID, WingsComponent: "wings", ConfigWriter: "panel-bootstrap",
		ConfigWriterShares: []string{"wings/data"}, LoopbackComponent: "panel", SIGTERMComponents: []string{"panel", "panel-bootstrap"},
	},
	pelicanWorkloadModuleID: {
		// The bootstrap converges the Panel's SQLite database in place.
		ModuleRef: pelicanWorkloadModuleID, WingsComponent: "wings", ConfigWriter: "panel-bootstrap",
		ConfigWriterShares: []string{"wings/data", "panel/data"}, LoopbackComponent: "panel", RootComponent: "panel-bootstrap",
		SIGTERMComponents: []string{"panel", "panel-bootstrap"},
		// The Pelican Panel image runs as www-data.
		ReadableConfigFiles: true,
	},
	calagopusWorkloadModuleID: {
		ModuleRef: calagopusWorkloadModuleID, WingsComponent: "wings", ConfigWriter: "wings-bootstrap",
		ConfigWriterShares: []string{"wings/data"},
	},
}

// GameNodeModuleFor returns the game-node shape of moduleRef; only the
// admitted Game platform modules have one.
func GameNodeModuleFor(moduleRef string) (GameNodeModule, bool) {
	module, ok := gameNodeModules[moduleRef]
	return module, ok
}
