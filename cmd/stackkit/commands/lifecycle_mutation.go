package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
)

type publicUpgradeLifecycleSession interface {
	BeginJoin(string, string, string, string, string) (string, error)
	Transition(string, string) error
	Complete(string) error
	Close() error
	Record() lifecyclemutation.Record
}

func currentLifecycleMutationJoin(command string) (lifecyclemutation.JoinRequest, error) {
	join := lifecyclemutation.JoinRequest{
		OperationID:   strings.TrimSpace(lifecycleJoinOperation),
		Phase:         strings.TrimSpace(lifecycleJoinPhase),
		BinaryVersion: version,
		Nonce:         strings.TrimSpace(lifecycleJoinNonce),
		Command:       command,
	}
	if join.OperationID == "" && join.Phase == "" && join.Nonce == "" {
		return join, nil
	}
	executableDigest, err := lifecyclemutation.CurrentExecutableSHA256()
	if err != nil {
		return lifecyclemutation.JoinRequest{}, err
	}
	join.ExecutableSHA256 = executableDigest
	return join, nil
}

func admitLifecycleMutationBeforeObservability(
	workspace, command string,
	mutating bool,
) error {
	join, err := currentLifecycleMutationJoin(command)
	if err != nil {
		return err
	}
	if join.OperationID != "" || join.Phase != "" {
		return lifecyclemutation.InspectJoin(workspace, join)
	}
	if mutating {
		return lifecyclemutation.RequireIdle(workspace)
	}
	return nil
}

func withLifecycleMutation(
	workspace, command string,
	execute func() error,
) error {
	join, err := currentLifecycleMutationJoin(command)
	if err != nil {
		return err
	}
	return lifecyclemutation.WithIdleMutation(workspace, join, execute)
}

func withLifecycleJoinIfPresent(
	workspace, command string,
	execute func() error,
) error {
	join, err := currentLifecycleMutationJoin(command)
	if err != nil {
		return err
	}
	if join.OperationID == "" && join.Phase == "" && join.Nonce == "" {
		return execute()
	}
	return lifecyclemutation.WithIdleMutation(workspace, join, execute)
}

func lifecycleChildFlags(operationID, phase, nonce string) []string {
	return []string{
		"--internal-lifecycle-operation", operationID,
		"--internal-lifecycle-phase", phase,
		"--internal-lifecycle-nonce", nonce,
	}
}

func executableFileSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
