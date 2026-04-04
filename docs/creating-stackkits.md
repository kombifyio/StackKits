# Creating a StackKit

> **Level:** Intermediate  
> **Time:** 30-60 minutes

This guide walks you through creating a custom StackKit from scratch. By the end, you'll have a fully functional infrastructure blueprint that can be deployed via CLI or kombify-TechStack.

## Prerequisites

- CUE installed (`cue version` >= 0.9)
- OpenTofu installed (`tofu version` >= 1.6)
- Docker installed and running
- Basic understanding of YAML, HCL, and CUE syntax

## StackKit Structure

Every StackKit follows this standard structure:

```
my-stackkit/
├── stackkit.yaml       # Metadata and requirements
├── stackfile.cue       # Main CUE schema
├── services.cue        # Service definitions
├── defaults.cue        # Smart defaults
├── variants/
│   ├── os/             # OS-specific configs
│   │   └── ubuntu-24.cue
│   └── compute/        # Resource tiers
│       └── compute.cue
├── templates/
│   ├── simple/         # Single-server template
│   │   └── main.tf
│   └── advanced/       # Multi-node with Terramate
│       └── terramate.tm.hcl
└── tests/
    └── validation_test.cue
```

## Step 1: Create the Metadata File

Start with `stackkit.yaml` to define your StackKit's identity:

```yaml
# stackkit.yaml
apiVersion: stackkit/v1
kind: StackKit
metadata:
  name: my-media-server
  version: "1.0.0"
  displayName: "Media Server Stack"
  description: "Self-hosted media server with Jellyfin, Sonarr, and Radarr"
  author: "Your Name"
  license: "MIT"
  homepage: "https://github.com/your-org/stackkits"
  tags:
    - media
    - homelab
    - streaming
    - plex-alternative

# Minimum requirements
requirements:
  os:
    - ubuntu-24
    - debian-12
  resources:
    cpu: 2
    memory: 4096  # MB
    storage: 100  # GB
  network:
    ports:
      - 80
      - 443
      - 8096  # Jellyfin

# Available deployment modes
modes:
  simple:
    description: "Single OpenTofu file for standalone deployment"
    default: true
  advanced:
    description: "Terramate-orchestrated for complex setups"

# Service variants
variants:
  default:
    description: "Jellyfin + Sonarr + Radarr"
    services:
      - jellyfin
      - sonarr
      - radarr
  minimal:
    description: "Jellyfin only"
    services:
      - jellyfin
  complete:
    description: "Full media stack with Plex alternative"
    services:
      - jellyfin
      - sonarr
      - radarr
      - prowlarr
      - overseerr
```

## Step 2: Define the Main Schema

Create `stackfile.cue` to define your StackKit's structure:

```cue
// stackfile.cue
package my_media_server

import "github.com/kombifyio/stackkits/base"

// #MediaServerKit extends the base StackKit
#MediaServerKit: base.#BaseStackKit & {
    // Override metadata
    metadata: {
        name:        "my-media-server"
        displayName: "Media Server Stack"
        version:     "1.0.0"
        description: "Self-hosted media server with Jellyfin, Sonarr, and Radarr"
        author:      "Your Name"
        license:     "MIT"
        tags:        ["media", "homelab", "streaming"]
    }

    // Variant selection
    variant: "default" | "minimal" | "complete" | *"default"

    // System defaults for media server
    system: {
        timezone: string | *"UTC"
        locale:   "en_US.UTF-8"
    }

    // Extended packages
    packages: base.#BasePackages & {
        extra: [
            "ffmpeg",          // Video transcoding
            "intel-media-va",  // Hardware acceleration
            "jq",
            "htop",
        ]
    }

    // Docker configuration
    container: base.#ContainerRuntime & {
        engine:      "docker"
        rootless:    false
        liveRestore: true
    }

    // Security (inherit from base with customizations)
    security: {
        ssh:       base.#SSHHardening
        firewall:  _mediaServerFirewall
        container: base.#ContainerSecurityContext
        secrets:   base.#SecretsPolicy & {backend: "file"}
        tls:       base.#TLSPolicy & {certSource: "acme"}
    }

    // Media-specific storage configuration
    storage: #MediaStorage
}

// Custom firewall rules for media server
_mediaServerFirewall: base.#FirewallPolicy & {
    enabled:         true
    backend:         "ufw"
    defaultInbound:  "deny"
    defaultOutbound: "allow"
    rules: [
        {port: 22, protocol: "tcp", comment:   "SSH"},
        {port: 80, protocol: "tcp", comment:   "HTTP"},
        {port: 443, protocol: "tcp", comment:  "HTTPS"},
        {port: 8096, protocol: "tcp", comment: "Jellyfin"},
        {port: 7878, protocol: "tcp", comment: "Radarr"},
        {port: 8989, protocol: "tcp", comment: "Sonarr"},
    ]
}

// Media storage configuration
#MediaStorage: {
    // Root path for all media
    root: string | *"/srv/media"
    
    // Subdirectories
    paths: {
        movies:   "\(root)/movies"
        tvshows:  "\(root)/tvshows"
        music:    "\(root)/music"
        downloads: "\(root)/downloads"
        config:   "\(root)/config"
    }
    
    // Storage permissions
    permissions: {
        user:  "media"
        group: "media"
        mode:  "0775"
    }
}
```

## Step 3: Define Services

Create `services.cue` with your service definitions:

```cue
// services.cue
package my_media_server

import "github.com/kombifyio/stackkits/base"

// =============================================================================
// CORE SERVICES
// =============================================================================

#JellyfinService: base.#ServiceDefinition & {
    name:        "jellyfin"
    displayName: "Jellyfin"
    type:        "media-server"
    required:    true
    image:       "jellyfin/jellyfin"
    tag:         "latest"
    description: "Free software media system"

    network: {
        ports: [
            {host: 8096, container: 8096, protocol: "tcp", description: "Web UI"},
        ]
        traefik: {
            enabled: true
            rule:    "Host(`jellyfin.{{.domain}}`)"
            tls:     true
            port:    8096
        }
    }

    volumes: [
        {
            source:      "jellyfin-config"
            target:      "/config"
            type:        "volume"
            backup:      true
            description: "Jellyfin configuration"
        },
        {
            source:      "{{.storage.paths.movies}}"
            target:      "/data/movies"
            type:        "bind"
            readOnly:    true
            description: "Movies library"
        },
        {
            source:      "{{.storage.paths.tvshows}}"
            target:      "/data/tvshows"
            type:        "bind"
            readOnly:    true
            description: "TV Shows library"
        },
    ]

    // GPU passthrough for hardware transcoding
    devices: [
        "/dev/dri:/dev/dri",  // Intel/AMD GPU
    ]

    environment: {
        "JELLYFIN_PublishedServerUrl": "https://jellyfin.{{.domain}}"
    }

    healthCheck: {
        enabled: true
        http: {
            path:   "/health"
            port:   8096
            scheme: "http"
        }
        interval:    "30s"
        timeout:     "10s"
        retries:     3
        startPeriod: "30s"
    }

    resources: {
        memory:    "1g"
        memoryMax: "4g"
        cpus:      2.0
    }

    output: {
        url:         "https://jellyfin.{{.domain}}"
        description: "Jellyfin Media Server"
        credentials: {
            note: "Create admin account on first login"
        }
    }
}

// =============================================================================
// AUTOMATION SERVICES
// =============================================================================

#SonarrService: base.#ServiceDefinition & {
    name:        "sonarr"
    displayName: "Sonarr"
    type:        "automation"
    required:    false
    enabled:     true
    image:       "lscr.io/linuxserver/sonarr"
    tag:         "latest"
    description: "TV show automation and management"
    needs:       ["jellyfin"]

    network: {
        ports: [
            {host: 8989, container: 8989, protocol: "tcp", description: "Web UI"},
        ]
        traefik: {
            enabled: true
            rule:    "Host(`sonarr.{{.domain}}`)"
            tls:     true
            port:    8989
        }
    }

    volumes: [
        {
            source:      "sonarr-config"
            target:      "/config"
            type:        "volume"
            backup:      true
            description: "Sonarr configuration"
        },
        {
            source:      "{{.storage.paths.tvshows}}"
            target:      "/tv"
            type:        "bind"
            description: "TV Shows directory"
        },
        {
            source:      "{{.storage.paths.downloads}}"
            target:      "/downloads"
            type:        "bind"
            description: "Downloads directory"
        },
    ]

    environment: {
        "PUID": "1000"
        "PGID": "1000"
        "TZ":   "{{.system.timezone}}"
    }

    healthCheck: {
        enabled: true
        http: {
            path:   "/ping"
            port:   8989
            scheme: "http"
        }
        interval:    "30s"
        timeout:     "10s"
        retries:     3
        startPeriod: "30s"
    }

    output: {
        url:         "https://sonarr.{{.domain}}"
        description: "Sonarr - TV Show Management"
    }
}

#RadarrService: base.#ServiceDefinition & {
    name:        "radarr"
    displayName: "Radarr"
    type:        "automation"
    required:    false
    enabled:     true
    image:       "lscr.io/linuxserver/radarr"
    tag:         "latest"
    description: "Movie automation and management"
    needs:       ["jellyfin"]

    network: {
        ports: [
            {host: 7878, container: 7878, protocol: "tcp", description: "Web UI"},
        ]
        traefik: {
            enabled: true
            rule:    "Host(`radarr.{{.domain}}`)"
            tls:     true
            port:    7878
        }
    }

    volumes: [
        {
            source:      "radarr-config"
            target:      "/config"
            type:        "volume"
            backup:      true
            description: "Radarr configuration"
        },
        {
            source:      "{{.storage.paths.movies}}"
            target:      "/movies"
            type:        "bind"
            description: "Movies directory"
        },
        {
            source:      "{{.storage.paths.downloads}}"
            target:      "/downloads"
            type:        "bind"
            description: "Downloads directory"
        },
    ]

    environment: {
        "PUID": "1000"
        "PGID": "1000"
        "TZ":   "{{.system.timezone}}"
    }

    healthCheck: {
        enabled: true
        http: {
            path:   "/ping"
            port:   7878
            scheme: "http"
        }
        interval:    "30s"
        timeout:     "10s"
        retries:     3
        startPeriod: "30s"
    }

    output: {
        url:         "https://radarr.{{.domain}}"
        description: "Radarr - Movie Management"
    }
}
```

## Step 4: Create Default Values

Create `defaults.cue` for smart defaults:

```cue
// defaults.cue
package my_media_server

// Default configuration that can be overridden
defaults: {
    // Network defaults
    network: {
        domain: string | *"local"
        subnet: "172.30.0.0/16"
    }

    // Storage defaults
    storage: {
        root: "/srv/media"
    }

    // Resource defaults by tier
    resources: {
        high: {
            jellyfin: {memory: "4g", cpus: 4.0}
            sonarr:   {memory: "512m", cpus: 1.0}
            radarr:   {memory: "512m", cpus: 1.0}
        }
        standard: {
            jellyfin: {memory: "2g", cpus: 2.0}
            sonarr:   {memory: "256m", cpus: 0.5}
            radarr:   {memory: "256m", cpus: 0.5}
        }
        low: {
            jellyfin: {memory: "1g", cpus: 1.0}
            sonarr:   {memory: "128m", cpus: 0.25}
            radarr:   {memory: "128m", cpus: 0.25}
        }
    }
}
```

## Step 5: Add OS Variants

Create `variants/os/ubuntu-24.cue`:

```cue
// variants/os/ubuntu-24.cue
package my_media_server

#Ubuntu24Variant: #OSVariant & {
    os: {
        family:       "debian"
        distribution: "ubuntu"
        version:      "24.04"
        codename:     "noble"
    }

    packages: {
        manager: "apt"
        
        // Media-specific packages
        media: [
            "ffmpeg",
            "intel-media-va-driver",  // Intel Quick Sync
            "va-driver-all",
            "intel-gpu-tools",
        ]

        // Docker installation
        docker: {
            repo:    "https://download.docker.com/linux/ubuntu"
            keyUrl:  "https://download.docker.com/linux/ubuntu/gpg"
            packages: [
                "docker-ce",
                "docker-ce-cli",
                "containerd.io",
                "docker-compose-plugin",
            ]
        }
    }

    // GPU detection commands
    gpu: {
        detect:      "lspci | grep -i vga"
        intelCheck:  "ls /dev/dri/render* 2>/dev/null"
        permissions: "usermod -aG render,video ${USER}"
    }
}
```

## Step 6: Create OpenTofu Template

Create `templates/simple/main.tf`:

```hcl
# templates/simple/main.tf
# Media Server Stack - OpenTofu Configuration
# StackKit: my-media-server v1.0.0

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "~> 3.0"
    }
  }
}

# =============================================================================
# VARIABLES
# =============================================================================

variable "domain" {
  description = "Primary domain for the media server"
  type        = string
}

variable "storage_root" {
  description = "Root directory for media storage"
  type        = string
  default     = "/srv/media"
}

variable "timezone" {
  description = "Server timezone"
  type        = string
  default     = "UTC"
}

variable "enable_sonarr" {
  description = "Enable Sonarr for TV show management"
  type        = bool
  default     = true
}

variable "enable_radarr" {
  description = "Enable Radarr for movie management"
  type        = bool
  default     = true
}

# =============================================================================
# DOCKER NETWORK
# =============================================================================

resource "docker_network" "media" {
  name   = "media-network"
  driver = "bridge"

  ipam_config {
    subnet = "172.30.0.0/16"
  }
}

# =============================================================================
# JELLYFIN
# =============================================================================

resource "docker_image" "jellyfin" {
  name         = "jellyfin/jellyfin:latest"
  keep_locally = true
}

resource "docker_volume" "jellyfin_config" {
  name = "jellyfin-config"
}

resource "docker_container" "jellyfin" {
  name  = "jellyfin"
  image = docker_image.jellyfin.image_id

  restart = "unless-stopped"

  networks_advanced {
    name = docker_network.media.name
  }

  ports {
    internal = 8096
    external = 8096
  }

  volumes {
    volume_name    = docker_volume.jellyfin_config.name
    container_path = "/config"
  }

  volumes {
    host_path      = "${var.storage_root}/movies"
    container_path = "/data/movies"
    read_only      = true
  }

  volumes {
    host_path      = "${var.storage_root}/tvshows"
    container_path = "/data/tvshows"
    read_only      = true
  }

  # GPU passthrough for hardware transcoding
  devices {
    host_path      = "/dev/dri"
    container_path = "/dev/dri"
  }

  env = [
    "JELLYFIN_PublishedServerUrl=https://jellyfin.${var.domain}"
  ]

  labels {
    label = "managed-by"
    value = "stackkit"
  }

  labels {
    label = "stackkit"
    value = "my-media-server"
  }

  healthcheck {
    test         = ["CMD", "curl", "-f", "http://localhost:8096/health"]
    interval     = "30s"
    timeout      = "10s"
    retries      = 3
    start_period = "30s"
  }
}

# =============================================================================
# SONARR (Optional)
# =============================================================================

resource "docker_image" "sonarr" {
  count        = var.enable_sonarr ? 1 : 0
  name         = "lscr.io/linuxserver/sonarr:latest"
  keep_locally = true
}

resource "docker_volume" "sonarr_config" {
  count = var.enable_sonarr ? 1 : 0
  name  = "sonarr-config"
}

resource "docker_container" "sonarr" {
  count = var.enable_sonarr ? 1 : 0
  name  = "sonarr"
  image = docker_image.sonarr[0].image_id

  restart = "unless-stopped"

  networks_advanced {
    name = docker_network.media.name
  }

  ports {
    internal = 8989
    external = 8989
  }

  volumes {
    volume_name    = docker_volume.sonarr_config[0].name
    container_path = "/config"
  }

  volumes {
    host_path      = "${var.storage_root}/tvshows"
    container_path = "/tv"
  }

  volumes {
    host_path      = "${var.storage_root}/downloads"
    container_path = "/downloads"
  }

  env = [
    "PUID=1000",
    "PGID=1000",
    "TZ=${var.timezone}"
  ]

  labels {
    label = "managed-by"
    value = "stackkit"
  }

  depends_on = [docker_container.jellyfin]
}

# =============================================================================
# RADARR (Optional)
# =============================================================================

resource "docker_image" "radarr" {
  count        = var.enable_radarr ? 1 : 0
  name         = "lscr.io/linuxserver/radarr:latest"
  keep_locally = true
}

resource "docker_volume" "radarr_config" {
  count = var.enable_radarr ? 1 : 0
  name  = "radarr-config"
}

resource "docker_container" "radarr" {
  count = var.enable_radarr ? 1 : 0
  name  = "radarr"
  image = docker_image.radarr[0].image_id

  restart = "unless-stopped"

  networks_advanced {
    name = docker_network.media.name
  }

  ports {
    internal = 7878
    external = 7878
  }

  volumes {
    volume_name    = docker_volume.radarr_config[0].name
    container_path = "/config"
  }

  volumes {
    host_path      = "${var.storage_root}/movies"
    container_path = "/movies"
  }

  volumes {
    host_path      = "${var.storage_root}/downloads"
    container_path = "/downloads"
  }

  env = [
    "PUID=1000",
    "PGID=1000",
    "TZ=${var.timezone}"
  ]

  labels {
    label = "managed-by"
    value = "stackkit"
  }

  depends_on = [docker_container.jellyfin]
}

# =============================================================================
# OUTPUTS
# =============================================================================

output "jellyfin_url" {
  value       = "http://localhost:8096"
  description = "Jellyfin local URL"
}

output "sonarr_url" {
  value       = var.enable_sonarr ? "http://localhost:8989" : "disabled"
  description = "Sonarr local URL"
}

output "radarr_url" {
  value       = var.enable_radarr ? "http://localhost:7878" : "disabled"
  description = "Radarr local URL"
}
```

## Step 7: Add Validation Tests

Create `tests/validation_test.cue`:

```cue
// tests/validation_test.cue
package my_media_server

// Test: Valid minimal configuration
_test_minimal: #MediaServerKit & {
    variant: "minimal"
    system: timezone: "Europe/Berlin"
    network: defaults: domain: "home.local"
}

// Test: Valid complete configuration  
_test_complete: #MediaServerKit & {
    variant: "complete"
    system: timezone: "America/New_York"
    network: defaults: domain: "media.example.com"
    storage: root: "/mnt/storage/media"
}

// Test: Services resolve correctly
_test_services: {
    jellyfin: #JellyfinService
    sonarr:   #SonarrService
    radarr:   #RadarrService
}
```

## Step 8: Validate and Deploy

### Validate the StackKit

```bash
# Validate CUE schemas
cd my-media-server
cue vet ./...

# Check for errors
cue eval ./... --strict
```

### Deploy with Simple Mode

```bash
# Create terraform.tfvars
cat > templates/simple/terraform.tfvars << EOF
domain       = "home.local"
storage_root = "/srv/media"
timezone     = "Europe/Berlin"
enable_sonarr = true
enable_radarr = true
EOF

# Initialize and apply
cd templates/simple
tofu init
tofu plan
tofu apply
```

## Best Practices

### 1. Schema Design

- **Use Required Fields:** Mark critical fields with `!` (e.g., `name!: string`)
- **Provide Defaults:** Use `| *"default"` for optional fields
- **Add Constraints:** Use regex and value bounds (e.g., `=~"^[a-z]+"`)

### 2. Service Definitions

- **Health Checks:** Always define health checks for production services
- **Dependencies:** Use `needs: [...]` to declare service dependencies
- **Resource Limits:** Define memory/CPU limits to prevent runaway containers

### 3. Templates

- **Idempotency:** Ensure templates can be applied multiple times
- **State Safety:** Never modify state outside of OpenTofu
- **Labels:** Always label resources with `managed-by` and `stackkit`

### 4. Testing

- **Validate Early:** Run `cue vet` before any deployment
- **Test Variants:** Test all variant combinations
- **Document Requirements:** Keep `stackkit.yaml` requirements up to date

## Next Steps

- [Variant System](variants.md) - Deep dive into OS and compute variants
- [Template Reference](templates.md) - OpenTofu template patterns
- [Publishing StackKits](publishing.md) - Share your StackKit with others
