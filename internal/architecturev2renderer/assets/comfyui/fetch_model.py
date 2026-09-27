#!/usr/bin/env python3
"""StackKits governed model download for ComfyUI.

`stackkit setup ai-image-video` runs this script inside the ComfyUI container
once per model file of an owner-approved preset:

    fetch_model.py <folder> <file> <url> <sha256> <bytes>

It writes only below the models directory, accepts only the fixed model
folders and HTTPS sources, resumes an interrupted download and moves the file
into place only after its size and SHA-256 match. A verified file is recorded
next to it, so a repeated run does not download or hash it again.
"""

import hashlib
import os
import re
import sys
import urllib.error
import urllib.request

MODELS = "/opt/ComfyUI/models"
FOLDERS = {"checkpoints", "diffusion_models", "text_encoders", "vae", "upscale_models"}
SOURCES = ("https://huggingface.co/", "https://github.com/")
CHUNK = 1 << 20


def fail(message, code=2):
    print("stackkit-model: " + message, file=sys.stderr)
    sys.exit(code)


def digest_of(path):
    digest, size = hashlib.sha256(), 0
    with open(path, "rb") as handle:
        for block in iter(lambda: handle.read(CHUNK), b""):
            digest.update(block)
            size += len(block)
    return digest, size


def main(argv):
    if len(argv) != 5:
        fail("usage: fetch_model.py <folder> <file> <url> <sha256> <bytes>")
    folder, name, url, expected, total = argv
    if folder not in FOLDERS or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*\.(safetensors|pth)", name):
        fail("model folder or file name is not admitted")
    if not url.startswith(SOURCES) or not re.fullmatch(r"[0-9a-f]{64}", expected) or not total.isdigit():
        fail("model source, checksum or size is not admitted")
    total = int(total)
    directory = os.path.join(MODELS, folder)
    os.makedirs(directory, exist_ok=True)
    target = os.path.join(directory, name)
    marker = os.path.join(directory, "." + name + ".sha256")
    partial = target + ".partial"
    if os.path.exists(target) and os.path.exists(marker):
        with open(marker, encoding="ascii") as handle:
            if handle.read().strip() == expected and os.path.getsize(target) == total:
                print("present " + folder + "/" + name)
                return
    digest, size = hashlib.sha256(), 0
    if os.path.exists(partial):
        digest, size = digest_of(partial)
        if size > total:
            os.remove(partial)
            digest, size = hashlib.sha256(), 0
    request = urllib.request.Request(url, headers={"User-Agent": "stackkit-model-download"})
    if 0 < size < total:
        request.add_header("Range", "bytes=%d-" % size)
    if size < total:
        try:
            response = urllib.request.urlopen(request, timeout=60)
        except urllib.error.URLError as error:
            fail("download of %s failed: %s" % (name, error), 3)
        with response:
            mode = "ab"
            if size and response.status != 206:
                digest, size, mode = hashlib.sha256(), 0, "wb"
            reported = 0
            with open(partial, mode) as handle:
                for block in iter(lambda: response.read(CHUNK), b""):
                    handle.write(block)
                    digest.update(block)
                    size += len(block)
                    if size > total:
                        break
                    if size - reported >= 512 * CHUNK:
                        reported = size
                        print("progress %s %d/%d" % (name, size, total), file=sys.stderr, flush=True)
    if size != total or digest.hexdigest() != expected:
        os.remove(partial)
        fail("%s does not match its published size and SHA-256; nothing was installed" % name, 4)
    os.replace(partial, target)
    with open(marker, "w", encoding="ascii") as handle:
        handle.write(expected + "\n")
    print("installed " + folder + "/" + name)


if __name__ == "__main__":
    main(sys.argv[1:])
