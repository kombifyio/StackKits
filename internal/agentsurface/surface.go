package agentsurface

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/kombifyio/stackkits/internal/architecturev2"
)

//go:embed surfaces.json
var embeddedCatalog []byte

const (
	SchemaVersion    = "stackkit.agent-surface/v1"
	WorkspaceRelPath = ".stackkit/agent-surface.json"
)

type LifecycleMCP struct {
	Ref                  string `json:"ref"`
	Owner                string `json:"owner"`
	Endpoint             string `json:"endpoint"`
	Transport            string `json:"transport"`
	Auth                 string `json:"auth"`
	GenerateClientConfig bool   `json:"generateClientConfig"`
}

type ProductMCP struct {
	ID                   string `json:"id"`
	Owner                string `json:"owner"`
	Endpoint             string `json:"endpoint"`
	Transport            string `json:"transport"`
	Auth                 string `json:"auth"`
	GenerateClientConfig bool   `json:"generateClientConfig"`
	Reason               string `json:"reason,omitempty"`
}

type API struct {
	ID       string `json:"id"`
	Protocol string `json:"protocol"`
	Purpose  string `json:"purpose"`
	Auth     string `json:"auth"`
}

type Skill struct {
	ID       string `json:"id"`
	Audience string `json:"audience"`
	Source   string `json:"source"`
	Path     string `json:"path"`
}

type CLIHelper struct {
	Command string `json:"command"`
	Purpose string `json:"purpose"`
}

type ConfigBaseline struct {
	Status         string `json:"status"`
	Reason         string `json:"reason,omitempty"`
	ModuleInputRef string `json:"moduleInputRef,omitempty"`
}

type Surface struct {
	Ref            string         `json:"ref"`
	EquipPolicy    string         `json:"equipPolicy"`
	LifecycleMCP   LifecycleMCP   `json:"lifecycleMcp"`
	ProductMCPs    []ProductMCP   `json:"productMcps"`
	APIs           []API          `json:"apis"`
	Skills         []Skill        `json:"skills"`
	CLIHelpers     []CLIHelper    `json:"cliHelpers"`
	ConfigBaseline ConfigBaseline `json:"configBaseline"`
}

type Document struct {
	SchemaVersion        string    `json:"schemaVersion"`
	ContainsSecretValues bool      `json:"containsSecretValues"`
	UseCases             []Surface `json:"useCases"`
}

type catalogFile struct {
	SchemaVersion string             `json:"schemaVersion"`
	Surfaces      map[string]Surface `json:"surfaces"`
}

func LoadEmbeddedCatalog() (map[string]Surface, error) {
	var file catalogFile
	if err := json.Unmarshal(embeddedCatalog, &file); err != nil {
		return nil, fmt.Errorf("decode embedded agent-surface catalog: %w", err)
	}
	if file.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("embedded agent-surface catalog schema %q, want %s", file.SchemaVersion, SchemaVersion)
	}
	if file.Surfaces == nil {
		file.Surfaces = map[string]Surface{}
	}
	return file.Surfaces, nil
}

func Project(selected []string, catalog map[string]Surface) Document {
	doc := Document{
		SchemaVersion:        SchemaVersion,
		ContainsSecretValues: false,
		UseCases:             []Surface{},
	}
	seen := map[string]struct{}{}
	var refs []string
	for _, ref := range selected {
		if ref == "" {
			continue
		}
		if _, dup := seen[ref]; dup {
			continue
		}
		seen[ref] = struct{}{}
		if _, ok := catalog[ref]; ok {
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs)
	for _, ref := range refs {
		surface := catalog[ref]
		surface.Ref = ref
		if surface.ProductMCPs == nil {
			surface.ProductMCPs = []ProductMCP{}
		}
		if surface.APIs == nil {
			surface.APIs = []API{}
		}
		if surface.Skills == nil {
			surface.Skills = []Skill{}
		}
		if surface.CLIHelpers == nil {
			surface.CLIHelpers = []CLIHelper{}
		}
		doc.UseCases = append(doc.UseCases, surface)
	}
	return doc
}

func Encode(doc Document) ([]byte, error) {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func WriteWorkspace(root string, selected []string) error {
	catalog, err := LoadEmbeddedCatalog()
	if err != nil {
		return err
	}
	data, err := Encode(Project(selected, catalog))
	if err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(WorkspaceRelPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create agent-surface directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", WorkspaceRelPath, err)
	}
	return nil
}

func WriteWorkspaceFromSpec(root, specPath string) error {
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("read stack spec for agent-surface: %w", err)
	}
	spec, err := architecturev2.DecodeYAMLObject(raw, "StackSpec")
	if err != nil {
		return fmt.Errorf("decode stack spec for agent-surface: %w", err)
	}
	workloads, _ := spec["workloads"].(map[string]any)
	selected := make([]string, 0, len(workloads))
	for ref := range workloads {
		selected = append(selected, ref)
	}
	if err := WriteWorkspace(root, selected); err != nil {
		return err
	}
	catalog, err := LoadEmbeddedCatalog()
	if err != nil {
		return err
	}
	return WriteProductMCPClients(root, spec, Project(selected, catalog))
}

func CatalogBytes(surfaces map[string]Surface) ([]byte, error) {
	file := catalogFile{SchemaVersion: SchemaVersion, Surfaces: surfaces}
	if file.Surfaces == nil {
		file.Surfaces = map[string]Surface{}
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(bytes.TrimRight(data, "\n"), '\n'), nil
}
