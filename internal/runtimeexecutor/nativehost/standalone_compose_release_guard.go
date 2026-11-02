package nativehost

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/applicationrelease"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

const (
	applicationReleaseDowngradeUnrestored = "application_release_downgrade_unrestored"
	applicationReleaseUnknown             = "application_release_unknown"
	applicationReleaseOperationOpen       = "application_release_operation_open"
)

// ApplicationReleaseSafetyError refuses an Apply that would start an earlier
// release of an application on data a later release may already have migrated.
type ApplicationReleaseSafetyError struct {
	reason, workload, from, to string
	// choice reports a workload whose catalog admits a release selection.
	choice bool
}

func (e *ApplicationReleaseSafetyError) Error() string {
	if e.reason == applicationReleaseOperationOpen {
		return "Apply refused " + e.workload + " " + e.to + " while release operation " + e.from + " governs it"
	}
	if e.reason == applicationReleaseDowngradeUnrestored {
		return "Apply refused to start " + e.workload + " " + e.to + " on data of " + e.from + " without the scoped release rollback"
	}
	return "Apply cannot establish the release of the existing " + e.workload + " data"
}

func (e *ApplicationReleaseSafetyError) ActionableError() actionableerror.Contract {
	if e.reason == applicationReleaseOperationOpen {
		return actionableerror.New("stackkit_command_denied", e.reason,
			"Release operation "+e.from+" governs "+e.workload+"; Apply starts only the release it expects.",
			[]string{
				"Read it: stackkit use-cases upgrade --status " + e.from + ".",
				"Complete it (generate and apply its selected release), roll it back (--rollback), resume it (--operation-id) or finalize it (--finalize), then change the release.",
			}, false)
	}
	if e.reason == applicationReleaseDowngradeUnrestored {
		guidance := []string{"Keep " + e.from + "; an earlier release opens this data only after a restore of data taken under that release."}
		if e.choice {
			guidance = []string{
				"Return to the earlier release with its data: stackkit use-cases upgrade --rollback <operation-id> --owner-approve, then stackkit generate and stackkit apply.",
				"To stay on " + e.from + ", select it again (stackkit use-cases configure " + e.workload + " --setting release=\"" + e.from + "\").",
			}
		}
		return actionableerror.New("stackkit_command_denied", e.reason,
			"StackKits blocked an earlier "+e.workload+" release before changing runtime files or containers. The application keeps "+e.from+".",
			guidance, false)
	}
	return actionableerror.New("stackkit_command_denied", e.reason,
		"StackKits found existing "+e.workload+" runtime custody but cannot read its release.",
		[]string{"Restore the matching StackKits workspace and runtime custody for this application, then retry."}, false)
}

// requireApplicationReleaseTransition keeps an earlier release from opening
// data a later release may have migrated. While a scoped release operation is
// open for the project, Apply starts only the release that operation expects:
// the target of a completed checkpoint, the prior release after its data was
// restored, and nothing while a checkpoint or rollback is incomplete. Without
// an open operation any move of applied runtime custody to an earlier release
// is refused, and a release-choice workload with data but no custody is refused.
func (o *osStandaloneComposeWorkloadOperations) requireApplicationReleaseTransition(
	ctx context.Context,
	bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor,
) error {
	unknown := &ApplicationReleaseSafetyError{reason: applicationReleaseUnknown, workload: bundle.WorkloadRef}
	target, ok := standaloneComposeComponent(bundle.Components, bundle.EntryComponent)
	if !ok {
		return unknown
	}
	to := componentImageTag(target.ImageRef)
	project := "stackkit-" + bundle.WorkloadRef + "-" + bundle.NodeRef
	// Only a release-choice workload has release operations to consult.
	choice := len(architecturev2renderer.AdmittedWorkloadReleases(bundle.ModuleRef)) > 1
	record, open := applicationrelease.Record{}, false
	if choice {
		var err error
		if record, open, err = applicationrelease.OpenFor(o.workspaceRoot, bundle.WorkloadRef); err != nil {
			return unknown
		}
	}
	if open && record.ComposeProject == project {
		expected := ""
		switch record.State {
		case applicationrelease.StateCheckpointed:
			expected = record.TargetRelease
		case applicationrelease.StateRolledBack:
			expected = record.PriorRelease
		}
		if expected == "" || to != expected {
			return &ApplicationReleaseSafetyError{reason: applicationReleaseOperationOpen, workload: bundle.WorkloadRef, from: record.OperationID, to: to}
		}
		return nil
	}
	image, persisted, err := PersistedApplicationEntryImage(o.workspaceRoot, project, bundle.EntryComponent)
	if err != nil {
		return unknown
	}
	if !persisted {
		if !choice {
			return nil
		}
		raw, err := o.runner.Run(ctx, applicationReleaseVolumeDiscoveryArgs(project), o.workspaceRoot)
		if err != nil || len(bytes.TrimSpace(raw)) > 0 {
			return unknown
		}
		return nil
	}
	ref, _, _ := strings.Cut(image, "@")
	from := componentImageTag(ref)
	if later, comparable := releaseIsLater(from, to); comparable && later {
		return &ApplicationReleaseSafetyError{reason: applicationReleaseDowngradeUnrestored, workload: bundle.WorkloadRef, from: from, to: to, choice: choice}
	}
	return nil
}

// releaseIsLater orders two release tags as semantic versions; comparable is
// false when either tag is not one.
func releaseIsLater(candidate, current string) (later, comparable bool) {
	a, b := "v"+strings.TrimPrefix(candidate, "v"), "v"+strings.TrimPrefix(current, "v")
	if !semver.IsValid(a) || !semver.IsValid(b) {
		return false, false
	}
	return semver.Compare(a, b) > 0, true
}

func componentImageTag(ref string) string {
	separator := strings.LastIndex(ref, ":")
	if separator <= strings.LastIndex(ref, "/") {
		return ""
	}
	return ref[separator+1:]
}

// PersistedApplicationEntryImage reads the entry image of one applied
// standalone Compose project; found is false before its first Apply.
func PersistedApplicationEntryImage(workspaceRoot, project, entry string) (string, bool, error) {
	return readPersistedEntryImage(filepath.Join(workspaceRoot, ".stackkit", "runtime", "applications", project), project, entry)
}

// readPersistedEntryImage reads the entry service image of the project's
// persisted Compose custody; persisted is false for a project never applied.
func readPersistedEntryImage(directory, project, entry string) (string, bool, error) {
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false, errors.New("application runtime custody is unavailable")
	}
	if standaloneComposeRuntimeCustodyAbsent(directory) {
		return "", false, nil
	}
	root, err := confinedfs.Open(directory)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = root.Close() }()
	tx, err := root.BeginTransaction()
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Close() }()
	compose, composeInfo, err := tx.ReadStableBounded("compose.yaml", standaloneComposeOutputMax)
	if err != nil || composeInfo == nil || !composeInfo.Mode().IsRegular() {
		return "", false, errors.New("application Compose custody is unavailable")
	}
	var document standaloneComposeDocument
	decoder := yaml.NewDecoder(bytes.NewReader(compose))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil || document.Name != project {
		return "", false, errors.New("application Compose custody differs from its project identity")
	}
	service, ok := document.Services[entry]
	if !ok || service.Image == "" {
		return "", false, errors.New("application Compose custody lacks its entry service")
	}
	return service.Image, true, nil
}

func applicationReleaseVolumeDiscoveryArgs(project string) []string {
	return []string{"volume", "ls", "--filter", "label=com.docker.compose.project=" + project, "--format", "{{.Name}}"}
}

// validApplicationReleaseDiscoveryQuery admits only the read-only volume
// listing of one standalone application project to the closed runner.
func validApplicationReleaseDiscoveryQuery(args []string) bool {
	if len(args) != 6 || args[0] != "volume" || args[1] != "ls" || args[2] != "--filter" || args[4] != "--format" || args[5] != "{{.Name}}" {
		return false
	}
	project, ok := strings.CutPrefix(args[3], "label=com.docker.compose.project=stackkit-")
	return ok && applicationReleaseProjectPattern.MatchString(project)
}

var applicationReleaseProjectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
