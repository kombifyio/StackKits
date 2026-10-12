package backupexec

import (
	"context"
	"fmt"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"reflect"
	"strings"
)

// NewV2EngineForPolicy is the same typed policy binding used by Docker. An
// explicit executor also admits narrow real Kopia probes without a new owner.
func NewV2EngineForPolicy(exec SecretExecutor, policy localbackuppolicy.Policy) (V2Engine, error) {
	raw, err := localbackuppolicy.ArtifactBytes(policy)
	if err != nil {
		return V2Engine{}, err
	}
	held, err := localbackuppolicy.Decode(raw)
	if err != nil {
		return V2Engine{}, err
	}
	return newV2EngineForHeldPolicy(exec, held)
}

func newV2EngineForHeldPolicy(exec SecretExecutor, held localbackuppolicy.Policy) (V2Engine, error) {
	sources, err := held.SnapshotSources()
	if err != nil {
		return V2Engine{}, err
	}
	engine := NewV2Engine(exec)
	engine.policy = &held
	engine.sources = sources
	engine.retention = held.Retention
	engine.hostname = held.Runtime.Hostname
	engine.coreModuleRef = held.Source.CoreModuleRef
	return engine, nil
}

func (e V2Engine) withSnapshotSource(source localbackuppolicy.SnapshotSource) (V2Engine, error) {
	if e.policy == nil {
		return V2Engine{}, fmt.Errorf("class source requires the held governed policy")
	}
	if err := e.policy.ValidateSnapshotSourceSelection([]localbackuppolicy.SnapshotSource{source}); err != nil {
		return V2Engine{}, err
	}
	retention, err := e.policy.RetentionForDataClass(source.DataClass)
	if err != nil {
		return V2Engine{}, err
	}
	result := e
	result.retention = retention
	result.selection = &source
	result.sources = nil
	return result, nil
}

func (e V2Engine) validateClassRoot(source string, excludes []string) error {
	if e.policy == nil || source != e.policy.Source.ContainerPath || !reflect.DeepEqual(excludes, e.policy.Source.ExcludePaths) {
		return fmt.Errorf("class source request differs from the held root policy")
	}
	return nil
}

func (e V2Engine) configureClassSources(ctx context.Context, root string, excludes []string, password []byte) error {
	if err := e.validateClassRoot(root, excludes); err != nil {
		return err
	}
	for _, source := range e.sources {
		member, err := e.withSnapshotSource(source)
		if err != nil {
			return err
		}
		if err := member.ConfigureSourcePolicy(ctx, source.Path, source.ExcludePaths, password); err != nil {
			return fmt.Errorf("configure retention class %s for %s: %w", source.DataClass, source.VolumeName, err)
		}
	}
	return nil
}

func (e V2Engine) classSourcePolicy(ctx context.Context, root string, excludes []string, password []byte) (SourcePolicy, error) {
	if err := e.validateClassRoot(root, excludes); err != nil {
		return SourcePolicy{}, err
	}
	result := SourcePolicy{Source: root, ExcludePaths: append([]string(nil), excludes...), Exact: true}
	for _, source := range e.sources {
		member, err := e.withSnapshotSource(source)
		if err != nil {
			return SourcePolicy{}, err
		}
		observed, err := member.SourcePolicy(ctx, source.Path, source.ExcludePaths, password)
		if err != nil {
			return SourcePolicy{}, fmt.Errorf("observe retention class %s for %s: %w", source.DataClass, source.VolumeName, err)
		}
		result.Exact = result.Exact && observed.Exact
		result.Sources = append(result.Sources, observed)
	}
	return result, nil
}

func selectedRelativeExcludes(selection localbackuppolicy.SnapshotSource, source string, excludes []string) ([]string, error) {
	if source != selection.Path || !reflect.DeepEqual(excludes, selection.ExcludePaths) {
		return nil, fmt.Errorf("volume source or exclusions differ from the held class selection")
	}
	relative := make([]string, 0, len(excludes))
	for _, exclude := range excludes {
		clean, err := cleanAbsoluteContainerPath(exclude)
		if err != nil || !strings.HasPrefix(clean, source+"/") {
			return nil, fmt.Errorf("class exclusion is outside its selected volume")
		}
		relative = append(relative, strings.TrimPrefix(clean, source+"/"))
	}
	return relative, nil
}
