package architecturev2renderer

import (
	"encoding/json"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/runtimeprofile"
)

func LocalNetworkForPlan(plan generationartifact.VerifiedPlan) (*runtimeprofile.LocalNetwork, error) {
	top, err := decodeRawObject(plan.Canonical(), "resolvedPlan")
	if err != nil {
		return nil, err
	}
	profile, _, err := parseDataProfile(top)
	if err != nil {
		return nil, err
	}
	return parseLocalNetwork(top, profile)
}

func parseDataProfile(top map[string]json.RawMessage) (runtimeprofile.DataProfile, *runtimeprofile.OfflineBundleRef, error) {
	value := ""
	if raw, exists := top["dataProfile"]; exists {
		if err := json.Unmarshal(raw, &value); err != nil || value == "" {
			return "", nil, fail(ErrInvalidPlan, "resolvedPlan.dataProfile", "requires a canonical profile string")
		}
	}
	profile, err := runtimeprofile.Parse(value)
	if err != nil {
		return "", nil, wrap(ErrInvalidPlan, "resolvedPlan.dataProfile", "invalid profile", err)
	}
	if profile.FullLocal() {
		for key, expected := range map[string]string{"edition": "selfhost-oss", "loc": "local"} {
			actual, err := requiredRawString(top, key, "resolvedPlan."+key)
			if err != nil {
				return "", nil, err
			}
			if actual != expected {
				return "", nil, fail(ErrInvalidPlan, "resolvedPlan."+key, "Full Local requires %s", expected)
			}
		}
	}
	var bundle *runtimeprofile.OfflineBundleRef
	if raw, exists := top["offlineBundle"]; exists {
		if !profile.FullLocal() {
			return "", nil, fail(ErrInvalidPlan, "resolvedPlan.offlineBundle", "requires Full Local")
		}
		bundle = &runtimeprofile.OfflineBundleRef{}
		if err := decodeStrict(raw, bundle); err != nil {
			return "", nil, wrap(ErrInvalidPlan, "resolvedPlan.offlineBundle", "invalid bundle reference", err)
		}
		if err := bundle.Validate(); err != nil {
			return "", nil, wrap(ErrInvalidPlan, "resolvedPlan.offlineBundle", "invalid bundle reference", err)
		}
	}
	return profile, bundle, nil
}

func parseLocalNetwork(top map[string]json.RawMessage, profile runtimeprofile.DataProfile) (*runtimeprofile.LocalNetwork, error) {
	if !profile.FullLocal() {
		return nil, nil
	}
	stackID, err := requiredRawString(top, "stackId", "resolvedPlan.stackId")
	if err != nil {
		return nil, err
	}
	var network struct {
		Configuration struct {
			DNS struct {
				Servers []string `json:"servers"`
			} `json:"dns"`
		} `json:"configuration"`
		RuntimeListeners []struct {
			ComponentRef string `json:"componentRef"`
			BindAddress  string `json:"bindAddress"`
			Port         int    `json:"port"`
		} `json:"runtimeListeners"`
	}
	if err := json.Unmarshal(top["network"], &network); err != nil {
		return nil, err
	}
	policy := &runtimeprofile.LocalNetwork{StackID: stackID, Upstream: network.Configuration.DNS.Servers}
	for _, listener := range network.RuntimeListeners {
		if listener.ComponentRef == "lan-dns" && listener.Port == 53 {
			policy.Resolver = listener.BindAddress
		}
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return policy, nil
}
