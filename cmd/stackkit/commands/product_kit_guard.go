package commands

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/productkits"
	"github.com/kombifyio/stackkits/pkg/models"
)

// requireRuntimeProductStackKit is the last common guard before generate or
// apply can enter a product renderer/executor. Migration readers deliberately
// do not call it; they may inspect a historical identifier but cannot execute
// that identifier as a product.
func requireRuntimeProductStackKit(spec *models.StackSpec) error {
	if spec == nil {
		return fmt.Errorf("stack spec is required")
	}
	return productkits.Validate(spec.StackKit)
}
