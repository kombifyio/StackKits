// Package base — CUE constraint tests for iac-defaults.cue.
// Run via: cue vet ./base/...
package base

_test_iac_defaults_minimal: #IaCDefaults & {
	provider_versions: {
		docker: "~> 3.0"
		local:  "~> 2.5"
		random: "~> 3.6"
		null:   "~> 3.2"
	}
	default_tags: {
		kit_slug:    "base-kit"
		kit_version: "1.0.0"
	}
	backend: type: "local"
}

_test_iac_defaults_with_tenant: #IaCDefaults & {
	provider_versions: {
		docker:  "~> 3.0"
		local:   "~> 2.5"
		random:  "~> 3.6"
		null:    "~> 3.2"
		hetzner: "~> 1.45"
	}
	default_tags: {
		kit_slug:    "base-kit"
		kit_version: "1.1.0"
		tenant_id:   "acme-corp"
		environment: "prod"
	}
	backend: {
		type: "s3"
		s3: {
			bucket: "kombify-tofu-state"
			key:    "tenants/acme-corp/base-kit.tfstate"
			region: "eu-central-1"
		}
	}
}

_test_iac_defaults_remote_backend: #IaCDefaults & {
	provider_versions: {
		docker: "~> 3.0"
		local:  "~> 2.5"
		random: "~> 3.6"
		null:   "~> 3.2"
	}
	default_tags: {
		kit_slug:    "base-kit"
		kit_version: "1.0.0"
	}
	backend: {
		type: "remote"
		remote: {
			organization: "kombify"
			workspace:    "base-kit-prod"
		}
	}
}
