package localevidence

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/kombifyio/stackkits/internal/applyevidencev2"
	"github.com/kombifyio/stackkits/internal/hostconformance"
)

// HostObserver answers `host` requirements from the StackKits host probe. It
// reuses internal/hostconformance rather than re-inspecting the machine, so
// local Apply evidence and `stackkit host conformance` describe the same host
// through the same code path.
type HostObserver struct {
	Probe hostconformance.Probe
}

// NewHostObserver binds an observer to a host probe.
func NewHostObserver(probe hostconformance.Probe) (*HostObserver, error) {
	if probe == nil {
		return nil, errors.New("localevidence: host observer requires a probe")
	}
	return &HostObserver{Probe: probe}, nil
}

// Observe implements Observer. Every returned fact is something the probe
// actually reported; nothing is defaulted or inferred. Apply evidence always
// claims "satisfied", so every contributing check must have positively passed
// before this observer returns facts to the signer.
func (o *HostObserver) Observe(ctx context.Context, expectation applyevidence.Expectation) (map[string]string, error) {
	if o == nil || o.Probe == nil {
		return nil, errors.New("localevidence: host observer is not configured")
	}
	observation, err := o.Probe.Observe(ctx)
	if err != nil {
		return nil, fmt.Errorf("probe host: %w", err)
	}

	facts := map[string]string{
		"subject.ownerKind":      expectation.Subject.OwnerKind,
		"subject.ownerRef":       expectation.Subject.OwnerRef,
		"os.family":              observation.Facts.OS.Family,
		"os.distribution":        observation.Facts.OS.Distribution,
		"os.version":             observation.Facts.OS.Version,
		"architecture":           observation.Facts.Architecture,
		"kernel.release":         observation.Facts.KernelRelease,
		"runtime.engine":         observation.Facts.Runtime.Engine,
		"runtime.version":        observation.Facts.Runtime.Version,
		"virtualization.class":   observation.Facts.Virtualization.Class,
		"virtualization.nested":  strconv.FormatBool(observation.Facts.Virtualization.Nested),
		"observation.checkCount": strconv.Itoa(len(observation.Checks)),
	}
	if expectation.Subject.NodeRef != "" {
		facts["subject.nodeRef"] = expectation.Subject.NodeRef
	}
	for _, check := range observation.Checks {
		if check.Status != "pass" {
			return nil, fmt.Errorf(
				"host check %q is not verified (status %q); refusing satisfied Apply evidence",
				check.ID,
				check.Status,
			)
		}
		facts["check."+check.ID+".status"] = check.Status
	}

	// An empty fact set would be signed as a meaningless observation; a probe
	// that reports nothing identifiable about the host is a failure, not a pass.
	if observation.Facts.OS.Family == "" && observation.Facts.Architecture == "" && observation.Facts.KernelRelease == "" {
		return nil, errors.New("host probe reported no identifying facts")
	}
	return facts, nil
}
