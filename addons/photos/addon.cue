// Package photos - Photo Gallery Add-On
//
// Immich: High-performance self-hosted photo/video management.
// Google Photos alternative with ML-powered features.
//
// License: AGPL-3.0
// Placement: Local node (storage-heavy, GPU optional for ML)
//
// Usage:
//   addons: photos: photos.#Config & {
//       machineLearning: enabled: true
//   }

package photos

// #Config defines photos add-on configuration
#Config: {
	_addon: {
		name:        "photos"
		displayName: "Photo Gallery"
		version:     "1.0.0"
		layer:       "APPLICATION"
		description: "Immich - Self-hosted photo and video management"
	}

	enabled: bool | *true

	// Machine learning features (face detection, object recognition)
	machineLearning: {
		enabled: bool | *true
		// Use GPU for ML inference
		gpu: bool | *false
	}

	// Storage configuration
	storage: {
		photosPath:  string | *"/data/photos"
		thumbsPath:  string | *"/data/photos/thumbs"
		uploadPath:  string | *"/data/photos/upload"
		externalLibraries: [...string] | *[]
	}

	// Database (Postgres required by Immich)
	database: {
		// Embedded Postgres
		embedded: bool | *true
	}

	// Resource limits
	resources: {
		server: {
			memory: string | *"2048m"
			cpus:   number | *2.0
		}
		ml: {
			memory: string | *"2048m"
			cpus:   number | *2.0
		}
	}
}

// Service definitions

#ImmichServerService: {
	name:        "immich-server"
	displayName: "Immich Server"
	image:       "ghcr.io/immich-app/immich-server:v1.124.2"
	category:    "application"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 2283, host: 2283, protocol: "tcp", name: "web"},
	]

	volumes: [
		{name: "immich-upload", path: "/usr/src/app/upload", type: "volume"},
		{host: "/etc/localtime", path: "/etc/localtime", type: "bind", readOnly: true},
	]

	traefik: {
		enabled: true
		rule:    "Host(`photos.{{.domain}}`)"
	}

	environment: {
		DB_HOSTNAME: "immich-postgres"
		DB_USERNAME: "postgres"
		DB_DATABASE_NAME: "immich"
		REDIS_HOSTNAME: "immich-redis"
	}
}

#ImmichMLService: {
	name:        "immich-ml"
	displayName: "Immich ML"
	image:       "ghcr.io/immich-app/immich-machine-learning:v1.124.2"
	category:    "application"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	volumes: [
		{name: "immich-model-cache", path: "/cache", type: "volume"},
	]
}

#ImmichPostgresService: {
	name:        "immich-postgres"
	displayName: "Immich PostgreSQL"
	image:       "tensorchord/pgvecto-rs:pg16-v0.3.0"
	category:    "database"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	volumes: [
		{name: "immich-pgdata", path: "/var/lib/postgresql/data", type: "volume"},
	]

	environment: {
		POSTGRES_PASSWORD: string
		POSTGRES_USER:     "postgres"
		POSTGRES_DB:       "immich"
	}
}

#ImmichRedisService: {
	name:        "immich-redis"
	displayName: "Immich Redis"
	image:       "redis:7-alpine"
	category:    "cache"

	placement: {
		nodeType: "local"
		strategy: "single"
	}
}

#Outputs: {
	url:      string | *"https://photos.{{.domain}}"
	mlEnabled: bool
}
