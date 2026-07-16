//go:build !windows

package main

import "os"

func atomicReplaceProjectedSource(source, target string) error {
	return os.Rename(source, target)
}
