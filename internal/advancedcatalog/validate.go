package advancedcatalog

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"

	"github.com/google/jsonschema-go/jsonschema"
)

// ValidateJSON validates a JSON document against a schema file under
// schemasDir. Relative $refs resolve to sibling files in schemasDir, so the
// contract schemas are checked exactly as they ship.
func ValidateJSON(schemasDir, schemaFile string, document []byte) error {
	schema, err := loadSchema(filepath.Join(schemasDir, filepath.Base(schemaFile)))
	if err != nil {
		return err
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{
		Loader: func(uri *url.URL) (*jsonschema.Schema, error) {
			return loadSchema(filepath.Join(schemasDir, path.Base(uri.Path)))
		},
	})
	if err != nil {
		return fmt.Errorf("resolve %s: %w", schemaFile, err)
	}
	var instance any
	if err := json.Unmarshal(document, &instance); err != nil {
		return fmt.Errorf("decode document: %w", err)
	}
	return resolved.Validate(instance)
}

func loadSchema(file string) (*jsonschema.Schema, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("decode schema %s: %w", filepath.Base(file), err)
	}
	return &schema, nil
}
