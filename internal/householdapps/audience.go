package householdapps

import "fmt"

// Audience is an explicit application-wide decision. Absence keeps the
// established member app grants; v1 admits local Owner or household access.
type Audience struct {
	Schema  string `json:"schema"`
	Members string `json:"members"`
}

const AudienceSchema = "stackkit.application-audience/v1"

// Groups uses the Homelab authority's existing groups. An explicit owner
// audience never admits a separate admin account. Household is every current
// household identity, including people added after the plan was generated.
func (a Audience) Groups() ([]string, error) {
	if a.Schema != AudienceSchema {
		return nil, fmt.Errorf("unsupported application audience schema %q", a.Schema)
	}
	switch a.Members {
	case "owner":
		return []string{"owners"}, nil
	case "household":
		return []string{"owners", "household"}, nil
	default:
		return nil, fmt.Errorf("unsupported application audience %q", a.Members)
	}
}
