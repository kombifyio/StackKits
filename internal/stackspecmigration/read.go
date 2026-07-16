package stackspecmigration

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/pkg/models"
	"gopkg.in/yaml.v3"
)

// SourceVersion identifies which side of the one-minor dual-read seam a
// document belongs to.
type SourceVersion string

const (
	SourceVersionV1       SourceVersion = "v1"
	SourceVersionV2Alpha1 SourceVersion = "v2alpha1"
)

// Document preserves the original bytes so dual-read cannot silently discard
// fields. Legacy is populated only for v1. V2 contains only dispatch identity;
// the complete document must still be validated by the CUE v2 contract.
type Document struct {
	Version         SourceVersion
	Raw             []byte
	Legacy          *models.StackSpec
	UnknownV1Fields []string
	V2              *V2Identity
}

// V2Identity is the minimum safe dispatch header for a canonical v2 document.
type V2Identity struct {
	APIVersion string
	Kind       string
	KitProfile KitProfile
}

// ReadError is a typed fail-closed dual-read error.
type ReadError struct {
	Code    string
	Message string
}

func (e *ReadError) Error() string {
	if e == nil {
		return "StackSpec read failed"
	}
	return e.Message
}

// Read classifies a StackSpec without normalizing v1 or partially validating
// v2 as though it were complete. Unknown versions and mixed v1/v2 identity
// fields fail closed.
func Read(data []byte) (Document, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Document{}, &ReadError{Code: "document.empty", Message: "StackSpec document is empty"}
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return Document{}, &ReadError{Code: "document.invalid-yaml", Message: fmt.Sprintf("invalid StackSpec document: %v", err)}
	}

	fields, err := topLevelFields(&root)
	if err != nil {
		return Document{}, err
	}

	apiVersion := scalarValue(fields["apiVersion"])
	kind := scalarValue(fields["kind"])
	rawCopy := bytes.Clone(data)

	switch apiVersion {
	case "", APIVersionV1:
		if apiVersion == "" && kind != "" {
			return Document{}, &ReadError{
				Code:    "document.partial-version-header",
				Message: "StackSpec kind is present without apiVersion; refusing to guess v1 or v2",
			}
		}
		if apiVersion == APIVersionV1 && kind != "" && kind != KindStackSpec {
			return Document{}, &ReadError{
				Code:    "document.invalid-kind",
				Message: fmt.Sprintf("apiVersion %q requires kind %q when kind is present", APIVersionV1, KindStackSpec),
			}
		}
		if mixed := v2OnlyFields(fields); len(mixed) > 0 {
			return Document{}, &ReadError{
				Code: "document.mixed-version-fields",
				Message: fmt.Sprintf(
					"v1 StackSpec contains v2-only top-level fields %s; refusing to discard or reinterpret mixed-version intent",
					strings.Join(mixed, ", "),
				),
			}
		}

		var spec models.StackSpec
		if err := yaml.Unmarshal(data, &spec); err != nil {
			return Document{}, &ReadError{Code: "v1.decode-failed", Message: fmt.Sprintf("cannot decode v1 StackSpec: %v", err)}
		}
		return Document{
			Version: SourceVersionV1, Raw: rawCopy, Legacy: &spec,
			UnknownV1Fields: unknownV1Fields(fields),
		}, nil

	case APIVersionV2Alpha1:
		if kind != KindStackSpec {
			return Document{}, &ReadError{
				Code:    "document.invalid-kind",
				Message: fmt.Sprintf("apiVersion %q requires kind %q", APIVersionV2Alpha1, KindStackSpec),
			}
		}
		if _, exists := fields["context"]; exists {
			return Document{}, &ReadError{
				Code:    "v2.legacy-context-forbidden",
				Message: "canonical v2 StackSpec must not contain top-level context",
			}
		}
		if _, exists := fields["stackkit"]; exists {
			return Document{}, &ReadError{
				Code:    "v2.legacy-kit-field-forbidden",
				Message: "canonical v2 StackSpec must use kit.slug, not top-level stackkit",
			}
		}

		kitNode := fields["kit"]
		kitFields, err := mappingFields(kitNode, "v2 kit")
		if err != nil {
			return Document{}, err
		}
		profile := KitProfile(strings.TrimSpace(scalarValue(kitFields["slug"])))
		if !isCanonicalKitProfile(profile) {
			return Document{}, &ReadError{
				Code:    "v2.invalid-kit-profile",
				Message: fmt.Sprintf("v2 kit.slug %q is not a canonical KitProfile", profile),
			}
		}

		return Document{
			Version: SourceVersionV2Alpha1,
			Raw:     rawCopy,
			V2: &V2Identity{
				APIVersion: apiVersion,
				Kind:       kind,
				KitProfile: profile,
			},
		}, nil

	default:
		return Document{}, &ReadError{
			Code:    "document.unsupported-api-version",
			Message: fmt.Sprintf("unsupported StackSpec apiVersion %q", apiVersion),
		}
	}
}

func unknownV1Fields(fields map[string]*yaml.Node) []string {
	known := map[string]struct{}{
		"apiVersion": {},
		"kind":       {},
	}
	typeOfSpec := reflect.TypeOf(models.StackSpec{})
	for index := 0; index < typeOfSpec.NumField(); index++ {
		name := strings.Split(typeOfSpec.Field(index).Tag.Get("yaml"), ",")[0]
		if name != "" && name != "-" {
			known[name] = struct{}{}
		}
	}
	var unknown []string
	for name := range fields {
		if _, exists := known[name]; !exists {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	return unknown
}

func v2OnlyFields(fields map[string]*yaml.Node) []string {
	var mixed []string
	for _, name := range []string{
		"access",
		"availability",
		"bridge",
		"capabilities",
		"controlPlane",
		"data",
		"deviceEnrollment",
		"generation",
		"install",
		"kit",
		"modules",
		"partitionPolicy",
		"routes",
		"sites",
		"source",
		"workloads",
	} {
		if _, exists := fields[name]; exists {
			mixed = append(mixed, name)
		}
	}
	sort.Strings(mixed)
	return mixed
}

func topLevelFields(root *yaml.Node) (map[string]*yaml.Node, error) {
	if root == nil || len(root.Content) != 1 {
		return nil, &ReadError{Code: "document.invalid-root", Message: "StackSpec document must contain exactly one root value"}
	}
	return mappingFields(root.Content[0], "StackSpec root")
}

func mappingFields(node *yaml.Node, name string) (map[string]*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, &ReadError{Code: "document.invalid-shape", Message: name + " must be a mapping"}
	}

	fields := make(map[string]*yaml.Node, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if _, exists := fields[key]; exists {
			return nil, &ReadError{Code: "document.duplicate-field", Message: fmt.Sprintf("%s contains duplicate field %q", name, key)}
		}
		fields[key] = node.Content[i+1]
	}
	return fields, nil
}

func scalarValue(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}
