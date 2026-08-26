//go:build !linux && !windows && !darwin

package hostconformance

import "fmt"

func platformCPUCores() (int, error) {
	return 0, fmt.Errorf("host CPU core count is unobserved on this OS")
}

func platformMemoryBytes() (uint64, error) {
	return 0, fmt.Errorf("host RAM is unobserved on this OS")
}

func platformStorageBytes() (uint64, error) {
	return 0, fmt.Errorf("host storage is unobserved on this OS")
}
