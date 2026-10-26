// Package runtimeprofile connects each StackKits entry point to the shared
// self-hosted data-profile policy. Locality policy remains in go-common.
package runtimeprofile

import (
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/dataprofilev1"
)

// DataProfile is the persisted StackSpec selection carried by a verified plan.
// Its zero value is the backward-compatible Standard profile.
type DataProfile string

const (
	StandardProfile  DataProfile = "standard"
	FullLocalProfile DataProfile = dataprofile.FullLocal
)

func (p DataProfile) FullLocal() bool { return p == FullLocalProfile }

// Parse accepts canonical intent only; an absent field retains Standard.
func Parse(value string) (DataProfile, error) {
	switch DataProfile(value) {
	case "", StandardProfile:
		return StandardProfile, nil
	case FullLocalProfile:
		return FullLocalProfile, nil
	default:
		return "", fmt.Errorf("data_profile_unresolvable: unknown StackSpec dataProfile %q", value)
	}
}

// FromBootEnv validates the shared edition/profile policy for initial authoring.
func FromBootEnv() (DataProfile, error) {
	profile, err := dataprofile.FromBootEnv()
	if err != nil {
		return "", err
	}
	if profile.Loc == dataprofile.LocalityLocal {
		return FullLocalProfile, nil
	}
	return StandardProfile, nil
}

// AdmitSpec binds native lifecycle admission to the persisted selection. It
// never repairs a mismatch by changing the environment or rewriting intent.
func AdmitSpec(value string) error {
	intent, err := Parse(value)
	if err != nil {
		return err
	}
	boot, err := FromBootEnv()
	if err != nil {
		return err
	}
	if intent != boot {
		return fmt.Errorf("data_profile_mismatch: StackSpec dataProfile=%s disagrees with boot dataProfile=%s; restart with the matching %s and KOMBIFY_EDITION before operating this stack", intent, boot, dataprofile.BootEnvVar)
	}
	return nil
}

// Boot validates the edition/profile before any runtime work and installs the
// shared HTTP/DNS guard. Custom transports and subprocesses retain their own
// admitted local destinations; this does not claim an operating-system firewall.
func Boot() error {
	profile, err := dataprofile.FromBootEnv()
	if err != nil {
		return err
	}
	return profile.EnforceEgress()
}

// FullLocal is queried only after Boot has validated the process environment.
func FullLocal() bool {
	return strings.TrimSpace(os.Getenv(dataprofile.BootEnvVar)) == dataprofile.FullLocal
}
