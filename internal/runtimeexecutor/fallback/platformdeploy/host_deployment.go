package platformdeploy

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// HostDeployment is the node's custody of the platform identity for the same
// generated Compose project. It contains no credential or application data.
type HostDeployment struct {
	APIVersion     string        `json:"apiVersion"`
	WorkloadRef    string        `json:"workloadRef"`
	InstanceRef    string        `json:"instanceRef"`
	ArtifactDigest string        `json:"artifactDigest"`
	SiteRef        string        `json:"siteRef"`
	NodeRef        string        `json:"nodeRef"`
	ServerID       string        `json:"serverId"`
	Deployment     DeploymentRef `json:"deployment"`
}

const hostDeploymentAPIVersion = "stackkit.platform-host-deployment/v1"
const hostDeploymentFile = "platform-deployment.json"

var hostProjectPattern = regexp.MustCompile(`^stackkit-[a-z0-9][a-z0-9-]{0,125}$`)
var hostArtifactDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func validateHostDeployment(project string, state HostDeployment) error {
	if !hostProjectPattern.MatchString(project) || state.APIVersion != hostDeploymentAPIVersion ||
		state.WorkloadRef == "" || state.InstanceRef == "" || state.SiteRef == "" || state.NodeRef == "" ||
		!hostArtifactDigestPattern.MatchString(state.ArtifactDigest) || state.ServerID == "" ||
		state.Deployment.Platform != "komodo" || state.Deployment.AppName != project ||
		state.Deployment.ExternalID == "" || strings.TrimSpace(state.Deployment.ExternalID) != state.Deployment.ExternalID {
		return errors.New("platform deployment custody does not bind the exact node project")
	}
	return nil
}

func SaveOwnerHostDeployment(workspace, project string, state HostDeployment) error {
	state.APIVersion = hostDeploymentAPIVersion
	if err := validateHostDeployment(project, state); err != nil {
		return err
	}
	directory := filepath.Join(workspace, ".stackkit", "runtime", "applications", project)
	root, err := confinedfs.Open(directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(hostDeploymentFile, append(data, '\n'))
	if err == nil && (!result.Installed || !result.FileSynced) {
		return errors.New("platform deployment custody was not durably installed")
	}
	return err
}

func LoadOwnerHostDeployment(workspace, project string) (HostDeployment, error) {
	if !hostProjectPattern.MatchString(project) {
		return HostDeployment{}, errors.New("platform project identity is invalid")
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return HostDeployment{}, err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return HostDeployment{}, err
	}
	file, err := view.Open(filepath.ToSlash(filepath.Join(".stackkit", "runtime", "applications", project, hostDeploymentFile)))
	if err != nil {
		return HostDeployment{}, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return HostDeployment{}, errors.New("platform deployment custody is not private")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return HostDeployment{}, errors.New("platform deployment custody cannot be read")
	}
	var state HostDeployment
	if err := json.Unmarshal(data, &state); err != nil {
		return state, errors.New("platform deployment custody is invalid")
	}
	return state, validateHostDeployment(project, state)
}

// HasOwnerHostDeployment distinguishes absent native custody from invalid
// custody. Only the former may use a historical deployment-state projection.
func HasOwnerHostDeployment(workspace, project string) (bool, error) {
	_, err := LoadOwnerHostDeployment(workspace, project)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
