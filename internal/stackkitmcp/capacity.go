package stackkitmcp

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The server validates these documents against its embedded CUE authority.
// Callers supply intent and observations, never compute profiles or a Plan.
type capacityAssessmentInput struct {
	StackSpec map[string]any `json:"stackSpec" jsonschema:"canonical StackSpec intent"`
	Inventory map[string]any `json:"inventory,omitempty" jsonschema:"optional observed Inventory document"`
}

func (a *App) assessCapacity(ctx context.Context, _ *mcp.CallToolRequest, in capacityAssessmentInput) (*mcp.CallToolResult, any, error) {
	return a.serverRequest(ctx, http.MethodPost, "/api/v2/assess-capacity", in)
}
