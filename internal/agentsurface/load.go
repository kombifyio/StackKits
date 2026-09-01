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
		var pkg map[string]any
		if err := loadCUE(repoRoot, pkgDir, "Package", &pkg); err != nil {
			return nil, err
		}
		raw, ok := pkg["agentSurface"]
		if !ok {
			continue
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("encode %s agentSurface: %w", entry.Name(), err)
		}
		var surface Surface
		if err := json.Unmarshal(encoded, &surface); err != nil {
			return nil, fmt.Errorf("decode %s agentSurface: %w", entry.Name(), err)
		}
		ref := nestedString(pkg, "metadata", "useCaseRef")
		if ref == "" {
			return nil, fmt.Errorf("package %s omits metadata.useCaseRef", entry.Name())
		}
		surface.Ref = ref
		surfaces[ref] = surface
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

func nestedString(object map[string]any, keys ...string) string {
	current := any(object)
	for _, key := range keys {
		nested, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = nested[key]
	}
	value, _ := current.(string)
	return value
}
