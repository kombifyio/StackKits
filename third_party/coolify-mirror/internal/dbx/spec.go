// Package dbx exports the database rows that make up selected Coolify resources
// and imports them into another Coolify instance, remapping ids and
// re-encrypting secrets for the target APP_KEY.
package dbx

import "github.com/mtalavi/coolify-mirror/internal/coolify"

// fk describes one reference column. Exactly one of Table / MorphCol is set:
// Table for a plain foreign key, MorphCol for a polymorphic pair (the column
// holding the model class, e.g. resource_type).
type fk struct {
	Col      string
	Table    string
	MorphCol string
	// AsText: the column stores the id as a string (application_deployment_queues).
	AsText bool
	// Optional: only newer Coolify versions have this column.
	Optional bool
}

const (
	refTeam   = "@team"   // always the target team
	refServer = "@server" // always the target localhost server (id 0)
	refNull   = "@null"   // always NULL on import
)

// foreignKeys lists every id-carrying column of the tables this tool moves.
// Columns not listed here are copied verbatim.
var foreignKeys = map[string][]fk{
	"s3_storages":      {{Col: "team_id", Table: refTeam}},
	"private_keys":     {{Col: "team_id", Table: refTeam}},
	"github_apps":      {{Col: "team_id", Table: refTeam}, {Col: "private_key_id", Table: "private_keys"}},
	"gitlab_apps":      {{Col: "team_id", Table: refTeam}, {Col: "private_key_id", Table: "private_keys"}},
	"projects":         {{Col: "team_id", Table: refTeam}, {Col: "icon_s3_storage_id", Table: "s3_storages"}},
	"project_settings": {{Col: "project_id", Table: "projects"}},
	"environments":     {{Col: "project_id", Table: "projects"}},
	"shared_environment_variables": {
		{Col: "team_id", Table: refTeam}, {Col: "project_id", Table: "projects"},
		{Col: "environment_id", Table: "environments"}, {Col: "server_id", Table: refServer},
	},
	"standalone_dockers": {{Col: "server_id", Table: refServer}},
	"tags":               {{Col: "team_id", Table: refTeam}},
	"applications": {
		{Col: "environment_id", Table: "environments"},
		{Col: "destination_id", MorphCol: "destination_type"},
		{Col: "source_id", MorphCol: "source_type"},
		{Col: "private_key_id", Table: "private_keys"},
	},
	"application_settings": {{Col: "application_id", Table: "applications"}},
	"application_deployment_queues": {
		{Col: "application_id", Table: "applications", AsText: true},
		{Col: "destination_id", Table: "standalone_dockers", AsText: true},
		{Col: "server_id", Table: refServer},
		{Col: "build_server_id", Table: refNull},
	},
	"services": {
		{Col: "environment_id", Table: "environments"},
		{Col: "server_id", Table: refServer},
		{Col: "destination_id", MorphCol: "destination_type"},
	},
	"service_applications":  {{Col: "service_id", Table: "services"}},
	"service_databases":     {{Col: "service_id", Table: "services"}},
	"environment_variables": {{Col: "resourceable_id", MorphCol: "resourceable_type"}},
	"local_persistent_volumes": {
		{Col: "resource_id", MorphCol: "resource_type"},
		// The volume of a SQLite database connected to an application (4.4+).
		{Col: "standalone_sqlite_id", Table: "standalone_sqlites", Optional: true},
	},
	"local_file_volumes": {{Col: "resource_id", MorphCol: "resource_type"}},
	"scheduled_tasks": {
		{Col: "application_id", Table: "applications"}, {Col: "service_id", Table: "services"},
		{Col: "team_id", Table: refTeam},
	},
	"scheduled_database_backups": {
		{Col: "database_id", MorphCol: "database_type"}, {Col: "s3_storage_id", Table: "s3_storages"},
		{Col: "team_id", Table: refTeam},
	},
	"scheduled_volume_backups": {
		{Col: "backupable_id", MorphCol: "backupable_type"}, {Col: "s3_storage_id", Table: "s3_storages"},
		{Col: "team_id", Table: refTeam},
	},
	"taggables": {{Col: "tag_id", Table: "tags"}, {Col: "taggable_id", MorphCol: "taggable_type"}},
	// Secret managers (Doppler, Infisical, Vault; 4.4+): the team's token and
	// the link that makes a resource read {{vault.KEY}} values from it.
	"integration_tokens": {{Col: "team_id", Table: refTeam}},
	"secret_manager_links": {
		{Col: "resourceable_id", MorphCol: "resourceable_type"},
		{Col: "integration_token_id", Table: "integration_tokens"},
	},
}

func init() {
	for _, d := range coolify.DatabaseKinds {
		foreignKeys[d.Table] = []fk{
			{Col: "environment_id", Table: "environments"},
			{Col: "destination_id", MorphCol: "destination_type"},
		}
	}
}

// insertOrder is the order rows are inserted in (parents before children).
var insertOrder = func() []string {
	order := []string{
		"s3_storages", "integration_tokens", "private_keys", "github_apps", "gitlab_apps",
		"projects", "project_settings", "environments", "shared_environment_variables",
		"standalone_dockers", "tags",
	}
	for _, d := range coolify.DatabaseKinds {
		order = append(order, d.Table)
	}
	return append(order,
		"services", "service_applications", "service_databases",
		"applications", "application_settings", "application_deployment_queues",
		"environment_variables", "secret_manager_links", "local_persistent_volumes", "local_file_volumes",
		"scheduled_tasks", "scheduled_database_backups", "scheduled_volume_backups",
		"taggables",
	)
}()

// uuidColumn names the unique public id column of a table ("" = none).
func uuidColumn(table string) string {
	switch table {
	case "application_deployment_queues":
		return "deployment_uuid"
	case "taggables", "project_settings", "application_settings":
		return ""
	}
	return "uuid"
}

// resourceTables are the tables whose rows are user-facing resources.
func isResourceTable(t string) bool {
	switch t {
	case "applications", "services", "service_applications", "service_databases":
		return true
	}
	_, ok := coolify.DatabaseKindByTable(t)
	return ok
}
