package localevidence

import (
	"github.com/kombifyio/stackkits/internal/actionableerror"
	"strings"
)

func CloudSessionRoutingDiagnostic(err error) error {
	if err == nil {
		return nil
	}
	reason, _, _ := strings.Cut(err.Error(), ":")
	if !strings.HasPrefix(reason, "cloud_session_") {
		return err
	}
	return actionableerror.WithDiagnostic(reason, err)
}
