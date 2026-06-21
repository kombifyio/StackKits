#!/usr/bin/env python3
# Copyright 2026 Kombiverse Labs.
#
# Licensed under the Apache License, Version 2.0 (the "License").

"""Build the kombify-StackKits architecture snapshot.

Source-of-truth extraction for the kombify-StackKits architectural patterns
(addons, modules, service-groups, stackkits, security/identity standards,
wizard schema). The resulting `architecture-snapshot.json` is the contract
that downstream agents (e.g. kombify-Agents/homelab-architect-py) consume
to ground their recommendations in the curated kombify defaults.

Why a snapshot instead of querying CUE live in the agent: agents that run
outside the kombify perimeter (Marketplace, Gemini Enterprise Agent Garden)
need a stable, Apache-2.0-clean artefact they can fetch over plain HTTPS
from the public OSS mirror. Same artefact serves internal agents — no
divergent code paths.

Runs locally and in CI (`.github/workflows/export-architecture-snapshot.yml`).
Output is `architecture-snapshot.json` at repo root.

Usage:
    python scripts/build-architecture-snapshot.py
    python scripts/build-architecture-snapshot.py --output build/snapshot.json
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import re
import subprocess
import sys
from pathlib import Path
from typing import Any


REPO_ROOT = Path(__file__).resolve().parent.parent

ADDONS_DIR = REPO_ROOT / "addons"
MODULES_DIR = REPO_ROOT / "modules"
BASE_DIR = REPO_ROOT / "base"
MODERN_HOMELAB_DIR = REPO_ROOT / "modern-homelab"
SCHEMAS_DIR = REPO_ROOT / "schemas"


def run_cue_eval_json(expr: str, pkg_dir: Path) -> Any | None:
    """Evaluate a CUE expression against a package dir as JSON. None on error."""
    rel = "./" + pkg_dir.relative_to(REPO_ROOT).as_posix()
    try:
        result = subprocess.run(
            ["cue", "eval", rel, "-e", expr, "--out", "json"],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
            timeout=30,
        )
        if result.returncode != 0:
            return None
        return json.loads(result.stdout)
    except (subprocess.TimeoutExpired, FileNotFoundError, json.JSONDecodeError):
        return None


def run_cue_eval(expr: str, path: Path) -> str | None:
    """Evaluate a CUE expression against a file. Returns stdout or None on error."""
    try:
        result = subprocess.run(
            ["cue", "eval", "-e", expr, str(path)],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
            timeout=30,
        )
        if result.returncode != 0:
            return None
        return result.stdout.strip()
    except (subprocess.TimeoutExpired, FileNotFoundError):
        return None


_KV_RE = re.compile(r'^\s*([a-zA-Z_][a-zA-Z0-9_-]*)\s*:\s*(.+?)\s*$', re.MULTILINE)
_STRING_VAL_RE = re.compile(r'^"(.*)"$')
_BOOL_VAL_RE = re.compile(r'^(true|false)$')
_INT_VAL_RE = re.compile(r'^(-?\d+)$')
_LIST_VAL_RE = re.compile(r'^\[(.*)\]$', re.DOTALL)


def parse_cue_kv(text: str) -> dict[str, Any]:
    """Parse a simple CUE-eval KV-block into a Python dict.

    Handles strings, booleans, ints, and flat string-lists. Nested
    structures fall through as raw text (good enough for the snapshot —
    we mostly care about the top-level fields).
    """
    out: dict[str, Any] = {}
    for m in _KV_RE.finditer(text):
        key, raw = m.group(1), m.group(2).strip()
        sm = _STRING_VAL_RE.match(raw)
        if sm:
            out[key] = sm.group(1)
            continue
        bm = _BOOL_VAL_RE.match(raw)
        if bm:
            out[key] = bm.group(1) == "true"
            continue
        im = _INT_VAL_RE.match(raw)
        if im:
            out[key] = int(im.group(1))
            continue
        lm = _LIST_VAL_RE.match(raw)
        if lm:
            items = lm.group(1).strip()
            if not items:
                out[key] = []
            else:
                parts = [p.strip().strip('"') for p in items.split(",")]
                out[key] = [p for p in parts if p]
            continue
        out[key] = raw
    return out


def parse_disjunction(text: str) -> list[str]:
    """Parse a CUE disjunction expression `"a" | "b" | "c"` into a list."""
    parts = [p.strip().strip('"') for p in text.split("|")]
    return [p.replace("*", "") for p in parts if p]


def extract_placement(cue_path: Path) -> dict[str, Any]:
    """Per-module/addon placement eligibility (PUBLISHABLE metadata).
    Reads #PlacementSupport if declared; defaults otherwise. Eligibility is NOT
    realization — MS config stays in the Control-Plane catalog."""
    raw = run_cue_eval("placementSupport", cue_path) or ""
    parsed = parse_cue_kv(raw) if raw else {}

    def as_bool(value: Any, default: bool) -> bool:
        if isinstance(value, bool):
            return value
        if isinstance(value, str):
            return value.strip().lower() == "true"
        return default

    supports = {
        "local_only": as_bool(parsed.get("local_only"), True),
        "standard": as_bool(parsed.get("standard"), True),
        "managed_serverless": as_bool(parsed.get("managed_serverless"), False),
    }
    return {
        "supports": supports,
        "cf_fit": supports["managed_serverless"],
        "rejection_reason": parsed.get("rejection_reason") or None,
    }


def extract_addons() -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for addon_cue in sorted(ADDONS_DIR.glob("*/addon.cue")):
        slug = addon_cue.parent.name
        meta = run_cue_eval("#Config._addon", addon_cue) or ""
        parsed = parse_cue_kv(meta) if meta else {}
        out.append(
            {
                "slug": parsed.get("name") or slug,
                "display_name": parsed.get("displayName") or slug,
                "version": parsed.get("version") or "",
                "layer": parsed.get("layer") or "ADDON",
                "description": parsed.get("description") or "",
                "source_path": str(addon_cue.relative_to(REPO_ROOT)).replace("\\", "/"),
                "placement": extract_placement(addon_cue),
            }
        )
    return out


def extract_modules() -> list[dict[str, Any]]:
    """Modules are atomic service contracts. Pull the slug + best-effort metadata."""
    out: list[dict[str, Any]] = []
    seen: set[str] = set()
    for module_cue in sorted(MODULES_DIR.glob("*/module.cue")):
        slug = module_cue.parent.name
        if slug in seen:
            continue
        seen.add(slug)
        # Try a few common patterns — modules don't share a single shape yet.
        meta_text = run_cue_eval("module", module_cue) or run_cue_eval("#Module", module_cue) or ""
        parsed = parse_cue_kv(meta_text) if meta_text else {}
        out.append(
            {
                "slug": parsed.get("name") or parsed.get("slug") or slug,
                "display_name": parsed.get("displayName") or slug,
                "layer": parsed.get("layer") or "MODULE",
                "description": parsed.get("description") or "",
                "source_path": str(module_cue.relative_to(REPO_ROOT)).replace("\\", "/"),
                "placement": extract_placement(module_cue),
            }
        )
    return out


def extract_stackkits() -> list[dict[str, Any]]:
    """Composed StackKits: base-kit, modern-homelab, ha-kit, …"""
    out: list[dict[str, Any]] = []
    # Look for top-level dirs that contain a stackfile.cue (= composition layer).
    for stackfile in sorted(REPO_ROOT.glob("*/stackfile.cue")):
        slug = stackfile.parent.name
        # Pull a best-effort name from the comment header or stackfile contents.
        text = stackfile.read_text(encoding="utf-8", errors="ignore")
        display = slug.replace("-", " ").title()
        # Heuristic: first line that says "// Package ..."
        for line in text.splitlines()[:10]:
            line = line.strip().lstrip("/").strip()
            if line.startswith("Package "):
                display = line[len("Package "):].split("-")[0].strip()
                break
        contexts_dir = stackfile.parent / "contexts"
        contexts: list[str] = []
        if contexts_dir.is_dir():
            contexts = sorted([p.stem for p in contexts_dir.glob("*.cue")])
        out.append(
            {
                "slug": slug,
                "display_name": display,
                "contexts": contexts,
                "source_path": str(stackfile.relative_to(REPO_ROOT)).replace("\\", "/"),
            }
        )
    return out


def extract_mode_matrix() -> dict[str, Any]:
    """Per-kit mode-support matrix (base/mode_matrix.cue #KitModeSupport).

    Fail-closed: every kit (top-level dir with a stackfile.cue) MUST declare a
    modeMatrix — a kit without one aborts the build rather than silently
    disappearing from the downstream contract."""
    out: dict[str, Any] = {}
    for stackfile in sorted(REPO_ROOT.glob("*/stackfile.cue")):
        kit_dir = stackfile.parent
        slug = kit_dir.name
        matrix = run_cue_eval_json("modeMatrix", kit_dir)
        if matrix is None:
            raise SystemExit(
                f"ERROR: kit {slug!r} declares no modeMatrix "
                f"(expected {slug}/mode_matrix.cue conforming to base/mode_matrix.cue #KitModeSupport)"
            )
        out[slug] = matrix
    return out


def extract_service_groups() -> list[dict[str, Any]]:
    """Service-groups are emitted by the wizard / kit catalog. Many StackKits
    don't declare a single canonical `service_groups` block today — we
    derive a best-effort map by inspecting the addons + module slugs
    against well-known categories. The kombify-Administration mirror is
    the canonical writer for this list; the snapshot is a developer-time
    bootstrap until that pipeline lands."""
    # Heuristic group-mapping. Mirrors the hand-curated set the agent
    # already knows about; the goal is to keep the contract stable while
    # the OSS mirror catches up.
    groups = [
        ("photos", "Foto-Verwaltung", "photos", []),
        ("media", "Mediathek", "media", []),
        ("vault", "Password Vault", "vault", []),
        ("smart-home", "Smart Home", "smart-home", []),
        ("files", "File-Sharing / Cloud-Drive", "file-sharing", []),
        ("ai-llm", "AI / LLM", "ai-workloads", []),
        ("dev", "Dev-Platform", "dev-platform", []),
        ("calendar", "Kalender / CardDAV", "calendar", []),
        ("mail", "Mail (light)", "mail", []),
        ("remote-desktop", "Remote-Desktop", "remote-desktop", []),
        ("tunnel", "External Access / Tunnel", "tunnel", ["vpn-overlay"]),
        ("vpn-overlay", "Mesh-VPN", "vpn-overlay", []),
        ("backup", "Backup", "backup", []),
        ("monitoring", "Observability", "monitoring", []),
        ("gameserver", "Gameserver", "gameserver", []),
        ("ha", "High-Availability", "ha", []),
        ("auth", "Authentifizierung", "login-gateway", ["authelia"]),
        ("identity-base", "Identity / PKI", "step-ca", []),
    ]
    return [
        {
            "slug": slug,
            "display_name": display,
            "primary_module": primary,
            "alternative_modules": alts,
        }
        for slug, display, primary, alts in groups
    ]


def extract_access_profiles() -> list[str]:
    raw = run_cue_eval("#AccessProfile", BASE_DIR / "security.cue") or ""
    if not raw:
        return []
    return parse_disjunction(raw)


def extract_zero_trust_policy() -> dict[str, Any]:
    """Extract the Zero-Trust-Policy primitive from base/security.cue."""
    text = (BASE_DIR / "security.cue").read_text(encoding="utf-8", errors="ignore")
    enabled_match = re.search(r'#ZeroTrustPolicy:\s*\{[^}]*?enabled:[^,\n]*\*(true|false)', text, re.DOTALL)
    device_trust_match = re.search(r'device[Tt]rust\s*[:=]?\s*"([^"]+)"', text)
    return {
        "enabled_default": True if (enabled_match and enabled_match.group(1) == "true") else False,
        "device_trust": device_trust_match.group(1) if device_trust_match else "mTLS",
        "source_path": "base/security.cue",
    }


def extract_identity_patterns() -> dict[str, Any]:
    """Best-effort extraction of identity baselines from base/identity.cue."""
    text = (BASE_DIR / "identity.cue").read_text(encoding="utf-8", errors="ignore")
    has_step_ca = "Step-CA" in text or "step-ca" in text
    has_jwk = "JWK" in text or "jwk" in text
    return {
        "internal_ca": "step-ca" if has_step_ca else None,
        "jwk_provisioner": has_jwk,
        "auth_gateway": "login-gateway",
        "source_path": "base/identity.cue",
    }


def extract_wizard_schema() -> dict[str, Any]:
    """Pull the 4-question wizard schema from schemas/wizard.cue."""
    path = SCHEMAS_DIR / "wizard.cue"
    if not path.exists():
        return {}
    text = path.read_text(encoding="utf-8", errors="ignore")
    # #Goal disjunction — span until the next top-level `#` definition.
    goals: list[str] = []
    goal_block_match = re.search(
        r"^// #Goal.*?\n#Goal\s*:(?P<body>.+?)(?=^// #|^#[A-Z]|^\Z)",
        text,
        re.DOTALL | re.MULTILINE,
    )
    if not goal_block_match:
        # Fallback: just find the #Goal: line and grab everything until the
        # next blank line.
        goal_block_match = re.search(
            r"#Goal\s*:(?P<body>.+?)(?:\n\s*\n|\n#[A-Z])",
            text,
            re.DOTALL,
        )
    if goal_block_match:
        goals_raw = goal_block_match.group("body")
        # Strip inline `// comment` text so quoted literals inside comments
        # don't pollute the result.
        cleaned = "\n".join(line.split("//", 1)[0] for line in goals_raw.splitlines())
        goals = re.findall(r'"([a-z][a-z-]*)"', cleaned)
    # access.audience values
    audiences: list[str] = []
    aud_match = re.search(r'audience:\s*("[^"]+"\s*\|\s*"[^"]+"(?:\s*\|\s*"[^"]+")*)', text)
    if aud_match:
        audiences = re.findall(r'"([a-z-]+)"', aud_match.group(1))
    # login.primaryMethod values
    login_methods: list[str] = []
    login_match = re.search(r'primaryMethod:\s*(\*?"[^"]+"\s*\|\s*\*?"[^"]+"(?:\s*\|\s*\*?"[^"]+")*)', text)
    if login_match:
        login_methods = re.findall(r'"([a-z-]+)"', login_match.group(1))
    return {
        "version": "1.0.0",
        "goals": goals,
        "audiences": audiences,
        "login_methods": login_methods,
        "intent_free_text_supported": "intentFreeText" in text,
        "source_path": "schemas/wizard.cue",
    }


def extract_contexts() -> dict[str, Any]:
    """Modern-homelab contexts (local / cloud)."""
    out: dict[str, Any] = {}
    ctx_dir = MODERN_HOMELAB_DIR / "contexts"
    if not ctx_dir.is_dir():
        return out
    for cue_file in sorted(ctx_dir.glob("*.cue")):
        ctx_name = cue_file.stem  # "local" / "cloud"
        text = cue_file.read_text(encoding="utf-8", errors="ignore")
        services: list[str] = []
        services_match = re.search(r"services:\s*\[(.*?)\]", text, re.DOTALL)
        if services_match:
            services = re.findall(r'"([a-z0-9-]+)"', services_match.group(1))
        # Heuristic: extract `publicAccess` flag
        public_access_match = re.search(r"publicAccess:\s*(true|false)", text)
        out[ctx_name] = {
            "services": services,
            "public_access_default": public_access_match.group(1) == "true" if public_access_match else None,
            "source_path": str(cue_file.relative_to(REPO_ROOT)).replace("\\", "/"),
        }
    return out


def git_sha() -> str:
    try:
        r = subprocess.run(
            ["git", "rev-parse", "HEAD"],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
            timeout=10,
        )
        if r.returncode == 0:
            return r.stdout.strip()
    except Exception:
        pass
    return os.environ.get("GITHUB_SHA", "")


def build_snapshot() -> dict[str, Any]:
    return {
        "$comment": (
            "Auto-generated by scripts/build-architecture-snapshot.py. "
            "Source-of-truth for downstream agents that need to ground "
            "recommendations in the curated kombify-StackKits patterns. "
            "Do not hand-edit — re-run the script after modifying the CUE."
        ),
        "schema_version": "2.2.0",
        "generated_at": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "source": {
            "repo": "kombifyio/stackKits",
            "ref": "main",
            "commit": git_sha(),
        },
        "placement_model": {
            "axis": "placementMode",
            "modes": ["local-only", "standard", "managed-serverless"],
            "sub_dimensions": {
                "exposure": ["private", "public"],
                "coupling": ["cloudless", "coupled"],
            },
            "oss_realized": ["local-only", "standard+cloudless"],
            "control_plane_only": ["standard+coupled", "managed-serverless"],
            "note": (
                "OSS publishes eligibility; realization of coupled/managed-serverless "
                "lives in the Control-Plane catalog (kombify-DB sk_*), never in OSS."
            ),
            "source_standard": "public StackKits standards/PLACEMENT-MODE-STANDARD.md@v1.1",
        },
        "architecture_patterns": {
            "access_profiles": extract_access_profiles(),
            "zero_trust_policy": extract_zero_trust_policy(),
            "identity_patterns": extract_identity_patterns(),
        },
        "wizard_schema": extract_wizard_schema(),
        "contexts": extract_contexts(),
        "stackkits": extract_stackkits(),
        "mode_matrix": {
            "levels": ["supported", "scaffolding", "unsupported", "control-plane"],
            "paas_statuses": ["default", "supported", "draft", "experimental"],
            "note": (
                "Declared per kit in <kit>/mode_matrix.cue (#KitModeSupport). "
                "'supported' requires cited evidence; managed-serverless cells "
                "are schema-forced to control-plane in OSS."
            ),
            "kits": extract_mode_matrix(),
        },
        "service_groups": extract_service_groups(),
        "modules": extract_modules(),
        "addons": extract_addons(),
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Build kombify-StackKits architecture snapshot")
    parser.add_argument(
        "--output",
        default=str(REPO_ROOT / "architecture-snapshot.json"),
        help="Output path (default: ./architecture-snapshot.json)",
    )
    args = parser.parse_args(argv)

    snapshot = build_snapshot()
    out_path = Path(args.output).resolve()
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(
        json.dumps(snapshot, indent=2, ensure_ascii=False, sort_keys=False) + "\n",
        encoding="utf-8",
    )
    print(f"Wrote {out_path}")
    print(
        f"  addons:         {len(snapshot['addons'])}\n"
        f"  modules:        {len(snapshot['modules'])}\n"
        f"  service-groups: {len(snapshot['service_groups'])}\n"
        f"  stackkits:      {len(snapshot['stackkits'])}\n"
        f"  access-profiles: {snapshot['architecture_patterns']['access_profiles']}\n"
        f"  goals:           {snapshot['wizard_schema'].get('goals', [])}"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
