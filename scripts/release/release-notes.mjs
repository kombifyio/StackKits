// Structured notes are published beside the release index under the same attestation.
// A scoped fix beginning with "security:" names an app patched without approval.
import assert from "node:assert/strict";

const maxReleases = 40;

const sectionKinds = new Map([
  ["security", "security"],
  ["added", "added"],
  ["fixed", "fixed"],
  ["changed", "changed"],
  ["reverted", "reverted"],
]);

// Scopes of release engineering, not of a node: they never reach a proposal.
const releaseEngineeringScopes = new Set([
  "affected-tests", "beads", "ci", "compat", "consolidation", "delivery", "deps", "docs", "e2e", "early-access", "matrix",
  "preview", "public", "publicexport", "rack-kvm", "release", "techstack", "toolchain", "train", "website", "window",
]);

function versionTag(raw) {
  return `v${raw.replace(/^v/, "")}`;
}

function compareVersions(left, right) {
  const a = left.replace(/^v/, "").split(".").map(Number);
  const b = right.replace(/^v/, "").split(".").map(Number);
  for (let i = 0; i < 3; i += 1) {
    if (a[i] !== b[i]) return a[i] - b[i];
  }
  return 0;
}

function cleanText(text) {
  return text
    .replace(/\s*\(\s*\[#?\w+\]\([^)]*\)\s*\)/g, "")
    .replace(/\s*\(\s*,\s*\)/g, "")
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/\*\*/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

function parseEntry(line) {
  const body = line.replace(/^[-*]\s+/, "");
  const scoped = /^\*\*([a-z0-9][a-z0-9._/-]*):\*\*\s*(.+)$/i.exec(body);
  if (scoped) return { scope: scoped[1].toLowerCase(), text: cleanText(scoped[2]) };
  return { scope: "", text: cleanText(body) };
}

/** Render the release notes of every released section up to `version`. */
export function renderReleaseNotes(changelog, version) {
  assert.match(version, /^v\d+\.\d+\.\d+(?:-(?:beta|edge)\.\d+)?$/, "StackKits version must be exact");
  const stableVersion = version.split("-")[0];
  const releases = [];
  let release = null;
  let kind = null;
  let entry = null;
  const flush = () => {
    if (entry && release && kind && entry.text) {
      const security = kind === "fixed" && /^security:\s*/i.test(entry.text);
      release.entries.push({ kind: security ? "security" : kind, ...entry,
        text: security ? entry.text.replace(/^security:\s*/i, "") : entry.text });
    }
    entry = null;
  };
  for (const line of changelog.split("\n")) {
    const heading = /^## \[?(\d+\.\d+\.\d+)(?=[\]\s(]|$)\]?(?:\([^)]*\))?\s*(?:\((\d{4}-\d{2}-\d{2})\))?/.exec(line);
    if (heading) {
      flush();
      release = { version: versionTag(heading[1]), date: heading[2] ?? "", entries: [] };
      releases.push(release);
      kind = null;
      continue;
    }
    if (/^## /.test(line)) {
      flush();
      release = null;
      kind = null;
      continue;
    }
    const section = /^### (.+?)\s*$/.exec(line);
    if (section) {
      flush();
      kind = sectionKinds.get(section[1].toLowerCase()) ?? null;
      continue;
    }
    if (!release || !kind) continue;
    if (/^[-*]\s+/.test(line)) {
      flush();
      entry = parseEntry(line.trim());
    } else if (entry && line.trim()) {
      entry.text = cleanText(`${entry.text} ${line.trim()}`);
    } else {
      flush();
    }
  }
  flush();
  const published = releases
    .filter((candidate) => compareVersions(candidate.version, stableVersion) <= 0)
    .map((candidate) => ({
      ...candidate,
      entries: candidate.entries.filter((item) => item.kind === "security" || !releaseEngineeringScopes.has(item.scope)),
    }))
    .sort((a, b) => compareVersions(b.version, a.version))
    .slice(0, maxReleases);
  assert(published.some((candidate) => candidate.version === stableVersion), `CHANGELOG.md has no section for ${version}`);
  return {
    schemaVersion: "stackkits.release-notes/v1",
    version,
    changelogUrl: "https://stackkit.cc/changelog",
    releases: published,
  };
}

