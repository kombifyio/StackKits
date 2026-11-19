package usecasecatalog

type ReleaseIdentity struct {
	Tag             string `json:"tag"`
	Version         string `json:"version"`
	SourceSHA       string `json:"sourceSha"`
	PublicSourceSHA string `json:"publicSourceSha"`
	ReleaseURL      string `json:"releaseUrl"`
}

type CatalogContent struct {
	Authoring struct {
		RetainedRemovalCandidates bool `json:"retainedRemovalCandidates"`
	} `json:"authoring"`
	UseCases []UseCase           `json:"useCases"`
	KitCores []AuthoringWorkload `json:"kitCores,omitempty"`
}

type UseCaseManifest struct {
	SchemaVersion    string          `json:"schemaVersion"`
	Release          ReleaseIdentity `json:"release"`
	GeneratedAt      string          `json:"generatedAt"`
	GeneratorVersion string          `json:"generatorVersion"`
	Catalog          CatalogContent  `json:"catalog"`
	ContentDigest    string          `json:"contentDigest"`
}

type OSCompatibility struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
	EvidenceRef  string `json:"evidenceRef,omitempty"`
}

type DeliveryCapabilities struct {
	Deployment     bool `json:"deployment"`
	RouteTLS       bool `json:"routeTLS"`
	StatusEvidence bool `json:"statusEvidence"`
	BackupRestore  bool `json:"backupRestore"`
}

type ApplicationDelivery struct {
	// Native authoring binds the catalog's default implementation to the
	// module whose explicit profiles must accompany a selected workload.
	DefaultAlternativeRef string               `json:"defaultAlternativeRef,omitempty"`
	DefaultModuleRef      string               `json:"defaultModuleRef,omitempty"`
	UseCaseRef            string               `json:"useCaseRef"`
	WorkloadRef           string               `json:"workloadRef"`
	AdapterRef            string               `json:"adapterRef"`
	AdapterName           string               `json:"adapterName"`
	Status                string               `json:"status"`
	Capabilities          DeliveryCapabilities `json:"capabilities"`
}

type CompatibilityManifest struct {
	SchemaVersion    string          `json:"schemaVersion"`
	Release          ReleaseIdentity `json:"release"`
	GeneratedAt      string          `json:"generatedAt"`
	GeneratorVersion string          `json:"generatorVersion"`
	Compatibility    struct {
		OS                  []OSCompatibility     `json:"os"`
		ApplicationDelivery []ApplicationDelivery `json:"applicationDelivery"`
	} `json:"compatibility"`
	ContentDigest string `json:"contentDigest"`
}

type Gate struct {
	ID         string   `json:"id"`
	Status     string   `json:"status"`
	ReasonCode string   `json:"reasonCode"`
	Detail     string   `json:"detail"`
	Sources    []string `json:"sources"`
}

type InternalUseCase struct {
	UseCase
	Gates []Gate `json:"gates"`
}

type TestReceipt struct {
	SchemaVersion string   `json:"schemaVersion"`
	SourceSHA     string   `json:"sourceSha"`
	Status        string   `json:"status"`
	UseCaseRefs   []string `json:"useCaseRefs"`
}

type EvidenceManifest struct {
	SchemaVersion    string            `json:"schemaVersion"`
	SourceSHA        string            `json:"sourceSha"`
	GeneratedAt      string            `json:"generatedAt"`
	GeneratorVersion string            `json:"generatorVersion"`
	UseCases         []InternalUseCase `json:"useCases"`
	ContentDigest    string            `json:"contentDigest"`
}

type RuntimeEvidenceReceipt struct {
	SchemaVersion string `json:"schemaVersion"`
	SourceSHA     string `json:"sourceSha"`
	UseCases      []struct {
		UseCaseRef  string `json:"useCaseRef"`
		Status      string `json:"status"`
		EvidenceRef string `json:"evidenceRef"`
	} `json:"useCases"`
}
