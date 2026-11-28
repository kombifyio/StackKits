package agentsurface

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	cueapi "cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
)

func LoadPackageSurfaces(repoRoot string) (map[string]Surface, error) {
	dir := filepath.Join(repoRoot, "use-cases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read use-cases: %w", err)
	}
	surfaces := map[string]Surface{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pkgDir := filepath.ToSlash(filepath.Join("use-cases", entry.Name()))
		var pkg struct {
			Metadata struct {
				UseCaseRef string `json:"useCaseRef"`
			} `json:"metadata"`
			AgentSurface *Surface `json:"agentSurface"`
		}
		if err := loadCUE(repoRoot, pkgDir, "Package", &pkg); err != nil {
			return nil, err
		}
		if pkg.AgentSurface == nil {
			continue
		}
		ref := pkg.Metadata.UseCaseRef
		if ref == "" {
			return nil, fmt.Errorf("package %s omits metadata.useCaseRef", entry.Name())
		}
		pkg.AgentSurface.Ref = ref
		surfaces[ref] = *pkg.AgentSurface
	}
	return surfaces, nil
}

func loadCUE(root, directory, expression string, target any) error {
	instances := load.Instances([]string{"./" + directory}, &load.Config{Dir: root, ModuleRoot: root})
	if len(instances) != 1 || instances[0].Err != nil {
		if len(instances) == 1 {
			return fmt.Errorf("load %s.%s: %w", directory, expression, instances[0].Err)
		}
		return fmt.Errorf("load %s.%s: got %d instances", directory, expression, len(instances))
	}
	value := cuecontext.New().BuildInstance(instances[0]).LookupPath(cueapi.ParsePath(expression))
	if err := value.Validate(cueapi.Concrete(true)); err != nil {
		return fmt.Errorf("%s.%s is not concrete: %w", directory, expression, err)
	}
	data, err := value.MarshalJSON()
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s.%s: %w", directory, expression, err)
	}
	return nil
}
