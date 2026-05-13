// Package modern_homelab - CUE Tests
//
// Run tests with: cue vet ./tests/

package modern_homelab

import (
	"github.com/kombifyio/stackkits/base"
)

// =============================================================================
// STACKKIT VALIDATION TESTS
// =============================================================================

// Test: ModernHomelabKit should extend BaseStackKit
_testModernHomelabKitExtendsBase: #ModernHomelabKit & {
	// This should compile if ModernHomelabKit properly extends BaseStackKit
	metadata: {
		name:        "modern-homelab"
		displayName: "Modern Homelab"
		version:     "1.0.0"
		description: "Test instance"
		license:     "Apache-2.0"
	}
	nodes: []
}

// Test: Default variant should be "default"
_testDefaultVariant: {
	kit: #ModernHomelabKit
	_assert: kit.variant == "default"
}

// Test: System swap should be disabled for Kubernetes
_testSwapDisabled: {
	kit: #ModernHomelabKit
	_assert: kit.system.swap == "disabled"
}

// =============================================================================
// K3S SERVICE TESTS
// =============================================================================

// Test: K3s service should be required
_testK3sRequired: {
	_assert: #K3sService.required == true
}

// Test: K3s service should have implemented status
_testK3sImplemented: {
	_assert: #K3sService.status == "implemented"
}

// Test: K3s default version
_testK3sDefaultVersion: {
	service: #K3sService
	_assert: service.config.cluster.version == "v1.30.2+k3s1"
}

// Test: K3s CNI default should be flannel
_testK3sCNIDefault: {
	service: #K3sService
	_assert: service.config.cni.plugin == "flannel"
}

// Test: K3s should disable traefik and local-storage by default
_testK3sDisabledComponents: {
	service: #K3sService
	_assert: service.config.server.disable[0] == "traefik"
	_assert2: service.config.server.disable[1] == "local-storage"
}

// =============================================================================
// GITOPS SERVICE TESTS
// =============================================================================

// Test: Flux service should depend on k3s
_testFluxDependsOnK3s: {
	_assert: #FluxService.needs[0] == "k3s"
}

// Test: ArgoCD service should depend on k3s
_testArgoCDDependsOnK3s: {
	_assert: #ArgoCDService.needs[0] == "k3s"
}

// Test: Flux default branch should be "main"
_testFluxDefaultBranch: {
	service: #FluxService
	_assert: service.config.repository.branch == "main"
}

// Test: ArgoCD default insecure mode
_testArgoCDInsecure: {
	service: #ArgoCDService
	_assert: service.config.server.insecure == true
}

// =============================================================================
// INGRESS SERVICE TESTS
// =============================================================================

// Test: Traefik ingress should be required
_testTraefikRequired: {
	_assert: #TraefikIngressService.required == true
}

// Test: Traefik depends on k3s
_testTraefikDependsOnK3s: {
	_assert: #TraefikIngressService.needs[0] == "k3s"
}

// Test: Traefik TLS should be enabled by default
_testTraefikTLSEnabled: {
	service: #TraefikIngressService
	_assert: service.config.tls.enabled == true
}

// Test: Traefik default resolver should be letsencrypt
_testTraefikDefaultResolver: {
	service: #TraefikIngressService
	_assert: service.config.tls.resolver == "letsencrypt"
}

// =============================================================================
// STORAGE SERVICE TESTS
// =============================================================================

// Test: Longhorn depends on k3s
_testLonghornDependsOnK3s: {
	_assert: #LonghornService.needs[0] == "k3s"
}

// Test: Longhorn default replicas should be 2
_testLonghornDefaultReplicas: {
	service: #LonghornService
	_assert: service.config.defaults.replicas == 2
}

// Test: Longhorn should be default StorageClass
_testLonghornDefaultClass: {
	service: #LonghornService
	_assert: service.config.defaults.defaultClass == true
}

// =============================================================================
// MONITORING SERVICE TESTS
// =============================================================================

// Test: Prometheus depends on k3s
_testPrometheusDependsOnK3s: {
	_assert: #PrometheusService.needs[0] == "k3s"
}

// Test: Prometheus default retention
_testPrometheusRetention: {
	service: #PrometheusService
	_assert: service.config.prometheus.retention == "15d"
}

// Test: Grafana should be enabled by default
_testGrafanaEnabled: {
	service: #PrometheusService
	_assert: service.config.grafana.enabled == true
}

// Test: Loki depends on k3s
_testLokiDependsOnK3s: {
	_assert: #LokiService.needs[0] == "k3s"
}

// =============================================================================
// SERVICE COLLECTION TESTS
// =============================================================================

// Test: Default services should include k3s
_testDefaultServicesIncludeK3s: {
	services: #DefaultServices
	_k3sFound: [ for s in services if s.name == "k3s" { s } ]
	_assert: len(_k3sFound) == 1
}

// Test: Default services should include flux
_testDefaultServicesIncludeFlux: {
	services: #DefaultServices
	_fluxFound: [ for s in services if s.name == "flux" { s } ]
	_assert: len(_fluxFound) == 1
}

// Test: Minimal services should NOT include loki
_testMinimalServicesNoLoki: {
	services: #MinimalServices
	_lokiFound: [ for s in services if s.name == "loki" { s } ]
	_assert: len(_lokiFound) == 0
}

// Test: ArgoCD services should include argocd
_testArgoCDServicesIncludeArgoCD: {
	services: #ArgoCDServices
	_argoFound: [ for s in services if s.name == "argocd" { s } ]
	_assert: len(_argoFound) == 1
}

// Test: ArgoCD services should NOT include flux
_testArgoCDServicesNoFlux: {
	services: #ArgoCDServices
	_fluxFound: [ for s in services if s.name == "flux" { s } ]
	_assert: len(_fluxFound) == 0
}

// =============================================================================
// SMART DEFAULTS TESTS
// =============================================================================

// Test: Production tier should have full monitoring
_testProductionTierMonitoring: {
	defaults: #SmartDefaults & {
		clusterTier: "production"
	}
	_assert: defaults.services.monitoring == "full"
}

// Test: Minimal tier should have minimal monitoring
_testMinimalTierMonitoring: {
	defaults: #SmartDefaults & {
		clusterTier: "minimal"
	}
	_assert: defaults.services.monitoring == "minimal"
}

// Test: Production tier Prometheus replicas
_testProductionPrometheusReplicas: {
	defaults: #SmartDefaults & {
		clusterTier: "production"
	}
	_assert: defaults.monitoring.prometheus.replicas == 2
}

// Test: Minimal tier Grafana disabled
_testMinimalGrafanaDisabled: {
	defaults: #SmartDefaults & {
		clusterTier: "minimal"
	}
	_assert: defaults.monitoring.grafana.replicas == 0
}

// =============================================================================
// CLUSTER TIER DETECTOR TESTS
// =============================================================================

// Test: High resources should be production tier
_testHighResourcesProduction: {
	detector: #ClusterTierDetector & {
		nodeCount: 3
		totalCpu:  24
		totalRam:  64
	}
	_assert: detector.tier == "production"
}

// Test: Low resources should be minimal tier
_testLowResourcesMinimal: {
	detector: #ClusterTierDetector & {
		nodeCount: 2
		totalCpu:  4
		totalRam:  8
	}
	_assert: detector.tier == "minimal"
}

// Test: Medium resources should be standard tier
_testMediumResourcesStandard: {
	detector: #ClusterTierDetector & {
		nodeCount: 2
		totalCpu:  8
		totalRam:  16
	}
	_assert: detector.tier == "standard"
}

// =============================================================================
// DOMAIN CONFIG TESTS
// =============================================================================

// Test: Domain URL generation
_testDomainURLGeneration: {
	config: #DomainConfig & {
		domain: "example.com"
	}
	_assert: config.urls.grafana == "https://grafana.example.com"
}

// Test: Custom subdomain
_testCustomSubdomain: {
	config: #DomainConfig & {
		domain: "homelab.local"
		subdomains: {
			grafana: "metrics"
		}
	}
	_assert: config.urls.grafana == "https://metrics.homelab.local"
}

// =============================================================================
// HELM RELEASE DEFAULTS TESTS
// =============================================================================

// Test: Traefik chart version
_testTraefikChartVersion: {
	defaults: #HelmReleaseDefaults
	_assert: defaults.charts["traefik"].version == "26.0.0"
}

// Test: Longhorn chart version
_testLonghornChartVersion: {
	defaults: #HelmReleaseDefaults
	_assert: defaults.charts["longhorn"].version == "1.6.1"
}
