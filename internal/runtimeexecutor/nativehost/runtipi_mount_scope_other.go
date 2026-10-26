//go:build !linux

package nativehost

import (
	"context"
	"errors"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/docker"
)

type runtipiWriterScope struct{}

func newRuntipiWriterScope(context.Context, []backuplifecycle.AppGroupVolume) (*runtipiWriterScope, error) {
	return nil, errors.New("native writable-source identity requires qualified Linux daemon namespace")
}
func (*runtipiWriterScope) overlaps(string) (bool, error) {
	return false, errors.New("native writable-source identity unavailable")
}
func (*runtipiWriterScope) verify(context.Context, *docker.ExecHandleClient) error {
	return errors.New("native writable-source identity unavailable")
}

func (*runtipiWriterScope) overlapsMount(context.Context, *docker.ExecHandleClient, string, string, string, bool) (bool, error) {
	return false, errors.New("native kernel mount identity unavailable")
}
