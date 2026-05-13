// Package media - Media Streaming Add-On
//
// Private media streaming with automatic content management:
//   - Jellyfin: Media server (video, music, photos)
//   - Sonarr: TV series management
//   - Radarr: Movie management
//   - Prowlarr: Indexer management
//   - Bazarr: Subtitle management (optional)
//
// License:
//   - Jellyfin: GPL-2.0
//   - Sonarr: GPL-3.0
//   - Radarr: GPL-3.0
//   - Prowlarr: GPL-3.0
//   - Bazarr: GPL-3.0
//
// Placement: Local node (storage-heavy, GPU for transcoding)
//
// Usage:
//   addons: media: media.#Config & {
//       jellyfin: hardwareTranscoding: true
//       arr: sonarr: enabled: true
//   }

package media

// #Config defines media add-on configuration
#Config: {
	_addon: {
		name:        "media"
		displayName: "Media Streaming"
		version:     "1.0.0"
		layer:       "APPLICATION"
		description: "Jellyfin + *arr stack for private streaming"
	}

	enabled: bool | *true

	// Jellyfin configuration
	jellyfin: #JellyfinConfig

	// *arr stack configuration
	arr: #ArrConfig

	// Storage paths
	storage: {
		mediaPath:    string | *"/data/media"
		moviesPath:   string | *"/data/media/movies"
		tvPath:       string | *"/data/media/tv"
		musicPath:    string | *"/data/media/music"
		downloadPath: string | *"/data/downloads"
	}
}

#JellyfinConfig: {
	enabled: bool | *true

	// Hardware transcoding
	hardwareTranscoding: bool | *false

	// GPU device (for transcoding)
	gpuDevice?: string

	// Resource limits
	resources: {
		memory: string | *"2048m"
		cpus:   number | *2.0
	}
}

#ArrConfig: {
	// Sonarr (TV series)
	sonarr: {
		enabled: bool | *true
	}

	// Radarr (Movies)
	radarr: {
		enabled: bool | *true
	}

	// Prowlarr (Indexer manager)
	prowlarr: {
		enabled: bool | *true
	}

	// Bazarr (Subtitles)
	bazarr: {
		enabled: bool | *false
	}
}

// Service definitions

#JellyfinService: {
	name:        "jellyfin"
	displayName: "Jellyfin"
	image:       "jellyfin/jellyfin:10.10.3"
	category:    "media"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 8096, host: 8096, protocol: "tcp", name: "web"},
	]

	volumes: [
		{name: "jellyfin-config", path: "/config", type: "volume"},
		{name: "jellyfin-cache", path: "/cache", type: "volume"},
		{host: "/data/media", path: "/media", type: "bind"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`media.{{.domain}}`)"
	}
}

#SonarrService: {
	name:        "sonarr"
	displayName: "Sonarr"
	image:       "linuxserver/sonarr:4"
	category:    "media"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 8989, host: 8989, protocol: "tcp", name: "web"},
	]

	volumes: [
		{name: "sonarr-config", path: "/config", type: "volume"},
		{host: "/data/media/tv", path: "/tv", type: "bind"},
		{host: "/data/downloads", path: "/downloads", type: "bind"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`sonarr.{{.domain}}`)"
	}
}

#RadarrService: {
	name:        "radarr"
	displayName: "Radarr"
	image:       "linuxserver/radarr:5"
	category:    "media"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 7878, host: 7878, protocol: "tcp", name: "web"},
	]

	volumes: [
		{name: "radarr-config", path: "/config", type: "volume"},
		{host: "/data/media/movies", path: "/movies", type: "bind"},
		{host: "/data/downloads", path: "/downloads", type: "bind"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`radarr.{{.domain}}`)"
	}
}

#ProwlarrService: {
	name:        "prowlarr"
	displayName: "Prowlarr"
	image:       "linuxserver/prowlarr:1"
	category:    "media"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 9696, host: 9696, protocol: "tcp", name: "web"},
	]

	volumes: [
		{name: "prowlarr-config", path: "/config", type: "volume"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`prowlarr.{{.domain}}`)"
	}
}

#BazarrService: {
	name:        "bazarr"
	displayName: "Bazarr"
	image:       "linuxserver/bazarr:1"
	category:    "media"

	placement: {
		nodeType: "local"
		strategy: "single"
	}

	ports: [
		{container: 6767, host: 6767, protocol: "tcp", name: "web"},
	]

	volumes: [
		{name: "bazarr-config", path: "/config", type: "volume"},
		{host: "/data/media/movies", path: "/movies", type: "bind"},
		{host: "/data/media/tv", path: "/tv", type: "bind"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`bazarr.{{.domain}}`)"
	}
}

#Outputs: {
	jellyfinUrl: string | *"https://media.{{.domain}}"
	sonarrUrl:   string | *"https://sonarr.{{.domain}}"
	radarrUrl:   string | *"https://radarr.{{.domain}}"
	prowlarrUrl: string | *"https://prowlarr.{{.domain}}"
}
