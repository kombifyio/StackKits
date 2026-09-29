---
name: image-video
description: Help the owner use ComfyUI installed by StackKits in Private AI - download a reviewed model preset after accepting its license, open the shipped image, upscale and video templates, generate images from Open WebUI, and understand what is backed up - without installing custom nodes or exposing ComfyUI publicly.
---

# Image and video

StackKits installs ComfyUI as the Private AI image-video module, on the CPU by
default (slow) or on the owner's NVIDIA GPU with the optional `nvidia`
accelerator profile (workload `ai-image-video`). ComfyUI has no sign-in of its
own: it is reached only on the private route `https://ai-image-video.<domain>`
behind the kit's login, and by Open WebUI on the private AI network. It never
loads ComfyUI-Manager or custom nodes; do not suggest installing them.

## Download a model

No model is bundled or downloaded at install. Downloading a preset is an
owner-approved setup action. The owner must read and accept the model license
personally; never accept it on their behalf.

```sh
stackkit setup ai-image-video --owner-approve --credentials-file .stackkit/setup/ai-image-video.json --json
```

```json
{"preset":"flux-schnell","acceptLicense":"apache-2.0"}
```

| Preset | License | Size | GPU | CPU only | Templates |
| --- | --- | --- | --- | --- | --- |
| `flux-schnell` | `apache-2.0` | 17.2 GB | 8 GiB VRAM | 32 GB RAM | `kombify-text-to-image`, `kombify-image-to-image`, Open WebUI images |
| `real-esrgan` | `bsd-3-clause` | 67 MB | 2 GiB VRAM | 4 GB RAM | `kombify-upscale-4x` |
| `wan22-5b` | `apache-2.0` | 18.1 GB | 16 GiB VRAM | not available | `kombify-video-wan22-5b` |

- Each file comes from a source pinned to a repository commit and is
  installed only when its size and SHA-256 match. An interrupted download
  resumes when the action runs again.
- A preset the GPU, or on a CPU-only node the host RAM, cannot run is
  refused before anything is downloaded.
- Run the action once per preset. Models are not backed up; after a restore,
  download them again.

## Use the templates

Open ComfyUI, then the workflow browser. The `kombify-*` templates are
read-only; save a copy to change one. Uploaded pictures (`input`), results
(`output`) and saved workflows (`user`) are backed up.

## Images in chat

When the chat module is selected, Open WebUI already uses ComfyUI for image
generation with the `flux-schnell` preset (1024x1024, 4 steps). It needs that
preset downloaded first.

## What is not available

Background removal, face restoration and other custom-node workflows are not
shipped. CodeFormer, HunyuanVideo, LTX-Video and CogVideoX are excluded for
license reasons. A CPU-only node runs images and upscaling slowly and cannot run video.
