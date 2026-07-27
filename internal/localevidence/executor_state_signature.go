package localevidence

const ownerExecutorStateDomain = "stackkit.owner-signed-executor-state-snapshot/v1\x00"

// OwnerExecutorStateSignature authenticates one executor-native recovery
// closure. Its domain is intentionally distinct from backup and restore
// evidence so signatures cannot be replayed across lifecycle phases.
type OwnerExecutorStateSignature struct {
	OwnerRef string `json:"ownerRef"`
	KeyID    string `json:"keyId"`
	Value    string `json:"value"`
}

func SignOwnerExecutorState(
	workspaceRoot string,
	canonicalSnapshot []byte,
) (OwnerExecutorStateSignature, error) {
	value, ownerRef, keyID, err := signOwnerRestore(
		workspaceRoot,
		canonicalSnapshot,
		ownerExecutorStateDomain,
		"executor-state snapshot",
	)
	if err != nil {
		return OwnerExecutorStateSignature{}, err
	}
	return OwnerExecutorStateSignature{OwnerRef: ownerRef, KeyID: keyID, Value: value}, nil
}

func VerifyOwnerExecutorState(
	workspaceRoot string,
	canonicalSnapshot []byte,
	signature OwnerExecutorStateSignature,
) error {
	return verifyOwnerRestore(
		workspaceRoot,
		canonicalSnapshot,
		ownerExecutorStateDomain,
		signature.OwnerRef,
		signature.KeyID,
		signature.Value,
		"executor-state snapshot",
	)
}
