//go:build !linux

package opentofu

import (
	"errors"
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

const actionLeaseSocketEnv = "STACKKIT_ACTION_LEASE_SOCKET"

type actionLeaseServer struct{}
type actionLease struct{}

func requireActionLeasePlatform() error { return errors.New("Runtipi API action lease requires Linux") }
func newActionLeaseServer(*confinedfs.OutputLockLease, string, string, string) (*actionLeaseServer, error) {
	return nil, requireActionLeasePlatform()
}
func (*actionLeaseServer) Endpoint() string { return "" }
func (*actionLeaseServer) Close()           {}
func receiveActionLease(string, string, string) (*actionLease, error) {
	return nil, requireActionLeasePlatform()
}
func (*actionLease) Close() {}
