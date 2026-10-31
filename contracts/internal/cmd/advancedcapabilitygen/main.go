// Command advancedcapabilitygen projects the canonical Advanced-capability
// contract source (contracts/advancedcapability/contract.json) into its generated Go
// package file, JSON Schema, signed conformance fixtures, and a generation
// manifest that binds every output to the source by SHA-256.
//
// The source is the single authority for the stackkit.advanced-capability/v1
// and stackkit.advanced-trust-bundle/v1 wire. Techstack issues and StackKits
// verifies against the generated package; neither keeps its own copy.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	generatorVersion       = "advancedcapabilitygen/v1"
	manifestSchemaVersion  = "kombify.advanced-capability-generation-manifest/v1"
	sourceSchemaVersion    = "kombify.advanced-capability-contract/v1"
	sourceRepository       = "github.com/kombifyio/stackkits"
	sourceFile             = "contract.json"
	generatedGoFile        = "advancedcapability_gen.go"
	generatedSchemaFile    = "schema.json"
	manifestFile           = "manifest.json"
	fixtureIssuerID        = "techstack"
	capabilityDocumentName = "Capability"
	trustBundleName        = "TrustBundle"
)

type namedValue struct {
	Go    string          `json:"go"`
	Value json.RawMessage `json:"value"`
	Doc   string          `json:"doc"`
}

type operation struct {
	Go            string `json:"go"`
	Value         string `json:"value"`
	BackupRenewal bool   `json:"backupRenewal"`
}

type pattern struct {
	Name   string `json:"name"`
	Regexp string `json:"regexp"`
}

type field struct {
	JSON      string `json:"json"`
	Go        string `json:"go"`
	Type      string `json:"type"`
	Optional  bool   `json:"optional"`
	Signature bool   `json:"signature"`
	Pattern   string `json:"pattern"`
	Const     string `json:"const"`
	Format    string `json:"format"`
	Doc       string `json:"doc"`
}

type document struct {
	Go            string  `json:"go"`
	Doc           string  `json:"doc"`
	SigningDomain string  `json:"signingDomain"`
	Fields        []field `json:"fields"`
}

type fixture struct {
	Name       string                     `json:"name"`
	SeedHex    string                     `json:"seedHex"`
	Capability map[string]json.RawMessage `json:"capability"`
}

type contract struct {
	SchemaVersion string       `json:"schemaVersion"`
	WireVersion   string       `json:"wireVersion"`
	Constants     []namedValue `json:"constants"`
	Limits        []namedValue `json:"limits"`
	Operations    []operation  `json:"operations"`
	Patterns      []pattern    `json:"patterns"`
	Documents     []document   `json:"documents"`
	Fixtures      []fixture    `json:"fixtures"`
}

type manifest struct {
	SchemaVersion    string `json:"schemaVersion"`
	GeneratorVersion string `json:"generatorVersion"`
	WireVersion      string `json:"wireVersion"`
	Source           struct {
		Repository string `json:"repository"`
		Path       string `json:"path"`
		SHA256     string `json:"sha256"`
	} `json:"source"`
	Outputs map[string]string `json:"outputs"`
}

func main() {
	directory := flag.String("dir", "contracts/advancedcapability", "package directory holding contract.json")
	write := flag.Bool("write", false, "write the generated outputs")
	check := flag.Bool("check", false, "fail when the checked-in outputs differ from a fresh generation")
	flag.Parse()
	if *write == *check {
		fmt.Fprintln(os.Stderr, "exactly one of --write or --check is required")
		os.Exit(2)
	}
	if err := run(*directory, *write); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *check {
		fmt.Println("advancedcapability-generation=PASS")
	}
}

func run(directory string, write bool) error {
	source, err := os.ReadFile(filepath.Join(directory, sourceFile))
	if err != nil {
		return fmt.Errorf("read contract source: %w", err)
	}
	outputs, err := generate(source)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(outputs))
	for name := range outputs {
		names = append(names, name)
	}
	sort.Strings(names)
	if write {
		for _, name := range names {
			target := filepath.Join(directory, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(target, outputs[name], 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	var drift []string
	for _, name := range names {
		current, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(current, outputs[name]) {
			drift = append(drift, name)
		}
	}
	if len(drift) != 0 {
		return fmt.Errorf("generated Advanced-capability outputs drifted from %s; run mise run contracts:advancedcapability:generate: %s", sourceFile, strings.Join(drift, ", "))
	}
	return nil
}

// generate renders every output from the exact source bytes. It is pure: the
// same source always yields the same bytes, which is what --check relies on.
func generate(source []byte) (map[string][]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	var spec contract
	if err := decoder.Decode(&spec); err != nil {
		return nil, fmt.Errorf("decode contract source: %w", err)
	}
	if err := validateSpec(spec); err != nil {
		return nil, fmt.Errorf("contract source: %w", err)
	}
	outputs := map[string][]byte{}
	goSource, err := renderGo(spec)
	if err != nil {
		return nil, err
	}
	outputs[generatedGoFile] = goSource
	schema, err := renderSchema(spec)
	if err != nil {
		return nil, err
	}
	outputs[generatedSchemaFile] = schema
	for _, item := range spec.Fixtures {
		capability, bundle, err := renderFixture(spec, item)
		if err != nil {
			return nil, fmt.Errorf("fixture %s: %w", item.Name, err)
		}
		outputs["testdata/"+item.Name+".json"] = capability
		outputs["testdata/"+item.Name+".trust-bundle.json"] = bundle
	}

	var record manifest
	record.SchemaVersion = manifestSchemaVersion
	record.GeneratorVersion = generatorVersion
	record.WireVersion = spec.WireVersion
	record.Source.Repository = sourceRepository
	record.Source.Path = "contracts/advancedcapability/" + sourceFile
	record.Source.SHA256 = digest(source)
	record.Outputs = map[string]string{}
	for name, content := range outputs {
		record.Outputs[name] = digest(content)
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, err
	}
	outputs[manifestFile] = append(encoded, '\n')
	return outputs, nil
}

func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

var goIdentifier = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)

func validateSpec(spec contract) error {
	if spec.SchemaVersion != sourceSchemaVersion {
		return fmt.Errorf("schemaVersion %q is not %q", spec.SchemaVersion, sourceSchemaVersion)
	}
	constants := map[string]string{}
	for _, constant := range spec.Constants {
		var value string
		if err := json.Unmarshal(constant.Value, &value); err != nil || !goIdentifier.MatchString(constant.Go) {
			return fmt.Errorf("constant %q must be a string with an exported Go name", constant.Go)
		}
		constants[constant.Go] = value
	}
	if constants["SchemaVersion"] != spec.WireVersion {
		return errors.New("SchemaVersion constant must equal wireVersion")
	}
	for _, required := range []string{"TrustBundleSchemaVersion", "Audience", "IssuerClass"} {
		if constants[required] == "" {
			return fmt.Errorf("constant %s is required", required)
		}
	}
	for _, limit := range spec.Limits {
		var value int64
		if err := json.Unmarshal(limit.Value, &value); err != nil || value <= 0 || !goIdentifier.MatchString(limit.Go) {
			return fmt.Errorf("limit %q must be a positive integer with an exported Go name", limit.Go)
		}
	}
	renewalOperations := 0
	for index, item := range spec.Operations {
		if !goIdentifier.MatchString(item.Go) || !strings.HasPrefix(item.Go, "Operation") || item.Value == "" {
			return fmt.Errorf("operation %q is malformed", item.Go)
		}
		if index > 0 && spec.Operations[index-1].Value >= item.Value {
			return errors.New("operations must be sorted by value without duplicates")
		}
		if item.BackupRenewal {
			renewalOperations++
		}
	}
	if renewalOperations != 1 {
		return errors.New("exactly one operation authorizes a backup renewal")
	}
	patterns := map[string]bool{}
	for _, item := range spec.Patterns {
		if _, err := regexp.Compile(item.Regexp); err != nil || !strings.HasPrefix(item.Regexp, "^") || !strings.HasSuffix(item.Regexp, "$") {
			return fmt.Errorf("pattern %q must be an anchored regular expression", item.Name)
		}
		patterns[item.Name] = true
	}
	documents := map[string]bool{}
	for _, item := range spec.Documents {
		documents[item.Go] = true
	}
	for _, name := range []string{capabilityDocumentName, "BackupRenewal", trustBundleName, "TrustBundleKey"} {
		if !documents[name] {
			return fmt.Errorf("document %s is required", name)
		}
	}
	for _, item := range spec.Documents {
		if len(item.Fields) == 0 || item.Fields[0].Optional || item.Fields[0].Signature {
			return fmt.Errorf("%s: the first member in canonical order must always be present", item.Go)
		}
		signatures := 0
		for index, member := range item.Fields {
			// JCS orders members by UTF-16 code units; every member name is ASCII,
			// so byte order is the canonical order the encoder emits.
			if index > 0 && item.Fields[index-1].JSON >= member.JSON {
				return fmt.Errorf("%s fields must be sorted by JSON name for RFC 8785 order", item.Go)
			}
			if !goIdentifier.MatchString(member.Go) {
				return fmt.Errorf("%s.%s has no exported Go name", item.Go, member.JSON)
			}
			if member.Pattern != "" && !patterns[member.Pattern] {
				return fmt.Errorf("%s.%s names unknown pattern %q", item.Go, member.JSON, member.Pattern)
			}
			if member.Const != "" {
				if _, ok := constants[member.Const]; !ok {
					return fmt.Errorf("%s.%s names unknown constant %q", item.Go, member.JSON, member.Const)
				}
			}
			switch {
			case member.Type == "string", member.Type == "operationSet":
			case strings.HasPrefix(member.Type, "object:") && documents[strings.TrimPrefix(member.Type, "object:")]:
			case strings.HasPrefix(member.Type, "array:") && documents[strings.TrimPrefix(member.Type, "array:")]:
			default:
				return fmt.Errorf("%s.%s has unsupported type %q", item.Go, member.JSON, member.Type)
			}
			if member.Optional && !strings.HasPrefix(member.Type, "object:") {
				return fmt.Errorf("%s.%s: only object members may be optional", item.Go, member.JSON)
			}
			if member.Signature {
				signatures++
				if member.Type != "string" {
					return fmt.Errorf("%s.%s: the signature must be a string", item.Go, member.JSON)
				}
			}
		}
		if (item.SigningDomain != "") != (signatures == 1) || signatures > 1 {
			return fmt.Errorf("%s: a signed document has exactly one signature member and a signing domain", item.Go)
		}
		if item.SigningDomain != "" && constants[item.SigningDomain] == "" {
			return fmt.Errorf("%s: signing domain %q is not a constant", item.Go, item.SigningDomain)
		}
	}
	names := map[string]bool{}
	for _, item := range spec.Fixtures {
		if names[item.Name] || !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`).MatchString(item.Name) {
			return fmt.Errorf("fixture name %q is malformed or duplicated", item.Name)
		}
		names[item.Name] = true
	}
	return nil
}

func findDocument(spec contract, name string) document {
	for _, item := range spec.Documents {
		if item.Go == name {
			return item
		}
	}
	return document{}
}

func goType(member field) string {
	switch {
	case member.Type == "operationSet":
		return "[]string"
	case strings.HasPrefix(member.Type, "object:"):
		return "*" + strings.TrimPrefix(member.Type, "object:")
	case strings.HasPrefix(member.Type, "array:"):
		return "[]" + strings.TrimPrefix(member.Type, "array:")
	default:
		return "string"
	}
}

func patternVar(name string) string {
	return name + "Pattern"
}

func comment(out *strings.Builder, indent, text string) {
	if text == "" {
		return
	}
	line := indent + "//"
	for _, word := range strings.Fields(text) {
		if len(line)+1+len(word) > 78 && line != indent+"//" {
			out.WriteString(line + "\n")
			line = indent + "//"
		}
		line += " " + word
	}
	out.WriteString(line + "\n")
}

func renderGo(spec contract) ([]byte, error) {
	var out strings.Builder
	fmt.Fprintf(&out, "// Code generated by %s from %s; DO NOT EDIT.\n\n", generatorVersion, sourceFile)
	out.WriteString("package advancedcapability\n\n")
	out.WriteString("import (\n\t\"regexp\"\n\t\"strconv\"\n)\n\n")

	out.WriteString("const (\n")
	for _, constant := range spec.Constants {
		var value string
		_ = json.Unmarshal(constant.Value, &value)
		comment(&out, "\t", constant.Doc)
		fmt.Fprintf(&out, "\t%s = %s\n", constant.Go, strconv.Quote(value))
	}
	out.WriteString(")\n\n")

	out.WriteString("const (\n")
	for _, limit := range spec.Limits {
		comment(&out, "\t", limit.Doc)
		fmt.Fprintf(&out, "\t%s = %s\n", limit.Go, string(limit.Value))
	}
	out.WriteString(")\n\n")

	out.WriteString("// Advanced operation identifiers. A capability authorizes a sorted subset.\nconst (\n")
	renewal := ""
	for _, item := range spec.Operations {
		fmt.Fprintf(&out, "\t%s = %s\n", item.Go, strconv.Quote(item.Value))
		if item.BackupRenewal {
			renewal = item.Go
		}
	}
	out.WriteString(")\n\n")
	fmt.Fprintf(&out, "// BackupRenewalOperation is the only operation a backup renewal capability\n// may carry.\nconst BackupRenewalOperation = %s\n\n", renewal)

	out.WriteString("// Operations returns every Advanced operation in canonical (sorted) order.\nfunc Operations() []string {\n\treturn []string{\n")
	for _, item := range spec.Operations {
		fmt.Fprintf(&out, "\t\t%s,\n", item.Go)
	}
	out.WriteString("\t}\n}\n\n")
	out.WriteString("// IsOperation reports whether value is an Advanced operation identifier.\nfunc IsOperation(value string) bool {\n\tswitch value {\n\tcase ")
	for index, item := range spec.Operations {
		if index > 0 {
			out.WriteString(", ")
		}
		out.WriteString(item.Go)
	}
	out.WriteString(":\n\t\treturn true\n\t}\n\treturn false\n}\n\n")

	out.WriteString("var (\n")
	for _, item := range spec.Patterns {
		fmt.Fprintf(&out, "\t%s = regexp.MustCompile(%s)\n", patternVar(item.Name), "`"+item.Regexp+"`")
	}
	out.WriteString(")\n\n")

	for _, item := range spec.Documents {
		comment(&out, "", item.Doc)
		fmt.Fprintf(&out, "type %s struct {\n", item.Go)
		for _, member := range item.Fields {
			comment(&out, "\t", member.Doc)
			tag := member.JSON
			if member.Optional {
				tag += ",omitempty"
			}
			fmt.Fprintf(&out, "\t%s %s `json:%s`\n", member.Go, goType(member), strconv.Quote(tag))
		}
		out.WriteString("}\n\n")
	}

	for _, item := range spec.Documents {
		receiver := strings.ToLower(item.Go[:1])
		signed := item.SigningDomain != ""
		if signed {
			fmt.Fprintf(&out, "// appendCanonical appends the RFC 8785 bytes of %s; the signature member\n// is emitted only when signed is true.\n", item.Go)
			fmt.Fprintf(&out, "func (%s %s) appendCanonical(out []byte, signed bool) []byte {\n", receiver, item.Go)
		} else {
			fmt.Fprintf(&out, "// appendCanonical appends the RFC 8785 bytes of %s.\n", item.Go)
			fmt.Fprintf(&out, "func (%s %s) appendCanonical(out []byte) []byte {\n", receiver, item.Go)
		}
		// validateSpec guarantees the first member is always present, so every
		// later member carries its leading separator statically.
		for index, member := range item.Fields {
			value := receiver + "." + member.Go
			prefix := "{"
			if index > 0 {
				prefix = ","
			}
			name := strconv.Quote(prefix + strconv.Quote(member.JSON) + ":")
			indent := "\t"
			switch {
			case member.Signature:
				out.WriteString("\tif signed {\n")
				indent = "\t\t"
			case member.Optional:
				fmt.Fprintf(&out, "\tif %s != nil {\n", value)
				indent = "\t\t"
			}
			fmt.Fprintf(&out, "%sout = append(out, %s...)\n", indent, name)
			switch {
			case member.Type == "string":
				fmt.Fprintf(&out, "%sout = strconv.AppendQuote(out, %s)\n", indent, value)
			case member.Type == "operationSet":
				fmt.Fprintf(&out, "%sout = append(out, '[')\n%sfor index, element := range %s {\n%s\tif index > 0 {\n%s\t\tout = append(out, ',')\n%s\t}\n%s\tout = strconv.AppendQuote(out, element)\n%s}\n%sout = append(out, ']')\n", indent, indent, value, indent, indent, indent, indent, indent, indent)
			case strings.HasPrefix(member.Type, "object:"):
				fmt.Fprintf(&out, "%sout = %s.appendCanonical(out)\n", indent, value)
			case strings.HasPrefix(member.Type, "array:"):
				fmt.Fprintf(&out, "%sout = append(out, '[')\n%sfor index, element := range %s {\n%s\tif index > 0 {\n%s\t\tout = append(out, ',')\n%s\t}\n%s\tout = element.appendCanonical(out)\n%s}\n%sout = append(out, ']')\n", indent, indent, value, indent, indent, indent, indent, indent, indent)
			}
			if member.Signature || member.Optional {
				out.WriteString("\t}\n")
			}
		}
		out.WriteString("\treturn append(out, '}')\n}\n\n")

		fmt.Fprintf(&out, "// checkFormats checks the constant and pattern members of %s.\n", item.Go)
		fmt.Fprintf(&out, "func (%s %s) checkFormats() error {\n", receiver, item.Go)
		for _, member := range item.Fields {
			value := receiver + "." + member.Go
			switch {
			case member.Const != "":
				fmt.Fprintf(&out, "\tif %s != %s {\n\t\treturn invalid(%s, \"must equal \"+strconv.Quote(%s))\n\t}\n", value, member.Const, strconv.Quote(member.JSON), member.Const)
			case member.Pattern != "":
				fmt.Fprintf(&out, "\tif !%s.MatchString(%s) {\n\t\treturn invalid(%s, \"is not canonical\")\n\t}\n", patternVar(member.Pattern), value, strconv.Quote(member.JSON))
			}
		}
		out.WriteString("\treturn nil\n}\n\n")
	}

	formatted, err := format.Source([]byte(out.String()))
	if err != nil {
		return nil, fmt.Errorf("format generated Go: %w\n%s", err, out.String())
	}
	return formatted, nil
}

// orderedObject keeps JSON Schema members in a fixed order so the projection
// is byte-stable.
type orderedObject []member

type member struct {
	key   string
	value any
}

func (object orderedObject) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for index, item := range object {
		if index > 0 {
			out.WriteByte(',')
		}
		key, _ := json.Marshal(item.key)
		out.Write(key)
		out.WriteByte(':')
		value, err := json.Marshal(item.value)
		if err != nil {
			return nil, err
		}
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

func renderSchema(spec contract) ([]byte, error) {
	constants := map[string]string{}
	for _, constant := range spec.Constants {
		var value string
		_ = json.Unmarshal(constant.Value, &value)
		constants[constant.Go] = value
	}
	patterns := map[string]string{}
	for _, item := range spec.Patterns {
		patterns[item.Name] = item.Regexp
	}
	operations := make([]string, 0, len(spec.Operations))
	for _, item := range spec.Operations {
		operations = append(operations, item.Value)
	}
	formats := map[string]string{
		"rfc3339-utc-seconds": "date-time",
		"rfc3339-utc":         "date-time",
	}
	definitions := orderedObject{}
	for _, item := range spec.Documents {
		properties := orderedObject{}
		var required []string
		for _, field := range item.Fields {
			property := orderedObject{}
			switch {
			case field.Type == "operationSet":
				property = append(property,
					member{"type", "array"}, member{"minItems", 1}, member{"uniqueItems", true},
					member{"items", orderedObject{{"enum", operations}}})
			case strings.HasPrefix(field.Type, "object:"):
				property = append(property, member{"$ref", "#/$defs/" + strings.TrimPrefix(field.Type, "object:")})
			case strings.HasPrefix(field.Type, "array:"):
				property = append(property, member{"type", "array"}, member{"minItems", 1},
					member{"items", orderedObject{{"$ref", "#/$defs/" + strings.TrimPrefix(field.Type, "array:")}}})
			default:
				property = append(property, member{"type", "string"})
				if field.Const != "" {
					property = append(property, member{"const", constants[field.Const]})
				}
				if field.Pattern != "" {
					property = append(property, member{"pattern", patterns[field.Pattern]})
				}
				if format, ok := formats[field.Format]; ok {
					property = append(property, member{"format", format})
				}
				if field.Format != "" {
					property = append(property, member{"x-kombify-format", field.Format})
				}
			}
			if field.Doc != "" {
				property = append(property, member{"description", field.Doc})
			}
			properties = append(properties, member{field.JSON, property})
			if !field.Optional {
				required = append(required, field.JSON)
			}
		}
		definition := orderedObject{
			{"type", "object"},
			{"additionalProperties", false},
			{"required", required},
			{"properties", properties},
		}
		if item.Doc != "" {
			definition = append(orderedObject{{"description", item.Doc}}, definition...)
		}
		definitions = append(definitions, member{item.Go, definition})
	}
	schema := orderedObject{
		{"$schema", "https://json-schema.org/draft/2020-12/schema"},
		{"$id", "urn:kombify:stackkit:advanced-capability:v1"},
		{"title", "Advanced capability and trust bundle wire"},
		{"description", "Generated by " + generatorVersion + " from " + sourceFile + ". Documents are exact RFC 8785 (JCS) bytes; the capability signature is Ed25519 over SHA-256(schemaVersion || 0x00 || unsigned JCS)."},
		{"oneOf", []orderedObject{{{"$ref", "#/$defs/" + capabilityDocumentName}}, {{"$ref", "#/$defs/" + trustBundleName}}}},
		{"$defs", definitions},
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, encoded, "", "  "); err != nil {
		return nil, err
	}
	indented.WriteByte('\n')
	return indented.Bytes(), nil
}

// renderFixture signs one conformance capability with its deterministic test
// seed. The encoder here is generic over the source field model and therefore
// independent of the generated Go encoder; the package tests require both to
// produce identical bytes.
func renderFixture(spec contract, item fixture) ([]byte, []byte, error) {
	seed, err := hex.DecodeString(item.SeedHex)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, nil, errors.New("seedHex must be one 32-byte Ed25519 test seed")
	}
	constants := map[string]string{}
	for _, constant := range spec.Constants {
		var value string
		_ = json.Unmarshal(constant.Value, &value)
		constants[constant.Go] = value
	}
	key := ed25519.NewKeyFromSeed(seed)
	publicKey := key.Public().(ed25519.PublicKey)
	keyDigest := sha256.Sum256(publicKey)
	keyID := "ed25519://sha256/" + hex.EncodeToString(keyDigest[:])

	values := map[string]json.RawMessage{}
	for name, value := range item.Capability {
		values[name] = value
	}
	capability := findDocument(spec, capabilityDocumentName)
	quoted := func(value string) json.RawMessage { return json.RawMessage(strconv.Quote(value)) }
	for _, field := range capability.Fields {
		if field.Const != "" {
			values[field.JSON] = quoted(constants[field.Const])
		}
	}
	values["keyId"] = quoted(keyID)
	unsigned, err := canonicalObject(spec, capability, values, false)
	if err != nil {
		return nil, nil, err
	}
	domain := constants[capability.SigningDomain]
	message := append(append([]byte(domain), 0), unsigned...)
	signingDigest := sha256.Sum256(message)
	for _, field := range capability.Fields {
		if field.Signature {
			values[field.JSON] = quoted(base64.RawStdEncoding.EncodeToString(ed25519.Sign(key, signingDigest[:])))
		}
	}
	signed, err := canonicalObject(spec, capability, values, true)
	if err != nil {
		return nil, nil, err
	}
	keyEntry := map[string]json.RawMessage{
		"issuerId":  quoted(fixtureIssuerID),
		"keyId":     quoted(keyID),
		"publicKey": quoted(base64.RawStdEncoding.EncodeToString(publicKey)),
	}
	keyBytes, err := canonicalObject(spec, findDocument(spec, "TrustBundleKey"), keyEntry, true)
	if err != nil {
		return nil, nil, err
	}
	bundle, err := canonicalObject(spec, findDocument(spec, trustBundleName), map[string]json.RawMessage{
		"keys":          json.RawMessage("[" + string(keyBytes) + "]"),
		"schemaVersion": quoted(constants["TrustBundleSchemaVersion"]),
	}, true)
	if err != nil {
		return nil, nil, err
	}
	return signed, bundle, nil
}

// canonicalObject emits members in the source order (validated as JCS order).
// String values are re-quoted from their decoded form; nested objects recurse.
func canonicalObject(spec contract, item document, values map[string]json.RawMessage, signed bool) ([]byte, error) {
	known := map[string]bool{}
	out := []byte{'{'}
	first := true
	for _, field := range item.Fields {
		known[field.JSON] = true
		raw, present := values[field.JSON]
		if field.Signature && !signed {
			continue
		}
		if !present {
			if field.Optional {
				continue
			}
			return nil, fmt.Errorf("%s.%s is missing", item.Go, field.JSON)
		}
		if !first {
			out = append(out, ',')
		}
		first = false
		out = strconv.AppendQuote(out, field.JSON)
		out = append(out, ':')
		switch {
		case field.Type == "string":
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, fmt.Errorf("%s.%s must be a string", item.Go, field.JSON)
			}
			out = strconv.AppendQuote(out, value)
		case field.Type == "operationSet":
			var elements []string
			if err := json.Unmarshal(raw, &elements); err != nil {
				return nil, fmt.Errorf("%s.%s must be a string array", item.Go, field.JSON)
			}
			out = append(out, '[')
			for index, element := range elements {
				if index > 0 {
					out = append(out, ',')
				}
				out = strconv.AppendQuote(out, element)
			}
			out = append(out, ']')
		case strings.HasPrefix(field.Type, "object:"):
			var nested map[string]json.RawMessage
			if err := json.Unmarshal(raw, &nested); err != nil {
				return nil, fmt.Errorf("%s.%s must be an object", item.Go, field.JSON)
			}
			encoded, err := canonicalObject(spec, findDocument(spec, strings.TrimPrefix(field.Type, "object:")), nested, signed)
			if err != nil {
				return nil, err
			}
			out = append(out, encoded...)
		case strings.HasPrefix(field.Type, "array:"):
			// Array elements are pre-rendered canonical objects.
			out = append(out, raw...)
		}
	}
	for name := range values {
		if !known[name] {
			return nil, fmt.Errorf("%s has unknown member %q", item.Go, name)
		}
	}
	return append(out, '}'), nil
}
