// Package backuphooks defines database pre/post-snapshot hooks.
//
// These hooks run automatically based on which DB engines are deployed in
// the surrounding StackKit. They are intentionally NOT exposed in #Config:
// users do not pick "use Litestream" or "use pgBackRest" — the runtime decides.
//
// Strategy per engine:
//   - SQLite   : `sqlite3 .backup` to a consistent copy before snapshot.
//                Cheap, atomic, no second tool. The output file is what
//                Kopia snapshots.
//   - Postgres : `pg_dump --format=custom` (`pg_dumpall` for global roles
//                separately). Custom format restores cleanly via
//                `pg_restore` and is what most users want.
//   - Redis    : `BGSAVE` then poll `LASTSAVE` until the dump file rotates.
//                Caches like Immich's redis are quiesce-or-skip.
//   - MariaDB  : `mariadb-dump --single-transaction --routines --events`.
//                Defensive: most basement-kit deployments do not include MariaDB,
//                but the hook is here for users who add it.
//   - MongoDB  : `mongodump` against an internal admin user.
//
// Detection rule: StackKit generation walks the deployed module list at apply
// time and matches container images / volume mount paths against the patterns below.
// A container that matches more than one pattern (e.g. an app that bundles
// both sqlite and a redis cache) gets multiple hooks — they run in series.
//
// Output-path rule (binding): every hook output MUST land inside a docker
// named volume. The kopia-agent snapshots /var/lib/docker/volumes read-only —
// container tmpfs paths are invisible to it, so a tmpfs dump would silently
// never be backed up. Engine defaults therefore point into the engine's own
// data-directory volume (e.g. $PGDATA/dbsnap for Postgres); env-var forms are
// expanded by the container shell at hook time.

package backuphooks

// #DBHook describes one pre-snapshot quiesce step. Multiple hooks per
// container are allowed; they execute in declaration order.
#DBHook: {
	// Engine kind drives the command template.
	engine: "sqlite" | "postgres" | "redis" | "mariadb" | "mongodb"

	// Container the hook attaches to (matched by service name in the
	// generated docker-compose).
	container: string

	// Detection patterns. Runtime wiring uses these to discover
	// hook targets without the user listing them by hand.
	detect: {
		// Container image regex (e.g. "^postgres:" or "vaultwarden/server").
		imagePattern?: string

		// Volume mount path inside the container that hints at the engine
		// (e.g. "/data/db.sqlite3" → sqlite).
		volumePattern?: string

		// Explicit env var name that, if present, identifies the engine
		// (e.g. POSTGRES_DB).
		envVar?: string
	}

	// Engine-specific settings.
	if engine == "sqlite" {
		sqlite: {
			// Path inside the container to the SQLite file.
			dbFile: string

			// Target for the consistent copy. Must satisfy the output-path
			// rule; the empty default means "next to dbFile" (which always
			// lives in a volume), i.e. <dbFile>.dbsnap.
			outFile: string | *""
		}
	}

	if engine == "postgres" {
		postgres: {
			// Database name (defaults to $POSTGRES_DB).
			database: string | *"$POSTGRES_DB"

			// Connection user — must have read on all schemas.
			user: string | *"$POSTGRES_USER"

			// pg_dump output path. Lives inside the PGDATA volume so the
			// kopia-agent's docker-volumes mount sees it (see output-path
			// rule above); Postgres ignores foreign subdirectories in its
			// data directory.
			outFile: string | *"$PGDATA/dbsnap/pg.dump"

			// Whether to also run pg_dumpall for roles/tablespaces.
			includeGlobals: bool | *true
		}
	}

	if engine == "redis" {
		redis: {
			// Path Redis writes its RDB to (Kopia snapshots this dir).
			dumpDir: string | *"/data"

			// Maximum seconds to wait for BGSAVE to complete.
			bgsaveTimeout: int | *30

			// If Redis is purely a cache (e.g. Immich), it's safe to
			// skip the wait and just snapshot whatever is on disk.
			cacheOnly: bool | *false
		}
	}

	if engine == "mariadb" {
		mariadb: {
			user: string | *"$MARIADB_USER"
			// Inside the MariaDB datadir volume (output-path rule).
			outFile: string | *"/var/lib/mysql/dbsnap/mariadb.sql"
		}
	}

	if engine == "mongodb" {
		mongodb: {
			// Inside the MongoDB data volume (output-path rule).
			outDir: string | *"/data/db/dbsnap"
		}
	}
}

// #BuiltinHooks lists the default detection rules for the supported StackKit
// catalog. The runtime merges these with any user-added containers it discovers.
#BuiltinHooks: [...#DBHook] & [
	// Vaultwarden — sqlite by default, postgres optional. The runtime
	// re-evaluates `detect` at apply time and picks the right branch.
	{
		engine:    "sqlite"
		container: "vaultwarden"
		detect: {
			imagePattern:  "^vaultwarden/server"
			volumePattern: "/data/db.sqlite3"
		}
		sqlite: dbFile: "/data/db.sqlite3"
	},
	// Jellyfin — sqlite catalog.
	{
		engine:    "sqlite"
		container: "jellyfin"
		detect: {
			imagePattern:  "^jellyfin/jellyfin"
			volumePattern: "/config/data/jellyfin.db"
		}
		sqlite: dbFile: "/config/data/jellyfin.db"
	},
	// Home Assistant — sqlite by default (Postgres is opt-in).
	{
		engine:    "sqlite"
		container: "homeassistant"
		detect: {
			imagePattern:  "^homeassistant/home-assistant"
			volumePattern: "/config/home-assistant_v2.db"
		}
		sqlite: dbFile: "/config/home-assistant_v2.db"
	},
	// Stalwart — sqlite store.
	{
		engine:    "sqlite"
		container: "stalwart"
		detect: {
			imagePattern: "^stalwartlabs/mail-server"
		}
		sqlite: dbFile: "/opt/stalwart-mail/data/index.sqlite3"
	},
	// Gitea — defaults to sqlite, supports postgres.
	{
		engine:    "sqlite"
		container: "gitea"
		detect: {
			imagePattern: "^gitea/gitea"
		}
		sqlite: dbFile: "/data/gitea/gitea.db"
	},
	// Immich — postgres, primary data store.
	{
		engine:    "postgres"
		container: "immich-postgres"
		detect: {
			imagePattern: "^postgres|^tensorchord/pgvecto-rs"
			envVar:       "POSTGRES_DB"
		}
	},
	// Immich — redis cache. Cache-only ⇒ no BGSAVE wait.
	{
		engine:    "redis"
		container: "immich-redis"
		detect: {
			imagePattern: "^redis"
		}
		redis: cacheOnly: true
	},
	// Dokploy — postgres state.
	{
		engine:    "postgres"
		container: "dokploy-postgres"
		detect: {
			imagePattern: "^postgres"
			envVar:       "POSTGRES_DB"
		}
	},
]
