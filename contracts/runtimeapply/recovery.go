package runtimeapply

import "context"

// RecoveryStore owns opaque canonical recovery-capsule custody by exact
// request digest. The consumer validates bytes before Save and after Load;
// implementations own only atomic persistence and exact-digest lookup.
type RecoveryStore interface {
	SaveApplyRecovery(context.Context, string, []byte) error
	LoadApplyRecovery(context.Context, string) ([]byte, error)
}
