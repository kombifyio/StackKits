package coolify

// Morph class names stored in Coolify's polymorphic *_type columns.
const (
	MorphApplication        = `App\Models\Application`
	MorphApplicationPreview = `App\Models\ApplicationPreview`
	MorphService            = `App\Models\Service`
	MorphServiceApplication = `App\Models\ServiceApplication`
	MorphServiceDatabase    = `App\Models\ServiceDatabase`
	MorphStandaloneDocker   = `App\Models\StandaloneDocker`
	MorphSwarmDocker        = `App\Models\SwarmDocker`
	MorphGithubApp          = `App\Models\GithubApp`
	MorphGitlabApp          = `App\Models\GitlabApp`
	MorphPersistentVolume   = `App\Models\LocalPersistentVolume`
	MorphFileVolume         = `App\Models\LocalFileVolume`
)

// DatabaseKind describes one standalone database type.
type DatabaseKind struct {
	Table string
	Kind  string
	Morph string
	// Since is the first Coolify version with this type ("" = every supported
	// version). Older versions have no such table.
	Since string
}

// DatabaseKinds lists every standalone database table Coolify has.
var DatabaseKinds = []DatabaseKind{
	{"standalone_postgresqls", "postgresql", `App\Models\StandalonePostgresql`, ""},
	{"standalone_mysqls", "mysql", `App\Models\StandaloneMysql`, ""},
	{"standalone_mariadbs", "mariadb", `App\Models\StandaloneMariadb`, ""},
	{"standalone_mongodbs", "mongodb", `App\Models\StandaloneMongodb`, ""},
	{"standalone_redis", "redis", `App\Models\StandaloneRedis`, ""},
	{"standalone_keydbs", "keydb", `App\Models\StandaloneKeydb`, ""},
	{"standalone_dragonflies", "dragonfly", `App\Models\StandaloneDragonfly`, ""},
	{"standalone_clickhouses", "clickhouse", `App\Models\StandaloneClickhouse`, ""},
	{"standalone_sqlites", "sqlite", `App\Models\StandaloneSqlite`, "4.4.0"},
}

// OptionalTables are tables that only newer Coolify versions have (the tool
// still supports versions without them).
var OptionalTables = map[string]bool{
	"standalone_sqlites": true, "integration_tokens": true, "secret_manager_links": true,
}

// MorphToTable maps a morph class to its table.
var MorphToTable = map[string]string{
	MorphApplication:        "applications",
	MorphApplicationPreview: "application_previews",
	MorphService:            "services",
	MorphServiceApplication: "service_applications",
	MorphServiceDatabase:    "service_databases",
	MorphStandaloneDocker:   "standalone_dockers",
	MorphSwarmDocker:        "swarm_dockers",
	MorphGithubApp:          "github_apps",
	MorphGitlabApp:          "gitlab_apps",
	MorphPersistentVolume:   "local_persistent_volumes",
	MorphFileVolume:         "local_file_volumes",
}

// TableToMorph is the inverse of MorphToTable.
var TableToMorph = map[string]string{}

func init() {
	for _, d := range DatabaseKinds {
		MorphToTable[d.Morph] = d.Table
	}
	for m, t := range MorphToTable {
		TableToMorph[t] = m
	}
}

// DatabaseKindByTable returns the database kind for a standalone_* table.
func DatabaseKindByTable(table string) (DatabaseKind, bool) {
	for _, d := range DatabaseKinds {
		if d.Table == table {
			return d, true
		}
	}
	return DatabaseKind{}, false
}
