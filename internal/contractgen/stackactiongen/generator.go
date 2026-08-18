package stackactiongen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
)

const (
	GeneratorVersion = "stackactiongen/v1"

	contractSource       = "foundation/stack_action.cue"
	localGoOutput        = "internal/stackaction/wire_gen.go"
	openAPIOutput        = "api/openapi/stackkits-v1.yaml"
	websiteOpenAPIOutput = "website/public/api/openapi.v1.yaml"
	bundleOutput         = "contracts/stackaction/v1"
	bundleIRFile         = "contract.ir.json"
	bundleOpenAPIFile    = "openapi.yaml"
	bundleGoFile         = "stackaction_gen.go"
	bundleManifestFile   = "manifest.json"

	pathsBegin   = "  # BEGIN GENERATED: stackaction paths"
	pathsEnd     = "  # END GENERATED: stackaction paths"
	schemasBegin = "    # BEGIN GENERATED: stackaction schemas"
	schemasEnd   = "    # END GENERATED: stackaction schemas"
)

var forbiddenPublicWireFields = map[string]struct{}{
	"access_key_id":         {},
	"agent_token":           {},
	"channel_bootstrap":     {},
	"client_private_key":    {},
	"key_path":              {},
	"key_pem":               {},
	"komodo_onboarding_key": {},
	"password":              {},
	"private_key":           {},
	"repo_password":         {},
	"secret_access_key":     {},
	"secrets":               {},
	"token":                 {},
}

// Options controls deterministic StackAction generation.
type Options struct {
	RepoRoot             string
	ExternalGoOutput     string
	ExternalBundleOutput string
	Check                bool
}

type contractBundleIR struct {
	SchemaVersion    string         `json:"schemaVersion"`
	GeneratorVersion string         `json:"generatorVersion"`
	Source           sourceIdentity `json:"source"`
	Contract         generationSpec `json:"contract"`
}

type sourceIdentity struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type generationManifest struct {
	SchemaVersion    string            `json:"schemaVersion"`
	GeneratorVersion string            `json:"generatorVersion"`
	WireVersion      string            `json:"wireVersion"`
	Source           sourceIdentity    `json:"source"`
	Outputs          map[string]string `json:"outputs"`
}

type generationSpec struct {
	WireVersion        string                    `json:"wireVersion"`
	Target             string                    `json:"target"`
	PathPrefix         string                    `json:"pathPrefix"`
	ObservationVersion string                    `json:"observationVersion"`
	Enums              map[string]enumSpec       `json:"enums"`
	Paths              []pathSpec                `json:"paths"`
	Types              map[string]objectTypeSpec `json:"types"`
}

type enumSpec struct {
	Order  int         `json:"order"`
	GoName string      `json:"goName"`
	Values []enumValue `json:"values"`
}

type enumValue struct {
	GoConst string `json:"goConst"`
	Value   string `json:"value"`
	Backup  bool   `json:"backup,omitempty"`
}

type pathSpec struct {
	GoConst     string `json:"goConst"`
	Suffix      string `json:"suffix"`
	Action      string `json:"action"`
	OperationID string `json:"operationID"`
	Summary     string `json:"summary"`
}

type objectTypeSpec struct {
	Order         int         `json:"order"`
	GoName        string      `json:"goName"`
	OpenAPIName   string      `json:"openapiName"`
	Description   string      `json:"description"`
	AnyOfRequired [][]string  `json:"anyOfRequired,omitempty"`
	Fields        []fieldSpec `json:"fields"`
}

type fieldSpec struct {
	JSON     string           `json:"json"`
	GoName   string           `json:"goName"`
	GoType   string           `json:"goType"`
	Required bool             `json:"required"`
	OpenAPI  openAPIFieldSpec `json:"openapi"`
}

type openAPIFieldSpec struct {
	Kind                 string   `json:"kind"`
	Format               string   `json:"format,omitempty"`
	Pattern              string   `json:"pattern,omitempty"`
	Minimum              *float64 `json:"minimum,omitempty"`
	Maximum              *float64 `json:"maximum,omitempty"`
	Ref                  string   `json:"ref,omitempty"`
	Enum                 string   `json:"enum,omitempty"`
	ItemsKind            string   `json:"itemsKind,omitempty"`
	ItemsFormat          string   `json:"itemsFormat,omitempty"`
	ItemsRef             string   `json:"itemsRef,omitempty"`
	MinItems             *int     `json:"minItems,omitempty"`
	AdditionalProperties string   `json:"additionalProperties,omitempty"`
}

// Run renders or verifies all local projections and an optional external Go
// staging output. StackKits never imports that external module.
func Run(options Options) error {
	root, err := filepath.Abs(options.RepoRoot)
	if err != nil {
		return fmt.Errorf("resolve repo root: %w", err)
	}
	spec, digest, err := loadSpec(root)
	if err != nil {
		return err
	}
	goOutput, err := renderGo(spec, digest)
	if err != nil {
		return err
	}

	openAPIPath := filepath.Join(root, filepath.FromSlash(openAPIOutput))
	currentOpenAPI, err := os.ReadFile(openAPIPath)
	if err != nil {
		return fmt.Errorf("read OpenAPI: %w", err)
	}
	openAPI, err := renderOpenAPI(currentOpenAPI, spec)
	if err != nil {
		return err
	}

	bundle, err := renderBundle(spec, digest, goOutput)
	if err != nil {
		return err
	}

	outputs := []output{
		{path: filepath.Join(root, filepath.FromSlash(localGoOutput)), data: goOutput},
		{path: openAPIPath, data: openAPI},
		{path: filepath.Join(root, filepath.FromSlash(websiteOpenAPIOutput)), data: openAPI},
	}
	outputs = append(outputs, bundle.outputs(filepath.Join(root, filepath.FromSlash(bundleOutput)))...)
	if strings.TrimSpace(options.ExternalGoOutput) != "" {
		external, err := filepath.Abs(options.ExternalGoOutput)
		if err != nil {
			return fmt.Errorf("resolve external Go output: %w", err)
		}
		outputs = append(outputs, output{path: external, data: goOutput})
	}
	if strings.TrimSpace(options.ExternalBundleOutput) != "" {
		external, err := filepath.Abs(options.ExternalBundleOutput)
		if err != nil {
			return fmt.Errorf("resolve external bundle output: %w", err)
		}
		outputs = append(outputs, bundle.outputs(external)...)
	}
	for _, candidate := range outputs {
		if options.Check {
			if err := checkOutput(candidate); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(candidate.path), 0o755); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}
		if err := os.WriteFile(candidate.path, candidate.data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", candidate.path, err)
		}
	}
	return nil
}

type renderedBundle struct {
	ir       []byte
	openAPI  []byte
	goSource []byte
	manifest []byte
}

func (bundle renderedBundle) outputs(root string) []output {
	return []output{
		{path: filepath.Join(root, bundleIRFile), data: bundle.ir},
		{path: filepath.Join(root, bundleOpenAPIFile), data: bundle.openAPI},
		{path: filepath.Join(root, bundleGoFile), data: bundle.goSource},
		{path: filepath.Join(root, bundleManifestFile), data: bundle.manifest},
	}
}

func renderBundle(spec generationSpec, sourceDigest string, goSource []byte) (renderedBundle, error) {
	ir, err := json.MarshalIndent(contractBundleIR{
		SchemaVersion:    "stackkit.stackaction-contract-ir/v1",
		GeneratorVersion: GeneratorVersion,
		Source:           sourceIdentity{Path: contractSource, SHA256: sourceDigest},
		Contract:         spec,
	}, "", "  ")
	if err != nil {
		return renderedBundle{}, fmt.Errorf("render StackAction contract IR: %w", err)
	}
	ir = append(ir, '\n')
	standaloneOpenAPI := renderStandaloneOpenAPI(spec, sourceDigest)

	outputs := map[string][]byte{
		bundleIRFile:      ir,
		bundleOpenAPIFile: standaloneOpenAPI,
		bundleGoFile:      goSource,
	}
	digests := make(map[string]string, len(outputs))
	for name, data := range outputs {
		digests[name] = sha256Hex(data)
	}
	manifest, err := json.MarshalIndent(generationManifest{
		SchemaVersion:    "stackkit.stackaction-generation-manifest/v1",
		GeneratorVersion: GeneratorVersion,
		WireVersion:      spec.WireVersion,
		Source:           sourceIdentity{Path: contractSource, SHA256: sourceDigest},
		Outputs:          digests,
	}, "", "  ")
	if err != nil {
		return renderedBundle{}, fmt.Errorf("render StackAction generation manifest: %w", err)
	}
	manifest = append(manifest, '\n')
	return renderedBundle{ir: ir, openAPI: standaloneOpenAPI, goSource: goSource, manifest: manifest}, nil
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

type output struct {
	path string
	data []byte
}

func checkOutput(candidate output) error {
	actual, err := os.ReadFile(candidate.path)
	if err != nil {
		return fmt.Errorf("generated output missing %s: %w", candidate.path, err)
	}
	if !bytes.Equal(actual, candidate.data) {
		return fmt.Errorf("generated output is stale: %s", candidate.path)
	}
	return nil
}

func loadSpec(root string) (generationSpec, string, error) {
	source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(contractSource)))
	if err != nil {
		return generationSpec{}, "", fmt.Errorf("read StackAction CUE authority: %w", err)
	}
	digestBytes := sha256.Sum256(source)
	digest := hex.EncodeToString(digestBytes[:])

	instances := load.Instances([]string{"./foundation"}, &load.Config{Dir: root})
	if len(instances) != 1 {
		return generationSpec{}, "", fmt.Errorf("load StackAction CUE authority: got %d instances", len(instances))
	}
	value := cuecontext.New().BuildInstance(instances[0])
	if err := value.Err(); err != nil {
		return generationSpec{}, "", fmt.Errorf("build StackAction CUE authority: %w", err)
	}
	generation := value.LookupPath(cue.ParsePath("stackActionGeneration"))
	if !generation.Exists() {
		return generationSpec{}, "", fmt.Errorf("StackAction CUE generation projection is missing")
	}
	var spec generationSpec
	if err := generation.Decode(&spec); err != nil {
		return generationSpec{}, "", fmt.Errorf("decode StackAction CUE generation projection: %w", err)
	}
	if err := validateSpec(spec); err != nil {
		return generationSpec{}, "", err
	}
	return spec, digest, nil
}

func validateSpec(spec generationSpec) error {
	if spec.WireVersion == "" || spec.Target == "" || spec.PathPrefix == "" {
		return fmt.Errorf("StackAction generation metadata is incomplete")
	}
	if _, ok := spec.Enums["Action"]; !ok {
		return fmt.Errorf("StackAction Action enum is missing")
	}
	for _, name := range []string{"Request", "Response"} {
		if _, ok := spec.Types[name]; !ok {
			return fmt.Errorf("StackAction %s type is missing", name)
		}
	}
	enumOrders := map[int]string{}
	for name, enum := range spec.Enums {
		if enum.GoName == "" || len(enum.Values) == 0 {
			return fmt.Errorf("StackAction enum %s is incomplete", name)
		}
		if previous, exists := enumOrders[enum.Order]; exists {
			return fmt.Errorf("StackAction enums %s and %s share order %d", previous, name, enum.Order)
		}
		enumOrders[enum.Order] = name
		values := map[string]bool{}
		constants := map[string]bool{}
		for _, value := range enum.Values {
			if value.Value == "" || value.GoConst == "" || values[value.Value] || constants[value.GoConst] {
				return fmt.Errorf("StackAction enum %s has an invalid or duplicate value %q", name, value.Value)
			}
			values[value.Value] = true
			constants[value.GoConst] = true
		}
	}
	typeOrders := map[int]string{}
	openAPITypes := map[string]bool{}
	for name, typ := range spec.Types {
		if typ.GoName == "" || typ.OpenAPIName == "" || len(typ.Fields) == 0 {
			return fmt.Errorf("StackAction type %s is incomplete", name)
		}
		if previous, exists := typeOrders[typ.Order]; exists {
			return fmt.Errorf("StackAction types %s and %s share order %d", previous, name, typ.Order)
		}
		if openAPITypes[typ.OpenAPIName] {
			return fmt.Errorf("StackAction OpenAPI type %s is duplicated", typ.OpenAPIName)
		}
		typeOrders[typ.Order] = name
		openAPITypes[typ.OpenAPIName] = true
	}
	for name, typ := range spec.Types {
		seenJSON := map[string]bool{}
		seenGo := map[string]bool{}
		for _, field := range typ.Fields {
			if field.JSON == "" || field.GoName == "" || field.GoType == "" || seenJSON[field.JSON] || seenGo[field.GoName] {
				return fmt.Errorf("StackAction type %s has an invalid field %q", name, field.JSON)
			}
			if _, forbidden := forbiddenPublicWireFields[field.JSON]; forbidden {
				return fmt.Errorf("StackAction type %s exposes forbidden secret-bearing field %q; use a scoped reference", name, field.JSON)
			}
			seenJSON[field.JSON] = true
			seenGo[field.GoName] = true
			if field.OpenAPI.Kind == "enum" {
				if _, ok := spec.Enums[field.OpenAPI.Enum]; !ok {
					return fmt.Errorf("StackAction type %s field %s references unknown enum %q", name, field.JSON, field.OpenAPI.Enum)
				}
			}
			if field.OpenAPI.Kind == "ref" && !openAPITypes[field.OpenAPI.Ref] {
				return fmt.Errorf("StackAction type %s field %s references unknown OpenAPI type %q", name, field.JSON, field.OpenAPI.Ref)
			}
			if field.OpenAPI.Kind == "array" && field.OpenAPI.ItemsKind == "ref" && !openAPITypes[field.OpenAPI.ItemsRef] {
				return fmt.Errorf("StackAction type %s field %s references unknown OpenAPI item type %q", name, field.JSON, field.OpenAPI.ItemsRef)
			}
		}
		for _, alternative := range typ.AnyOfRequired {
			if len(alternative) == 0 {
				return fmt.Errorf("StackAction type %s has an empty required-field alternative", name)
			}
			for _, field := range alternative {
				if !seenJSON[field] {
					return fmt.Errorf("StackAction type %s required-field alternative references unknown field %q", name, field)
				}
			}
		}
	}
	actions := map[string]bool{}
	for _, action := range spec.Enums["Action"].Values {
		actions[action.Value] = true
	}
	paths := map[string]bool{}
	operations := map[string]bool{}
	constants := map[string]bool{}
	for _, path := range spec.Paths {
		fullPath := spec.PathPrefix + path.Suffix
		if !strings.HasPrefix(path.Suffix, "/") || path.OperationID == "" || path.GoConst == "" || paths[fullPath] || operations[path.OperationID] || constants[path.GoConst] || !actions[path.Action] {
			return fmt.Errorf("StackAction path %q has invalid or duplicate metadata", fullPath)
		}
		paths[fullPath] = true
		operations[path.OperationID] = true
		constants[path.GoConst] = true
	}
	return nil
}

func renderGo(spec generationSpec, digest string) ([]byte, error) {
	var b strings.Builder
	b.WriteString("// Code generated by " + GeneratorVersion + "; DO NOT EDIT.\n")
	b.WriteString("// Source: " + contractSource + "\n")
	b.WriteString("// Contract SHA-256: " + digest + "\n\n")
	b.WriteString("package stackaction\n\n")
	b.WriteString("import (\n\t\"strings\"\n\t\"time\"\n)\n\n")
	b.WriteString("const (\n")
	fmt.Fprintf(&b, "\t// GeneratorVersion identifies the deterministic contract generator.\n\tGeneratorVersion = %q\n", GeneratorVersion)
	fmt.Fprintf(&b, "\t// SourceSHA256 binds this projection to the canonical CUE source bytes.\n\tSourceSHA256 = %q\n", digest)
	fmt.Fprintf(&b, "\t// WireVersion identifies the generated StackAction wire contract.\n\tWireVersion = %q\n", spec.WireVersion)
	fmt.Fprintf(&b, "\t// TargetStackKits identifies StackKits as the action target.\n\tTargetStackKits = %q\n", spec.Target)
	fmt.Fprintf(&b, "\t// PathPrefix is the canonical internal StackAction transport prefix.\n\tPathPrefix = %q\n", spec.PathPrefix)
	fmt.Fprintf(&b, "\t// ObservationVersion identifies the generated observation envelope.\n\tObservationVersion = %q\n", spec.ObservationVersion)
	b.WriteString("\t// ObservationVersionV1 is retained as a source-compatible alias.\n\tObservationVersionV1 = ObservationVersion\n")
	for _, path := range spec.Paths {
		fmt.Fprintf(&b, "\t// %s is the canonical path for the %q action.\n\t%s = PathPrefix + %q\n", path.GoConst, path.Action, path.GoConst, path.Suffix)
	}
	b.WriteString(")\n\n")

	for _, named := range orderedEnums(spec.Enums) {
		fmt.Fprintf(&b, "// %s is a closed StackAction wire vocabulary.\ntype %s string\n\nconst (\n", named.spec.GoName, named.spec.GoName)
		for _, value := range named.spec.Values {
			fmt.Fprintf(&b, "\t// %s represents the %q wire value.\n\t%s %s = %q\n", value.GoConst, value.Value, value.GoConst, named.spec.GoName, value.Value)
		}
		b.WriteString(")\n\n")
	}

	for _, named := range orderedTypes(spec.Types) {
		fmt.Fprintf(&b, "// %s is %s\ntype %s struct {\n", named.spec.GoName, sentence(named.spec.Description), named.spec.GoName)
		for _, field := range named.spec.Fields {
			jsonTag := field.JSON
			if !field.Required {
				jsonTag += ",omitempty"
			}
			fmt.Fprintf(&b, "\t// %s maps the %q wire field.\n\t%s %s `json:%q`\n", field.GoName, field.JSON, field.GoName, field.GoType, jsonTag)
		}
		b.WriteString("}\n\n")
	}

	b.WriteString("// NormalizeAction canonicalizes a StackAction name.\nfunc NormalizeAction(action string) Action {\n\taction = strings.ToLower(strings.TrimSpace(action))\n\taction = strings.ReplaceAll(action, \"-\", \"_\")\n\treturn Action(action)\n}\n\n")
	b.WriteString("// IsStackKitsAction reports whether StackKits owns the action.\nfunc IsStackKitsAction(action Action) bool {\n\tswitch action {\n")
	actions := spec.Enums["Action"].Values
	for index, value := range actions {
		if index == 0 {
			b.WriteString("\tcase ")
		} else {
			b.WriteString(", ")
		}
		b.WriteString(value.GoConst)
	}
	b.WriteString(":\n\t\treturn true\n\tdefault:\n\t\treturn false\n\t}\n}\n\n")
	b.WriteString("// IsBackupAction reports whether the node-side backup engine owns the action.\nfunc IsBackupAction(action Action) bool {\n\tswitch action {\n")
	backupIndex := 0
	for _, value := range spec.Enums["Action"].Values {
		if value.Backup {
			if backupIndex == 0 {
				b.WriteString("\tcase ")
			} else {
				b.WriteString(", ")
			}
			b.WriteString(value.GoConst)
			backupIndex++
		}
	}
	b.WriteString(":\n\t\treturn true\n\tdefault:\n\t\treturn false\n\t}\n}\n")

	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("format generated StackAction Go: %w", err)
	}
	return formatted, nil
}

type namedEnum struct {
	name string
	spec enumSpec
}

func orderedEnums(values map[string]enumSpec) []namedEnum {
	out := make([]namedEnum, 0, len(values))
	for name, spec := range values {
		out = append(out, namedEnum{name: name, spec: spec})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].spec.Order < out[j].spec.Order })
	return out
}

type namedType struct {
	name string
	spec objectTypeSpec
}

func orderedTypes(values map[string]objectTypeSpec) []namedType {
	out := make([]namedType, 0, len(values))
	for name, spec := range values {
		out = append(out, namedType{name: name, spec: spec})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].spec.Order < out[j].spec.Order })
	return out
}

func sentence(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "is generated from the StackKits CUE authority."
	}
	return strings.ToLower(value[:1]) + value[1:]
}

func renderOpenAPI(document []byte, spec generationSpec) ([]byte, error) {
	withPaths, err := replaceRegion(string(document), pathsBegin, pathsEnd, renderOpenAPIPaths(spec))
	if err != nil {
		return nil, err
	}
	withSchemas, err := replaceRegion(withPaths, schemasBegin, schemasEnd, renderOpenAPISchemas(spec))
	if err != nil {
		return nil, err
	}
	return []byte(withSchemas), nil
}

func renderStandaloneOpenAPI(spec generationSpec, sourceDigest string) []byte {
	var b strings.Builder
	b.WriteString("openapi: 3.1.0\n")
	b.WriteString("info:\n")
	b.WriteString("  title: StackAction Contract\n")
	fmt.Fprintf(&b, "  version: %s\n", yamlString(spec.WireVersion))
	b.WriteString("  description: Provider-free StackKits action request and evidence contract generated from CUE.\n")
	fmt.Fprintf(&b, "x-kombify-generator-version: %s\n", yamlString(GeneratorVersion))
	fmt.Fprintf(&b, "x-kombify-source-path: %s\n", yamlString(contractSource))
	fmt.Fprintf(&b, "x-kombify-source-sha256: %s\n", yamlString(sourceDigest))
	b.WriteString("paths:\n")
	b.WriteString(renderStandaloneOpenAPIPaths(spec))
	b.WriteString("components:\n  schemas:\n")
	b.WriteString(renderOpenAPISchemas(spec))
	return []byte(b.String())
}

func renderStandaloneOpenAPIPaths(spec generationSpec) string {
	var b strings.Builder
	requestSchema := spec.Types["Request"].OpenAPIName
	responseSchema := spec.Types["Response"].OpenAPIName
	for _, path := range spec.Paths {
		fmt.Fprintf(&b, "  %s%s:\n", spec.PathPrefix, path.Suffix)
		b.WriteString("    post:\n")
		fmt.Fprintf(&b, "      operationId: %s\n      summary: %s\n", path.OperationID, yamlString(path.Summary))
		b.WriteString("      tags: [StackAction]\n      requestBody:\n        required: true\n        content:\n          application/json:\n            schema:\n              allOf:\n                - $ref: ")
		b.WriteString(yamlString("#/components/schemas/" + requestSchema))
		b.WriteString("\n                - type: object\n                  properties:\n                    action:\n                      const: ")
		b.WriteString(yamlString(path.Action))
		b.WriteString("\n      responses:\n        \"200\":\n          description: StackAction result\n          content:\n            application/json:\n              schema:\n                $ref: ")
		b.WriteString(yamlString("#/components/schemas/" + responseSchema))
		b.WriteString("\n        \"400\":\n          description: Invalid StackAction request\n\n")
	}
	return b.String()
}

func replaceRegion(document, begin, end, body string) (string, error) {
	start := strings.Index(document, begin)
	finish := strings.Index(document, end)
	if start < 0 || finish < 0 || finish < start {
		return "", fmt.Errorf("generated OpenAPI region %q..%q is missing", begin, end)
	}
	finish += len(end)
	return document[:start] + begin + "\n" + strings.TrimRight(body, "\n") + "\n" + end + document[finish:], nil
}

func renderOpenAPIPaths(spec generationSpec) string {
	var b strings.Builder
	requestSchema := spec.Types["Request"].OpenAPIName
	responseSchema := spec.Types["Response"].OpenAPIName
	for _, path := range spec.Paths {
		fmt.Fprintf(&b, "  %s%s:\n", spec.PathPrefix, path.Suffix)
		b.WriteString("    post:\n")
		fmt.Fprintf(&b, "      operationId: %s\n      summary: %s\n", path.OperationID, yamlString(path.Summary))
		b.WriteString("      description: Requires X-Kombify-Service-Auth from caller techstack.\n      tags: [StackAction]\n      requestBody:\n        required: true\n        content:\n          application/json:\n            schema:\n              allOf:\n                - $ref: ")
		b.WriteString(yamlString("#/components/schemas/" + requestSchema))
		b.WriteString("\n                - type: object\n                  properties:\n                    action:\n                      const: ")
		b.WriteString(yamlString(path.Action))
		b.WriteString("\n      responses:\n        \"200\":\n          description: StackAction accepted\n          content:\n            application/json:\n              schema:\n                allOf:\n                  - $ref: \"#/components/schemas/SuccessEnvelope\"\n                  - properties:\n                      data:\n                        $ref: ")
		b.WriteString(yamlString("#/components/schemas/" + responseSchema))
		b.WriteString("\n        \"400\":\n          $ref: \"#/components/responses/BadRequest\"\n\n")
	}
	return b.String()
}

func renderOpenAPISchemas(spec generationSpec) string {
	var b strings.Builder
	for _, named := range orderedTypes(spec.Types) {
		typ := named.spec
		fmt.Fprintf(&b, "    %s:\n      type: object\n      description: %s\n      additionalProperties: false\n", typ.OpenAPIName, yamlString(typ.Description))
		required := make([]string, 0)
		for _, field := range typ.Fields {
			if field.Required {
				required = append(required, field.JSON)
			}
		}
		if len(required) > 0 {
			b.WriteString("      required:\n")
			for _, field := range required {
				fmt.Fprintf(&b, "        - %s\n", field)
			}
		}
		if len(typ.AnyOfRequired) > 0 {
			b.WriteString("      anyOf:\n")
			for _, alternative := range typ.AnyOfRequired {
				b.WriteString("        - required:\n")
				for _, field := range alternative {
					fmt.Fprintf(&b, "            - %s\n", field)
				}
			}
		}
		b.WriteString("      properties:\n")
		for _, field := range typ.Fields {
			fmt.Fprintf(&b, "        %s:\n", field.JSON)
			renderOpenAPIField(&b, field.OpenAPI, spec, "          ")
		}
	}
	return b.String()
}

func renderOpenAPIField(b *strings.Builder, field openAPIFieldSpec, spec generationSpec, indent string) {
	switch field.Kind {
	case "ref":
		fmt.Fprintf(b, "%s$ref: %s\n", indent, yamlString("#/components/schemas/"+field.Ref))
	case "enum":
		b.WriteString(indent + "type: string\n" + indent + "enum:\n")
		for _, value := range spec.Enums[field.Enum].Values {
			fmt.Fprintf(b, "%s  - %s\n", indent, yamlString(value.Value))
		}
	case "array":
		b.WriteString(indent + "type: array\n")
		if field.MinItems != nil {
			fmt.Fprintf(b, "%sminItems: %d\n", indent, *field.MinItems)
		}
		b.WriteString(indent + "items:\n")
		if field.ItemsKind == "ref" {
			fmt.Fprintf(b, "%s  $ref: %s\n", indent, yamlString("#/components/schemas/"+field.ItemsRef))
		} else {
			fmt.Fprintf(b, "%s  type: %s\n", indent, field.ItemsKind)
			if field.ItemsFormat != "" {
				fmt.Fprintf(b, "%s  format: %s\n", indent, field.ItemsFormat)
			}
		}
	case "object":
		b.WriteString(indent + "type: object\n")
		if field.AdditionalProperties == "any" {
			b.WriteString(indent + "additionalProperties: true\n")
		} else {
			b.WriteString(indent + "additionalProperties:\n")
			fmt.Fprintf(b, "%s  type: %s\n", indent, field.AdditionalProperties)
		}
	default:
		fmt.Fprintf(b, "%stype: %s\n", indent, field.Kind)
		if field.Format != "" {
			fmt.Fprintf(b, "%sformat: %s\n", indent, field.Format)
		}
		if field.Pattern != "" {
			fmt.Fprintf(b, "%spattern: %s\n", indent, yamlString(field.Pattern))
		}
		if field.Minimum != nil {
			fmt.Fprintf(b, "%sminimum: %s\n", indent, number(*field.Minimum))
		}
		if field.Maximum != nil {
			fmt.Fprintf(b, "%smaximum: %s\n", indent, number(*field.Maximum))
		}
	}
}

func yamlString(value string) string { return strconv.Quote(value) }

func number(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }
