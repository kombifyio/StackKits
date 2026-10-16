package architecturev2renderer

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func nativeResourceName(kind, key string) string {
	return kind + "_" + nativeResourceNamePattern.ReplaceAllString(key, "_")
}

func appendNativeLabels(block *hclwrite.Body, labels map[string]string) {
	for _, key := range sortedKeys(labels) {
		label := block.AppendNewBlock("labels", nil).Body()
		label.SetAttributeValue("label", cty.StringVal(key))
		label.SetAttributeValue("value", cty.StringVal(labels[key]))
	}
}

// nativeProjectPathTokens is abspath("${path.module}/../<rel>"): a file in
// the workload project directory beside compose.yaml.
func nativeProjectPathTokens(rel string) hclwrite.Tokens {
	return hclwrite.TokensForFunctionCall("abspath", nativeProjectTemplateTokens(rel))
}

func nativeFileDigestTokens(rel string) hclwrite.Tokens {
	return hclwrite.TokensForFunctionCall("filesha256", nativeProjectTemplateTokens(rel))
}

func nativeProjectTemplateTokens(rel string) hclwrite.Tokens {
	return hclwrite.Tokens{
		{Type: hclsyntax.TokenOQuote, Bytes: []byte(`"`)},
		{Type: hclsyntax.TokenTemplateInterp, Bytes: []byte(`${`)},
		{Type: hclsyntax.TokenIdent, Bytes: []byte(`path.module`)},
		{Type: hclsyntax.TokenTemplateSeqEnd, Bytes: []byte(`}`)},
		{Type: hclsyntax.TokenQuotedLit, Bytes: []byte("/../" + escapeHCLTemplateLiteral(rel))},
		{Type: hclsyntax.TokenCQuote, Bytes: []byte(`"`)},
	}
}

// appendNativeUpload renders the provider upload of one secret file: the
// owner-only source beside compose.yaml is copied into the container with
// its owner and mode; the root and state hold its path and digest only.
func appendNativeUpload(block *hclwrite.Body, file NativeDockerSecretFile) {
	upload := block.AppendNewBlock("upload", nil).Body()
	upload.SetAttributeValue("file", cty.StringVal(file.Target))
	upload.SetAttributeRaw("source", nativeProjectPathTokens(file.RelPath))
	upload.SetAttributeRaw("source_hash", nativeFileDigestTokens(file.RelPath))
	upload.SetAttributeValue("owner", cty.StringVal(file.Owner))
	upload.SetAttributeValue("group", cty.StringVal(file.Group))
	upload.SetAttributeValue("permissions", cty.StringVal(fmt.Sprintf("%04o", file.Permissions)))
}

func appendNativeBindMount(block *hclwrite.Body, rel, target string) {
	mounts := block.AppendNewBlock("mounts", nil).Body()
	mounts.SetAttributeValue("type", cty.StringVal("bind"))
	mounts.SetAttributeRaw("source", nativeProjectPathTokens(rel))
	mounts.SetAttributeValue("target", cty.StringVal(target))
	mounts.SetAttributeValue("read_only", cty.True)
}

func nativeMemoryMegabytes(value string) (int64, error) {
	match := nativeMemoryPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, fmt.Errorf("unsupported memory value %q", value)
	}
	amount, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, err
	}
	if match[2] == "g" {
		amount *= 1024
	}
	return amount, nil
}

// unescapeComposeLiteral undoes the renderer's `$` → `$$` escape; any other
// `$` would be a Compose interpolation and is refused.
func unescapeComposeLiteral(value string) (string, error) {
	var out strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '$' {
			out.WriteByte(value[index])
			continue
		}
		if index+1 < len(value) && value[index+1] == '$' {
			out.WriteByte('$')
			index++
			continue
		}
		return "", fmt.Errorf("unescaped interpolation")
	}
	return out.String(), nil
}
