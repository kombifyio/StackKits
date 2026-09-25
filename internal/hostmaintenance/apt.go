package hostmaintenance

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
)

// simulation is what `apt-get -s upgrade` would do, split into the packages
// apply installs and the packages held back by policy.
type simulation struct {
	Apply    []Package
	Held     []Package
	KeptBack []string
}

// Inst lines of an apt-get simulation:
//
//	Inst libssl3 [3.0.2-0ubuntu1.10] (3.0.2-0ubuntu1.12 Ubuntu:22.04/jammy-updates, Ubuntu:22.04/jammy-security [amd64])
var simulationInstLine = regexp.MustCompile(`^Inst (\S+) (?:\[([^\]]*)\] )?\((\S+) ([^)]*)\)`)

// Package names and versions become apt-get arguments inside the update
// unit's script, so both are restricted to the Debian policy alphabets.
var (
	debianPackageName    = regexp.MustCompile(`^[a-z0-9][a-z0-9+.\-]+(:[a-z0-9\-]+)?$`)
	debianPackageVersion = regexp.MustCompile(`^[A-Za-z0-9.+~:\-]+$`)
)

// parseSimulation reads the stdout of `apt-get -s upgrade`.
func parseSimulation(stdout string) simulation {
	result := simulation{Apply: []Package{}, Held: []Package{}, KeptBack: []string{}}
	inKeptBack := false
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "The following packages have been kept back") {
			inKeptBack = true
			continue
		}
		if inKeptBack {
			if strings.HasPrefix(line, " ") {
				result.KeptBack = append(result.KeptBack, strings.Fields(line)...)
				continue
			}
			inKeptBack = false
		}
		pkg, ok := parseInstLine(line)
		if !ok {
			continue
		}
		if IsHeld(pkg.Name) {
			result.Held = append(result.Held, pkg)
			continue
		}
		result.Apply = append(result.Apply, pkg)
	}
	sort.Slice(result.Apply, func(i, j int) bool { return result.Apply[i].Name < result.Apply[j].Name })
	sort.Slice(result.Held, func(i, j int) bool { return result.Held[i].Name < result.Held[j].Name })
	sort.Strings(result.KeptBack)
	return result
}

func parseInstLine(line string) (Package, bool) {
	match := simulationInstLine.FindStringSubmatch(line)
	if match == nil {
		return Package{}, false
	}
	return Package{Name: match[1], From: match[2], To: match[3], Security: securityOrigin(match[4])}, true
}

// securityOrigin reports whether any origin of the candidate is a security
// archive: a `-security` suite (Ubuntu, Debian) or the Debian-Security label.
//
//	Ubuntu:24.04/noble-updates, Ubuntu:24.04/noble-security [amd64]
//	Debian-Security:12/stable-security [amd64]
func securityOrigin(origins string) bool {
	if index := strings.LastIndex(origins, " ["); index >= 0 {
		origins = origins[:index]
	}
	for _, origin := range strings.Split(origins, ",") {
		label, rest, _ := strings.Cut(strings.TrimSpace(origin), ":")
		_, suite, _ := strings.Cut(rest, "/")
		if label == "Debian-Security" || strings.HasSuffix(suite, "-security") {
			return true
		}
	}
	return false
}

// installSetViolations compares the simulation of the exact apply command with
// the approved set: apply may install nothing else and remove nothing.
func installSetViolations(stdout string, approved []Package) []string {
	allowed := make(map[string]string, len(approved))
	for _, pkg := range approved {
		allowed[pkg.Name] = pkg.To
	}
	var violations []string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "Remv ") {
			violations = append(violations, "would remove "+strings.Fields(line)[1])
			continue
		}
		pkg, ok := parseInstLine(line)
		if !ok {
			continue
		}
		if version, exists := allowed[pkg.Name]; !exists || version != pkg.To {
			violations = append(violations, "would also install "+pkg.Name+"="+pkg.To)
		}
	}
	return violations
}

// IsHeld reports whether a package is held by default: the container runtime
// is upgraded deliberately, never as a side effect of OS updates, because
// upgrading it restarts every workload on the node.
//
// The hold binds `stackkit host updates apply` only. unattended-upgrades is
// governed by the StackKit base policy, not by this list.
func IsHeld(name string) bool {
	base := packageBaseName(name)
	switch base {
	case "docker.io", "docker-engine", "runc", "crun", "podman",
		"moby-engine", "moby-containerd", "moby-runc":
		return true
	}
	return strings.HasPrefix(base, "docker-ce") || strings.HasPrefix(base, "containerd")
}

// HoldScope is reported with every plan so no reader mistakes the hold for a
// system-wide apt hold.
const HoldScope = "Held packages are excluded from `stackkit host updates apply` only; unattended-upgrades is governed by the StackKit base policy."

// PlanDigest is `sha256:` plus the hex SHA-256 of the lexically sorted
// `name=version` lines of the packages apply would install, each line
// terminated by a line feed. Held packages are not part of it.
func PlanDigest(packages []Package) string {
	lines := make([]string, 0, len(packages))
	for _, pkg := range packages {
		lines = append(lines, pkg.Name+"="+pkg.To+"\n")
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// rebootLikely reports whether the set updates the kernel, the C library or
// systemd, the packages whose running copies only a reboot replaces.
func rebootLikely(packages []Package) bool {
	for _, pkg := range packages {
		base := packageBaseName(pkg.Name)
		switch {
		case strings.HasPrefix(base, "linux-image"), strings.HasPrefix(base, "linux-modules"),
			strings.HasPrefix(base, "linux-generic"), strings.HasPrefix(base, "linux-virtual"),
			base == "libc6", base == "libc-bin",
			base == "systemd", base == "systemd-sysv", base == "libsystemd0":
			return true
		}
	}
	return false
}

func securityCount(packages []Package) int {
	count := 0
	for _, pkg := range packages {
		if pkg.Security {
			count++
		}
	}
	return count
}

func packageBaseName(name string) string {
	if index := strings.IndexByte(name, ':'); index >= 0 {
		return name[:index]
	}
	return name
}

func validPackageToken(pkg Package) bool {
	return debianPackageName.MatchString(pkg.Name) && debianPackageVersion.MatchString(pkg.To)
}

// isLockContention recognizes apt and dpkg lock refusals. A lock file the
// caller may not open (not root) is a permission failure, not contention.
func isLockContention(text string) bool {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "permission denied") || strings.Contains(lower, "are you root") {
		return false
	}
	for _, marker := range []string{"could not get lock", "unable to lock", "unable to acquire the dpkg frontend lock"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
