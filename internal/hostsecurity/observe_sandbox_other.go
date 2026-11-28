//go:build !linux || (!amd64 && !arm64)

package hostsecurity

func runObserveSandbox(string) int { return 1 }
