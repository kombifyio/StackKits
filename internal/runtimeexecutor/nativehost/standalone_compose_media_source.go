package nativehost

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
)

// MediaLibrarySourceError refuses an unavailable owner-mounted library without
// creating a directory, changing its permissions, or exposing its host path.
type MediaLibrarySourceError struct{ reason string }

func (e *MediaLibrarySourceError) Error() string {
	return "the declared media library source is unavailable on the execution host"
}

func (e *MediaLibrarySourceError) ActionableError() actionableerror.Contract {
	message := "StackKits could not read the declared media library on the execution host."
	guidance := "Make storage.hostRoots.mediaRoot readable on the target host, then retry. StackKits does not mount or repair external storage."
	if e.reason == "media_root_missing" {
		message = "The declared media library directory does not exist on the execution host."
		guidance = "Attach the owner-managed disk and verify storage.hostRoots.mediaRoot on the target host, then retry. StackKits will not create an empty library."
	} else if e.reason == "media_root_not_directory" {
		message = "The declared media library source is not a directory."
		guidance = "Point storage.hostRoots.mediaRoot at an existing readable directory on the target host, then retry."
	} else if e.reason == "media_root_unobservable" {
		message = "The declared media library is not an absolute filesystem path on this execution host."
		guidance = "Run workload Apply on the authorized target host with a directly accessible absolute mediaRoot path; the controller's filesystem is not media-path evidence."
	}
	return actionableerror.New("stackkit_command_denied", e.reason, message, []string{guidance}, false)
}

// Native owners execute only an admitted local Site/node/channel. Remote
// channels transport the request to that host; they do not run this probe on
// the controller. The closed Docker runner ignores transport overrides.
func (o *osStandaloneComposeWorkloadOperations) requireMediaLibrarySource(ctx context.Context, bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor) error {
	for _, component := range bundle.Components {
		for _, volume := range component.Volumes {
			if volume.HostPath == "" || !architecturev2renderer.GovernedMediaLibraryMount(bundle.ModuleRef, component.ID, volume.ID, volume.Target) {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			probe := o.mediaRootProbe
			if probe == nil {
				// A POSIX path must not become drive-relative on a different
				// native filesystem. Host ownership comes from channel custody,
				// not an operating-system allowlist or Docker transport override.
				if !filepath.IsAbs(volume.HostPath) {
					return &MediaLibrarySourceError{reason: "media_root_unobservable"}
				}
				probe = probeMediaLibraryDirectory
			}
			if err := probe(volume.HostPath); err != nil {
				var refusal *MediaLibrarySourceError
				if errors.As(err, &refusal) {
					return err
				}
				return mediaLibraryProbeError(err)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
	}
	return nil
}

func probeMediaLibraryDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return mediaLibraryProbeError(err)
	}
	if !info.IsDir() {
		return &MediaLibrarySourceError{reason: "media_root_not_directory"}
	}
	// OpenRoot opens a directory, so a source replaced with a FIFO cannot
	// block a plain file open between the type check and the read.
	root, err := os.OpenRoot(path)
	if err != nil {
		return mediaLibraryProbeError(err)
	}
	defer func() { _ = root.Close() }()
	directory, err := root.Open(".")
	if err != nil {
		return mediaLibraryProbeError(err)
	}
	defer func() { _ = directory.Close() }()
	// Read at most one name: an empty library is valid, and preflight does not
	// scan customer media or infer container UID/GID access from host access.
	if _, err := directory.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return &MediaLibrarySourceError{reason: "media_root_unreadable"}
	}
	return nil
}

func mediaLibraryProbeError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return &MediaLibrarySourceError{reason: "media_root_missing"}
	}
	return &MediaLibrarySourceError{reason: "media_root_unreadable"}
}
