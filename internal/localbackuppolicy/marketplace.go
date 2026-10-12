package localbackuppolicy

// Marketplace keeps its installed project and volume identities when it joins
// the existing application backup graph. Only this native node installation
// uses the fixed project; adapter workloads retain their node-qualified names.
const MarketplaceModuleRef = "stackkits-marketplace-coolify-runtime"
const MarketplaceComposeProject = "stackkit-marketplace"

func ApplicationComposeProjectName(workloadRef, nodeRef string) string {
	if workloadRef == "marketplace" {
		return MarketplaceComposeProject
	}
	return StandaloneComposeProjectName(workloadRef, nodeRef)
}

func ApplicationLogicalVolumeName(workloadRef, componentRef, volumeRef string) string {
	if workloadRef == "marketplace" {
		return "marketplace-" + volumeRef
	}
	return StandaloneComposeLogicalVolumeName(componentRef, volumeRef)
}

func ApplicationVolumeName(workloadRef, nodeRef, componentRef, volumeRef string) string {
	return ApplicationComposeProjectName(workloadRef, nodeRef) + "_" +
		ApplicationLogicalVolumeName(workloadRef, componentRef, volumeRef)
}

// MarketplaceVolumeTarget is the closed persistent installation layout. The
// compiler and native recipe both use it; a policy cannot add a similarly
// named volume or move a selected source to another container path.
func MarketplaceVolumeTarget(componentRef, volumeRef string) (string, bool) {
	switch componentRef {
	case "postgres":
		if volumeRef == "postgres" {
			return "/var/lib/postgresql/data", true
		}
	case "redis":
		if volumeRef == "redis" {
			return "/data", true
		}
	case "coolify":
		switch volumeRef {
		case "ssh", "applications", "databases", "services", "backups", "images":
			return "/var/www/html/storage/app/" + volumeRef, true
		}
	}
	return "", false
}
