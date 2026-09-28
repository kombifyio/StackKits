package hostpreflight

import (
	_ "embed"
	"os"
	"slices"
	"strings"
)

// Host resolutions are the fixes for what preflight found.
//
// Preflight already names what is wrong and what would fix it, but naming is
// where it stopped: the operator had to translate prose into commands, and the
// installer could only refuse. These resolutions make the fix itself an
// auditable artifact -- a closed set of commands and file edits, bound to the
// check that justifies them, each declaring whether it can be undone, whether
// it needs root, and whether it may run without a person watching.
//
// Nothing here executes on its own. Every path requires either an explicit
// --apply with --yes or an installer mode that opted in, and anything needing a
// reboot or a credential stays advice no matter what was opted into.

// Mode says whether a resolution can be carried out or only described.
type Mode string

const (
	// ModeHint is advice. Something outside StackKits' authority has to change:
	// a credential, a hypervisor setting, a decision only the owner can make.
	ModeHint Mode = "hint"
	// ModeApply can be carried out on this host.
	ModeApply Mode = "apply"
)

// FileChange is one declared file edit. Merge, Content and Append are
// exclusive: a merge folds keys into an existing JSON document without
// disturbing the rest, content writes a self-contained drop-in, and append adds
// a line to a file that must otherwise stay as it is.
type FileChange struct {
	Path string `json:"path"`
	Mode uint32 `json:"mode"`

	Merge   map[string]any `json:"merge,omitempty"`
	Content string         `json:"content,omitempty"`
	Append  string         `json:"append,omitempty"`

	// AppendUnlessPresent keeps an append idempotent: the line is added only
	// when the file does not already mention this substring.
	AppendUnlessPresent string `json:"appendUnlessPresent,omitempty"`

	Backup bool `json:"backup"`
}

// Resolution is one fix, bound to the check that justifies it.
type Resolution struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	AppliesTo string `json:"appliesTo"`
	Mode      Mode   `json:"mode"`
	Summary   string `json:"summary"`

	// Files are applied before Commands, so a service restart observes the
	// configuration it is being restarted for.
	Files    []FileChange `json:"files,omitempty"`
	Commands [][]string   `json:"commands,omitempty"`

	// Guidance is what to tell the operator: the whole content of a hint, and
	// the caveats of an apply.
	Guidance []string `json:"guidance,omitempty"`

	// Distributions restricts an apply to hosts whose /etc/os-release ID or
	// ID_LIKE names one of them (package sources are distribution-specific).
	// Elsewhere the resolution is refused with its guidance; empty means any.
	Distributions []string `json:"distributions,omitempty"`

	RequiresRoot   bool `json:"requiresRoot"`
	RequiresReboot bool `json:"requiresReboot"`

	// Reversible means this host can be put back the way it was: a backup is
	// kept, or the change is a drop-in that can simply be removed.
	Reversible bool `json:"reversible"`

	// AutoInstallerEligible marks a resolution the installer may carry out in
	// its unattended mode. It requires Reversible, no reboot and no credential:
	// a fix nobody is watching must be one nobody regrets.
	AutoInstallerEligible bool `json:"autoInstallerEligible"`
}

// The NVIDIA apt source signs with an ASCII-armored key, which apt accepts
// from a .asc keyring file without gpg.
const (
	nvidiaContainerToolkitKeyring    = "/usr/share/keyrings/nvidia-container-toolkit-keyring.asc"
	nvidiaContainerToolkitSourceList = "/etc/apt/sources.list.d/nvidia-container-toolkit.list"
)

// The gVisor apt source (https://gvisor.dev/docs/user_guide/install/) signs
// with "The gVisor Authors" key, fingerprint
// 6F1D F85E 3A71 C249 18E7 27D5 6FC6 D554 E32B D943. The key is pinned in the
// binary rather than fetched at install, so a changed download cannot
// change what the host trusts. The runsc package installs /usr/bin/runsc.
const (
	gvisorKeyring    = "/usr/share/keyrings/gvisor-archive-keyring.asc"
	gvisorSourceList = "/etc/apt/sources.list.d/gvisor.list"
	gvisorRunscPath  = "/usr/bin/runsc"
)

//go:embed assets/gvisor-archive-keyring.asc
var gvisorArchiveKey string

// debianFamily are the distributions whose apt sources these resolutions
// declare (Debian, Ubuntu and ID_LIKE derivatives).
var debianFamily = []string{"debian", "ubuntu"}

// Resolutions is the closed catalog, ordered by the check they answer.
func Resolutions() []Resolution {
	return []Resolution{
		{
			ID: "docker-log-rotation", Title: "Bound container log growth",
			AppliesTo: "host-storage", Mode: ModeApply,
			Summary: "Container logs grow without limit under the default json-file driver until they fill the disk.",
			Files: []FileChange{{
				Path: "/etc/docker/daemon.json", Mode: 0o644, Backup: true,
				Merge: map[string]any{
					"log-driver": "json-file",
					"log-opts":   map[string]any{"max-size": "10m", "max-file": "3"},
				},
			}},
			Commands: [][]string{{"systemctl", "restart", "docker"}},
			Guidance: []string{
				"Restarting the Docker daemon restarts every running container, so run this before an Apply rather than during one.",
				"The previous daemon.json is kept beside it as daemon.json.stackkit-backup.",
			},
			RequiresRoot: true, Reversible: true, AutoInstallerEligible: true,
		},
		{
			ID: "swap-zram", Title: "Add 2 GB of swap",
			AppliesTo: "host-swap", Mode: ModeApply,
			Summary: "A host this small with no swap has nothing between a memory spike and the OOM killer.",
			Files: []FileChange{{
				Path: "/etc/fstab", Mode: 0o644, Backup: true,
				Append: "/swapfile none swap sw 0 0\n", AppendUnlessPresent: "/swapfile",
			}},
			Commands: [][]string{
				{"fallocate", "-l", "2G", "/swapfile"},
				{"chmod", "600", "/swapfile"},
				{"mkswap", "/swapfile"},
				{"swapon", "/swapfile"},
			},
			Guidance: []string{
				"Swap on an SD card wears it out faster; on a Raspberry Pi prefer zram or an SSD.",
				"Undo with swapoff /swapfile and rm /swapfile, then drop the /swapfile line from /etc/fstab.",
			},
			RequiresRoot: true, Reversible: true, AutoInstallerEligible: true,
		},
		{
			ID: "resolved-stub-listener", Title: "Preserve the existing DNS listener",
			AppliesTo: "host-ports", Mode: ModeHint,
			Summary: "An occupied listener belongs to its existing owner until a reviewed change transfers it.",
			Guidance: []string{
				"Keep systemd-resolved and other existing services running.",
				"Choose a module-supported binding or another target and regenerate the plan, or review an explicit migration with the existing owner.",
			},
		},
		{
			ID: "pi-cgroup-memory", Title: "Enable the kernel memory controller",
			AppliesTo: "cgroup-memory-controller", Mode: ModeHint,
			Summary: "The memory cgroup controller is disabled, so container memory limits are ignored.",
			Guidance: []string{
				"Enable the controller on this kernel: add cgroup_enable=memory cgroup_memory=1 to the kernel command line on Debian/Raspberry Pi OS, or the equivalent sysfs/cmdline on other distros.",
				"The change takes effect only after a reboot, which is why StackKits will not make it for you.",
				"Until then the rollout still succeeds; the limits it renders just do not bind.",
			},
			RequiresRoot: true, RequiresReboot: true, Reversible: true,
		},
		{
			ID: "registry-mirror", Title: "Pull images through a registry mirror",
			AppliesTo: "container-runtime", Mode: ModeHint,
			Summary: "A rate-limited or unreachable registry can be bypassed through a mirror you control.",
			Guidance: []string{
				"Add your mirror to registry-mirrors in /etc/docker/daemon.json and restart the daemon.",
				"StackKits will not choose a mirror for you: which registry your images may come from is a trust decision.",
			},
			RequiresRoot: true, Reversible: true,
		},
		{
			ID: "docker-login-hint", Title: "Authenticate to the image registry",
			AppliesTo: "container-runtime", Mode: ModeHint,
			Summary: "Anonymous pulls are rate limited, and private images need credentials.",
			Guidance: []string{
				"Run docker login yourself, then retry with stackkit apply.",
				"StackKits never handles registry credentials.",
			},
		},
		{
			ID: "nvidia-container-toolkit", Title: "Let containers use the NVIDIA GPU",
			AppliesTo: CheckGPUContainerAccess, Mode: ModeApply,
			Summary: "A selected NVIDIA accelerator profile needs the NVIDIA Container Toolkit and a CDI spec for nvidia.com/gpu.",
			Files: []FileChange{{
				Path: nvidiaContainerToolkitSourceList, Mode: 0o644,
				Content: "deb [signed-by=" + nvidiaContainerToolkitKeyring + "] https://nvidia.github.io/libnvidia-container/stable/deb/$(ARCH) /\n",
			}},
			Commands: [][]string{
				{"curl", "-fsSL", "-o", nvidiaContainerToolkitKeyring, "https://nvidia.github.io/libnvidia-container/gpgkey"},
				{"apt-get", "update"},
				{"apt-get", "install", "-y", "nvidia-container-toolkit"},
				{"nvidia-ctk", "cdi", "generate", "--output=/etc/cdi/nvidia.yaml"},
			},
			Guidance: []string{
				"Debian and Ubuntu hosts only; on other distributions follow NVIDIA's Container Toolkit install guide, then run nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml.",
				"The NVIDIA driver 550 or newer must already be installed: StackKits never installs GPU drivers.",
				"Regenerate the CDI spec after every driver upgrade with the same nvidia-ctk command, or containers lose the GPU.",
				"Undo with apt-get remove nvidia-container-toolkit and by deleting /etc/cdi/nvidia.yaml, the key and the source list.",
			},
			RequiresRoot: true, Reversible: true, AutoInstallerEligible: true,
		},
		{
			ID: "gvisor-runsc", Title: "Register the gVisor sandbox runtime",
			AppliesTo: CheckSandboxRuntime, Mode: ModeApply,
			Summary:       "The selected agent harness runs its container under gVisor (runsc), which Docker on this host has not registered.",
			Distributions: debianFamily,
			Files: []FileChange{
				{Path: gvisorKeyring, Mode: 0o644, Content: gvisorArchiveKey},
				{Path: gvisorSourceList, Mode: 0o644, Content: "deb [signed-by=" + gvisorKeyring + "] https://storage.googleapis.com/gvisor/releases release main\n"},
				{
					Path: "/etc/docker/daemon.json", Mode: 0o644, Backup: true,
					Merge: map[string]any{"runtimes": map[string]any{"runsc": map[string]any{"path": gvisorRunscPath}}},
				},
			},
			Commands: [][]string{
				{"apt-get", "update"},
				{"apt-get", "install", "-y", "runsc"},
				{"systemctl", "restart", "docker"},
			},
			Guidance: []string{
				"Debian and Ubuntu hosts only; on other distributions install runsc from https://gvisor.dev/docs/user_guide/install/, add {\"runtimes\":{\"runsc\":{\"path\":\"/usr/bin/runsc\"}}} to /etc/docker/daemon.json and restart Docker.",
				"The apt source is signed by the pinned gVisor Authors key (6F1D F85E 3A71 C249 18E7 27D5 6FC6 D554 E32B D943); nothing is downloaded to decide what to trust.",
				"daemon.json keeps every other setting, including other runtimes and CDI; the previous file is kept beside it as daemon.json.stackkit-backup.",
				"Restarting the Docker daemon restarts every running container, so run this before an Apply rather than during one.",
				"Undo with apt-get remove runsc, by removing runtimes.runsc from /etc/docker/daemon.json and by deleting the key and the source list, then restart Docker.",
			},
			RequiresRoot: true, Reversible: true, AutoInstallerEligible: true,
		},
		{
			ID: "proxmox-cpu-host", Title: "Pass the host CPU through to the VM",
			AppliesTo: "cpu-baseline", Mode: ModeHint,
			Summary: "A masked virtual CPU hides instruction sets that some images require.",
			Guidance: []string{
				"Set the VM CPU type to host in Proxmox and restart it.",
				"This is a hypervisor setting outside the guest, so it cannot be changed from inside.",
			},
		},
	}
}

// hostDistributionIDs reads ID and ID_LIKE from /etc/os-release, lower-cased.
func hostDistributionIDs() []string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return nil
	}
	var ids []string
	for _, line := range strings.Split(string(data), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found || (key != "ID" && key != "ID_LIKE") {
			continue
		}
		ids = append(ids, strings.Fields(strings.ToLower(strings.Trim(value, osReleaseQuotes)))...)
	}
	return ids
}

// supportsDistribution reports whether the host's distribution family is one
// the resolution declares. An unrestricted resolution applies anywhere.
func supportsDistribution(resolution Resolution, hostIDs []string) bool {
	if len(resolution.Distributions) == 0 {
		return true
	}
	return slices.ContainsFunc(hostIDs, func(id string) bool { return slices.Contains(resolution.Distributions, id) })
}

// ResolutionByID returns one catalog entry.
func ResolutionByID(id string) (Resolution, bool) {
	for _, resolution := range Resolutions() {
		if resolution.ID == id {
			return resolution, true
		}
	}
	return Resolution{}, false
}

// ResolutionsForReport returns the resolutions that answer what this report
// actually found. A passing check needs no fix, so only warnings, blocks and
// unknowns bring one forward.
func ResolutionsForReport(report Report) []Resolution {
	wanted := make(map[string]bool, len(report.Checks))
	for _, check := range report.Checks {
		switch check.Status {
		case StatusWarning, StatusBlocked, StatusUnknown:
			wanted[check.ID] = true
		}
	}
	matched := make([]Resolution, 0, len(wanted))
	for _, resolution := range Resolutions() {
		if wanted[resolution.AppliesTo] {
			matched = append(matched, resolution)
		}
	}
	return matched
}
