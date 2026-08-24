//go:build windows

package hostpreflight

// freeBytes is unavailable on Windows. Apply targets are Linux hosts; the CLI
// runs on Windows only for authoring, where a container-storage measurement
// would describe the wrong machine. Reporting it unobserved keeps the report
// honest instead of measuring something irrelevant.
func freeBytes(string) (uint64, bool) { return 0, false }
