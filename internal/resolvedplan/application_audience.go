package resolvedplan

import (
	"reflect"

	"github.com/kombifyio/stackkits/internal/householdapps"
)

func resolveApplicationAudience(policy map[string]any, path string) (map[string]any, bool, error) {
	raw, exists := policy["audience"]
	if !exists {
		return nil, false, nil
	}
	value, err := asObject(raw, path+".audience")
	if err != nil {
		return nil, true, err
	}
	schema, schemaOK := value["schema"].(string)
	members, membersOK := value["members"].(string)
	if len(value) != 2 || !schemaOK || !membersOK {
		return nil, true, fail(ErrProfileMismatch, path+".audience", "requires only schema and members")
	}
	if _, err := (householdapps.Audience{Schema: schema, Members: members}).Groups(); err != nil {
		return nil, true, fail(ErrProfileMismatch, path+".audience", "%v", err)
	}
	return map[string]any{"schema": schema, "members": members}, true, nil
}

// One application cannot keep a broader alias, path, module or publication.
// Absence on even one sibling route is a conflict with an explicit audience.
func validateApplicationAudiences(routes []any) error {
	type decision struct {
		audience any
		module   any
	}
	seen := map[string]decision{}
	for _, raw := range routes {
		route := raw.(map[string]any)
		service, _ := route["serviceRef"].(string)
		access, _ := route["access"].(map[string]any)
		next := decision{access["audience"], route["moduleRef"]}
		if prior, exists := seen[service]; exists && (prior.audience != nil || next.audience != nil) && !reflect.DeepEqual(prior, next) {
			return fail(ErrProfileMismatch, "network.routes", "application %q has conflicting audiences or runtime owners", service)
		}
		seen[service] = next
	}
	return nil
}
