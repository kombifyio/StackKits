package architecturev2renderer

import (
	"bytes"
	"strings"

	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

func renderKopiaSourceVolumeBinds(unit RenderUnit, compose []byte) []byte {
	source, err := kopiaSourceForComposeUnit(unit)
	if err != nil {
		return compose
	}
	return injectKopiaSourceVolumeBinds(compose, source)
}

func kopiaSourceForComposeUnit(unit RenderUnit) (localbackuppolicy.Source, error) {
	if emptyJSONObject(unit.ValuesJSON()) {
		source, err := localbackuppolicy.GovernedSourceForCoreModule(unit.ModuleID())
		if err != nil {
			return localbackuppolicy.GovernedSource(), nil
		}
		return source, nil
	}
	var values localKopiaRuntimeValues
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return localbackuppolicy.Source{}, err
	}
	source := values.BackupSource.Source
	if err := localbackuppolicy.ValidateSourceProjection(source); err != nil {
		return localbackuppolicy.Source{}, err
	}
	return source, nil
}

func kopiaSourceVolumeBind(source localbackuppolicy.Source, volumeName string) string {
	return source.HostPath + "/" + volumeName + "/_data:" + source.ContainerPath + "/" + volumeName + "/_data:ro"
}

func injectKopiaSourceVolumeBinds(compose []byte, source localbackuppolicy.Source) []byte {
	if len(source.ManagedVolumeNames) == 0 {
		return compose
	}
	lines := bytes.Split(compose, []byte("\n"))
	out := make([][]byte, 0, len(lines)+len(source.ManagedVolumeNames))
	replaced := false
	for _, line := range lines {
		text := string(line)
		if isKopiaHostPathBindLine(text, source) {
			if !replaced {
				indent := leadingSpaces(text)
				for _, name := range source.ManagedVolumeNames {
					out = append(out, []byte(indent+"- "+kopiaSourceVolumeBind(source, name)))
				}
				replaced = true
			}
			continue
		}
		if !replaced && strings.Contains(strings.TrimSpace(text), "kopia-repository:") {
			indent := leadingSpaces(text)
			for _, name := range source.ManagedVolumeNames {
				out = append(out, []byte(indent+"- "+kopiaSourceVolumeBind(source, name)))
			}
			replaced = true
		}
		out = append(out, line)
	}
	return bytes.Join(out, []byte("\n"))
}

func isKopiaHostPathBindLine(text string, source localbackuppolicy.Source) bool {
	trimmed := strings.TrimSpace(text)
	prefix := "- " + source.HostPath + "/"
	return strings.HasPrefix(trimmed, prefix) &&
		strings.Contains(trimmed, "/_data:"+source.ContainerPath+"/") &&
		strings.HasSuffix(trimmed, "/_data:ro")
}

func extraKopiaSourceVolumeNames(content []byte, core localbackuppolicy.Source) ([]string, bool) {
	coreNames := make(map[string]struct{}, len(core.ManagedVolumeNames))
	for _, name := range core.ManagedVolumeNames {
		coreNames[name] = struct{}{}
	}
	present := map[string]struct{}{}
	var extra []string
	for _, line := range bytes.Split(content, []byte("\n")) {
		text := string(line)
		if !isKopiaHostPathBindLine(text, core) {
			continue
		}
		name, ok := parseKopiaHostPathVolumeName(strings.TrimSpace(text), core)
		if !ok {
			return nil, false
		}
		if _, known := coreNames[name]; known {
			present[name] = struct{}{}
			continue
		}
		if !localbackuppolicy.ValidComposeVolumeName(name) {
			return nil, false
		}
		extra = append(extra, name)
	}
	for _, name := range core.ManagedVolumeNames {
		if _, ok := present[name]; !ok {
			return nil, false
		}
	}
	return extra, true
}

func parseKopiaHostPathVolumeName(trimmed string, source localbackuppolicy.Source) (string, bool) {
	prefix := "- " + source.HostPath + "/"
	suffix := "/_data:" + source.ContainerPath + "/"
	if !strings.HasPrefix(trimmed, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(trimmed, prefix)
	hostEnd := strings.Index(rest, "/_data:")
	if hostEnd <= 0 {
		return "", false
	}
	name := rest[:hostEnd]
	want := prefix + name + suffix + name + "/_data:ro"
	if trimmed != want {
		return "", false
	}
	return name, true
}

func alignKopiaSourceVolumeBinds(content, expected []byte, core localbackuppolicy.Source) []byte {
	extras, ok := extraKopiaSourceVolumeNames(content, core)
	if !ok {
		return nil
	}
	if len(extras) == 0 {
		return expected
	}
	extended := core
	extended.ManagedVolumeNames = append(append([]string{}, core.ManagedVolumeNames...), extras...)
	return injectKopiaSourceVolumeBinds(expected, extended)
}

func leadingSpaces(text string) string {
	return text[:len(text)-len(strings.TrimLeft(text, " \t"))]
}

func validateClosedLocalCoreBackupSourceInputs(unit RenderUnit, path, displayName, moduleID string) error {
	if len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) {
		return fail(ErrInvalidPlan, path+".inputs", "%s consumes no secret material; Apply supplies local custody out of band", displayName)
	}
	if len(unit.PlanInputRefs()) != 0 || !emptyJSONObject(unit.PlanInputsJSON()) {
		return fail(ErrInvalidPlan, path+".inputs", "%s consumes no caller plan material; backup source is compiler-owned", displayName)
	}
	if len(unit.PublicInputRefs()) == 0 && emptyJSONObject(unit.ValuesJSON()) && emptyJSONArray(unit.InputBindingsJSON()) {
		return nil
	}
	if !exactStringList(unit.PublicInputRefs(), localKopiaRuntimePublicInputRefs) {
		return fail(ErrInvalidPlan, path+".inputs", "%s may bind only the compiler-owned backup source", displayName)
	}
	if err := validateLocalKopiaRuntimeBinding(unit.InputBindingsJSON(), path+".inputBindings"); err != nil {
		return err
	}
	var values localKopiaRuntimeValues
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return wrap(ErrInvalidPlan, path+".values", "decode compiler-owned backup source", err)
	}
	source := values.BackupSource.Source
	if err := localbackuppolicy.ValidateSourceProjection(source); err != nil {
		return wrap(ErrInvalidPlan, path+".values.backup-source", "validate compiler-owned backup source", err)
	}
	if source.CoreModuleRef != "" && source.CoreModuleRef != moduleID {
		return fail(ErrInvalidPlan, path+".values.backup-source.coreModuleRef", "must bind the selected %s runtime", displayName)
	}
	return nil
}
