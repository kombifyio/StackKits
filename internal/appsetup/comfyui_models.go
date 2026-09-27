package appsetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// ComfyUIModelDownloadAction is the owner-approved setup action of the
// Private AI image-video module. It downloads one model preset into ComfyUI's
// models volume after the owner accepted the preset's license. Weights are
// never part of an install and never downloaded without this action.
const ComfyUIModelDownloadAction = "comfyui-model-download"

// ComfyUIModelFile is one published model file with its exact size and
// SHA-256 from the source repository.
type ComfyUIModelFile struct {
	Folder string
	Name   string
	URL    string
	SHA256 string
	Bytes  int64
}

// ComfyUIModelPreset is one reviewed model set for a shipped workflow
// template. License is the identifier the owner types to accept it.
type ComfyUIModelPreset struct {
	ID         string
	Title      string
	Template   string
	License    string
	LicenseURL string
	MinVRAMGiB int
	Files      []ComfyUIModelFile
}

// ComfyUIModelPresets returns the reviewed presets. Only permissively
// licensed weights are offered; CodeFormer, HunyuanVideo, LTX-Video and
// CogVideoX are deliberately absent.
func ComfyUIModelPresets() []ComfyUIModelPreset {
	return slices.Clone(comfyUIModelPresets)
}

// ResolveComfyUIModelPreset returns the preset the owner selected, only when
// the owner accepted exactly its license.
func ResolveComfyUIModelPreset(id, acceptedLicense string) (ComfyUIModelPreset, error) {
	id = strings.TrimSpace(id)
	for _, preset := range comfyUIModelPresets {
		if preset.ID != id {
			continue
		}
		if strings.TrimSpace(acceptedLicense) != preset.License {
			return ComfyUIModelPreset{}, fmt.Errorf("preset %s downloads %s under the %s license (%s); read it and set \"acceptLicense\": %q to accept it", preset.ID, preset.Title, preset.License, preset.LicenseURL, preset.License)
		}
		return preset, nil
	}
	ids := make([]string, 0, len(comfyUIModelPresets))
	for _, preset := range comfyUIModelPresets {
		ids = append(ids, preset.ID)
	}
	return ComfyUIModelPreset{}, fmt.Errorf("unknown model preset %q; choose one of %s", id, strings.Join(ids, ", "))
}

// ComfyUIDeviceVRAMGiB reads the largest GPU memory ComfyUI reports.
func ComfyUIDeviceVRAMGiB(ctx context.Context, client *http.Client, baseURL string) (int, error) {
	var stats struct {
		Devices []struct {
			Type      string `json:"type"`
			VRAMTotal int64  `json:"vram_total"`
		} `json:"devices"`
	}
	if err := comfyUIGetJSON(ctx, client, baseURL, "/system_stats", &stats); err != nil {
		return 0, err
	}
	largest := int64(0)
	for _, device := range stats.Devices {
		if device.Type != "cpu" && device.VRAMTotal > largest {
			largest = device.VRAMTotal
		}
	}
	return int(largest >> 30), nil
}

// VerifyComfyUIModels confirms that ComfyUI lists every file of the preset in
// its model folders, which is what the workflow templates load.
func VerifyComfyUIModels(ctx context.Context, client *http.Client, baseURL string, preset ComfyUIModelPreset) error {
	listed := map[string][]string{}
	for _, file := range preset.Files {
		if _, read := listed[file.Folder]; !read {
			var names []string
			if err := comfyUIGetJSON(ctx, client, baseURL, "/models/"+url.PathEscape(file.Folder), &names); err != nil {
				return err
			}
			listed[file.Folder] = names
		}
		if !slices.Contains(listed[file.Folder], file.Name) {
			return fmt.Errorf("ComfyUI does not list %s/%s after the download", file.Folder, file.Name)
		}
	}
	return nil
}

func comfyUIGetJSON(ctx context.Context, client *http.Client, baseURL, path string, value any) error {
	if client == nil || client.Transport == nil {
		return errors.New("ComfyUI setup requires the already-admitted application HTTP client")
	}
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return errors.New("ComfyUI setup requires the admitted application endpoint")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String()+path, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("reach ComfyUI: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("ComfyUI answered %s with HTTP %d", path, response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(value); err != nil {
		return fmt.Errorf("decode ComfyUI %s: %w", path, err)
	}
	return nil
}

// The reviewed presets (2026-09-27). Sources are pinned to a repository
// commit; the fetch script installs a file only after its published size and
// SHA-256 match.
var comfyUIModelPresets = []ComfyUIModelPreset{
	{
		ID: "flux-schnell", Title: "FLUX.1 [schnell] (FP8, all-in-one checkpoint)",
		Template: "kombify-text-to-image and kombify-image-to-image",
		License:  "apache-2.0", LicenseURL: "https://huggingface.co/black-forest-labs/FLUX.1-schnell",
		MinVRAMGiB: 8,
		Files: []ComfyUIModelFile{{
			Folder: "checkpoints", Name: "flux1-schnell-fp8.safetensors",
			URL:    "https://huggingface.co/Comfy-Org/flux1-schnell/resolve/c2b683ea00713d6feadcd54b39e3725bbc78638b/flux1-schnell-fp8.safetensors",
			SHA256: "ead426278b49030e9da5df862994f25ce94ab2ee4df38b556ddddb3db093bf72", Bytes: 17236328572,
		}},
	},
	{
		ID: "real-esrgan", Title: "Real-ESRGAN x4plus upscaler",
		Template: "kombify-upscale-4x",
		License:  "bsd-3-clause", LicenseURL: "https://github.com/xinntao/Real-ESRGAN/blob/master/LICENSE",
		MinVRAMGiB: 2,
		Files: []ComfyUIModelFile{{
			Folder: "upscale_models", Name: "RealESRGAN_x4plus.pth",
			URL:    "https://github.com/xinntao/Real-ESRGAN/releases/download/v0.1.0/RealESRGAN_x4plus.pth",
			SHA256: "4fa0d38905f75ac06eb49a7951b426670021be3018265fd191d2125df9d682f1", Bytes: 67040989,
		}},
	},
	{
		ID: "wan22-5b", Title: "Wan 2.2 TI2V 5B text/image-to-video",
		Template: "kombify-video-wan22-5b",
		License:  "apache-2.0", LicenseURL: "https://huggingface.co/Wan-AI/Wan2.2-TI2V-5B",
		MinVRAMGiB: 16,
		Files: []ComfyUIModelFile{
			{
				Folder: "diffusion_models", Name: "wan2.2_ti2v_5B_fp16.safetensors",
				URL:    "https://huggingface.co/Comfy-Org/Wan_2.2_ComfyUI_Repackaged/resolve/ee6f4a40737a995bf5818954cfce6d59443b0f04/split_files/diffusion_models/wan2.2_ti2v_5B_fp16.safetensors",
				SHA256: "456f901338bd9eadbded3828b819109a9b68e8a525ca5cf8d0049a69fcfeca1e", Bytes: 9999658848,
			},
			{
				Folder: "text_encoders", Name: "umt5_xxl_fp8_e4m3fn_scaled.safetensors",
				URL:    "https://huggingface.co/Comfy-Org/Wan_2.2_ComfyUI_Repackaged/resolve/ee6f4a40737a995bf5818954cfce6d59443b0f04/split_files/text_encoders/umt5_xxl_fp8_e4m3fn_scaled.safetensors",
				SHA256: "c3355d30191f1f066b26d93fba017ae9809dce6c627dda5f6a66eaa651204f68", Bytes: 6735906897,
			},
			{
				Folder: "vae", Name: "wan2.2_vae.safetensors",
				URL:    "https://huggingface.co/Comfy-Org/Wan_2.2_ComfyUI_Repackaged/resolve/ee6f4a40737a995bf5818954cfce6d59443b0f04/split_files/vae/wan2.2_vae.safetensors",
				SHA256: "e40321bd36b9709991dae2530eb4ac303dd168276980d3e9bc4b6e2b75fed156", Bytes: 1409400960,
			},
		},
	},
}
