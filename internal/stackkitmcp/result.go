package stackkitmcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/logging"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TextResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: logging.RedactText(text)}}}
}

func JSONResult(value any) *mcp.CallToolResult {
	value = logging.RedactValue(value)
	raw, _ := json.MarshalIndent(value, "", "  ")
	result := TextResult(string(raw))
	result.StructuredContent = value
	return result
}

func StringMapValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func ShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
