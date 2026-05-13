# Base Kit - Advanced Mode mit Terramate
# Diese Struktur ermöglicht Change Detection und parallele Ausführung

terramate {
  required_version = ">= 0.4.0"

  config {
    git {
      default_remote = "origin"
      default_branch = "main"
    }

    run {
      env {
        # Terramate-Umgebungsvariablen
        TM_STACK_NAME = global.stack.name
        TM_STACK_PATH = terramate.stack.path.absolute
      }
    }
  }
}

globals {
  # Stack-Metadaten
  stack = {
    name        = "base-kit"
    version     = "1.0.0"
    description = "KombiStack Base Kit"
  }

  # Globale Tags für alle Ressourcen
  common_tags = {
    managed-by = "kombistack"
    stackkit   = global.stack.name
    version    = global.stack.version
  }

  # Default-Werte
  defaults = {
    compute_tier = "standard"
    stacks_dir   = "/opt/stacks"
  }

  # Service-Versionen (zentral verwaltet)
  versions = {
    traefik  = "v3.0"
    dockge   = "1"
    dozzle   = "latest"
    netdata  = "stable"
    glances  = "latest-alpine"
    portainer = "latest"
  }
}
