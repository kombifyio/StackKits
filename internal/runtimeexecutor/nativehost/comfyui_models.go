package nativehost

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
)

const comfyUIWorkloadModuleRef = "stackkits-comfyui-runtime"

var (
	comfyUIModelFolders   = map[string]bool{"checkpoints": true, "diffusion_models": true, "text_encoders": true, "vae": true, "upscale_models": true}
	comfyUIModelNameRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.(safetensors|pth)$`)
	comfyUIModelSHA256RE  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	comfyUIModelSourceRE  = regexp.MustCompile(`^https://(huggingface\.co|github\.com)/[A-Za-z0-9._~/%-]+$`)
	errComfyUIModelOutput = errors.New("model files belong only to the ComfyUI image-video workload")
)

// ComfyUIModelFile is one reviewed model file the governed fetch script
// downloads and verifies inside the ComfyUI container.
type ComfyUIModelFile struct {
	Folder, Name, URL, SHA256 string
	Bytes                     int64
}

// DownloadStandaloneComposeComfyUIModel downloads one model file into the
// ComfyUI models volume through the governed fetch_model.py. The call is
// closed: only the admitted ComfyUI deployment, its fixed component and
// script, and a validated folder, file name, HTTPS source, SHA-256 and size.
// The script installs the file only after its size and checksum match.
func DownloadStandaloneComposeComfyUIModel(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, file ComfyUIModelFile) error {
	if !comfyUIModelFolders[file.Folder] || !comfyUIModelNameRE.MatchString(file.Name) || !comfyUIModelSHA256RE.MatchString(file.SHA256) ||
		!comfyUIModelSourceRE.MatchString(file.URL) || strings.Contains(file.URL, "..") || file.Bytes <= 0 {
		return errors.New("model file is not a reviewed ComfyUI preset file")
	}
	if deployment.ModuleRef != comfyUIWorkloadModuleRef {
		return errComfyUIModelOutput
	}
	operations, err := NewOSStandaloneComposeWorkloadOperations(workspace)
	if err != nil {
		return err
	}
	o, ok := operations.(*osStandaloneComposeWorkloadOperations)
	if !ok {
		return errors.New("standalone Compose operations have an unexpected implementation")
	}
	project, err := o.prepare(ctx, deployment)
	if err != nil {
		return err
	}
	if err := o.verifyPersisted(project); err != nil {
		return err
	}
	if project.bundle.ModuleRef != comfyUIWorkloadModuleRef || project.bundle.EntryComponent != "comfyui" {
		return errComfyUIModelOutput
	}
	args := []string{
		"compose", "--project-name", project.name, "--env-file", filepath.Join(project.directory, ".env"),
		"-f", filepath.Join(project.directory, "compose.yaml"),
		"exec", "-T", "comfyui", "python", architecturev2renderer.ComfyUIModelFetchScriptPath,
		file.Folder, file.Name, file.URL, file.SHA256, strconv.FormatInt(file.Bytes, 10),
	}
	if _, err := o.runner.Run(ctx, args, project.directory); err != nil {
		return fmt.Errorf("download %s/%s into the ComfyUI models volume: %w", file.Folder, file.Name, err)
	}
	return nil
}
