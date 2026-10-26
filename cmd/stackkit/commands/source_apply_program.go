package commands

import (
	"errors"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/generationartifact"
)

// This handle can be obtained only after the original canonical Apply result
// passed the existing Owner/member receipt gate for the exact generation.
// It proves recorded program identity, not installation permission or Apply
// readiness under the current compiler.
type verifiedOriginalApplyProgram struct {
	record architecturev2.ApplyProgramRecord
}

func (proof verifiedOriginalApplyProgram) Program() (architecturev2.ApplyProgramIdentity, error) {
	if proof.record.Program == nil || proof.record.ResultHash == "" || proof.record.Program.ProcessRole != "stackkit-cli" {
		return architecturev2.ApplyProgramIdentity{}, errors.New("original Apply executable identity is unproven")
	}
	program := *proof.record.Program
	if program.PreviewCandidate != nil {
		source := *program.PreviewCandidate
		program.PreviewCandidate = &source
	}
	return program, nil
}

func (proof verifiedOriginalApplyProgram) ApplyResultHash() string { return proof.record.ResultHash }

func readVerifiedOriginalApplyProgram(workspace string, binding generationartifact.PlanBinding, manifestHash, generationReceiptHash string) (verifiedOriginalApplyProgram, error) {
	if manifestHash == "" || generationReceiptHash == "" {
		return verifiedOriginalApplyProgram{}, errors.New("original program inspection requires exact manifest and generation receipt hashes")
	}
	record, err := readCurrentArchitectureV2ApplyEvidence(workspace, binding, func(raw []byte) (architecturev2.ApplyProgramRecord, currentApplyEvidenceIdentity, error) {
		data, err := architecturev2.InspectApplyProgramRecord(raw)
		if err != nil {
			return architecturev2.ApplyProgramRecord{}, currentApplyEvidenceIdentity{}, err
		}
		if data.Binding != binding || data.ManifestHash != manifestHash || data.GenerationReceiptHash != generationReceiptHash {
			return architecturev2.ApplyProgramRecord{}, currentApplyEvidenceIdentity{}, errApplyEvidenceOtherGeneration
		}
		return data, currentApplyEvidenceIdentity{ResultHash: data.ResultHash, AppliedAt: data.AppliedAt}, nil
	})
	if err != nil {
		return verifiedOriginalApplyProgram{}, err
	}
	proof := verifiedOriginalApplyProgram{record: record}
	if _, err := proof.Program(); err != nil {
		return verifiedOriginalApplyProgram{}, err
	}
	return proof, nil
}
