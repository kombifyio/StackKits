package lifecyclemutation

import (
	"errors"
	"reflect"
	"time"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
)

// CompletionDigest is exactly the original native close request's group
// digest. ACKs are siblings of AppGroups, so recording one cannot change that
// digest or require a different native close on recovery. Absent v1 fields
// remain absent in canonical signing bytes.
type RestoreAppGroupClosure struct {
	GroupID          string `json:"groupId"`
	CompletionDigest string `json:"completionDigest"`
}

func RestoreAppGroupClosuresPending(record Record) bool {
	return record.Kind == KindRestoreActivation && record.RestoreActivation != nil && len(record.RestoreActivation.AppGroups) != len(record.RestoreActivation.AppGroupClosures)
}
func recordBlocksMutation(record Record) bool {
	return record.Status == StatusActive || RestoreAppGroupClosuresPending(record)
}

func validateRestoreAppGroupClosures(record Record) error {
	state := record.RestoreActivation
	if state == nil {
		return nil
	}
	if len(state.AppGroupClosures) == 0 {
		return nil
	}
	if record.Status != StatusSucceeded && record.Status != StatusRecovered {
		return errors.New("restore native close ACK requires terminal owning outcome")
	}
	seen := map[string]bool{}
	for _, ack := range state.AppGroupClosures {
		if seen[ack.GroupID] {
			return errors.New("duplicate native restore close ACK")
		}
		seen[ack.GroupID] = true
		found := false
		for _, g := range state.AppGroups {
			if g.Binding.GroupID == ack.GroupID && ack.CompletionDigest == backuplifecycle.AppGroupDigest(g) && backuplifecycle.AppGroupRestored(g) {
				found = true
			}
		}
		if !found {
			return errors.New("native close ACK differs from original restored signed group")
		}
	}
	return nil
}

func (s *Session) RestoreAppGroupClosed(g backuplifecycle.AppGroupControl) bool {
	if s == nil || s.record.RestoreActivation == nil {
		return false
	}
	for _, ack := range s.record.RestoreActivation.AppGroupClosures {
		if ack.GroupID == g.Binding.GroupID && ack.CompletionDigest == backuplifecycle.AppGroupDigest(g) {
			return true
		}
	}
	return false
}

// AcknowledgeRestoreAppGroupClose is ONLY called after original native close
// ACK/readback. It appends a signed phase at the same terminal status under
// the existing Session/OS lease. Lost native or journal replies replay the
// identical group completion digest; no replacement operation is admitted.
func (s *Session) AcknowledgeRestoreAppGroupClose(g backuplifecycle.AppGroupControl) error {
	if err := s.VerifyRestoreAppGroup(g, true); err != nil {
		return err
	}
	if !backuplifecycle.AppGroupRestored(g) {
		return backuplifecycle.ErrControlUnresolved
	}
	if s.RestoreAppGroupClosed(g) {
		return nil
	}
	current, digest, exists, err := loadRecord(s.workspace, s.transaction)
	if err != nil {
		return err
	}
	if !exists || digest != s.recordDigest || !reflect.DeepEqual(current, s.record) {
		return errors.New("restore close ACK journal preimage changed")
	}
	previous := s.record
	state := *s.record.RestoreActivation
	state.AppGroupClosures = append(append([]RestoreAppGroupClosure(nil), state.AppGroupClosures...), RestoreAppGroupClosure{GroupID: g.Binding.GroupID, CompletionDigest: backuplifecycle.AppGroupDigest(g)})
	candidate := s.record
	candidate.RestoreActivation = &state
	if err = validateRestoreAppGroupClosures(candidate); err != nil {
		return err
	}
	s.record = candidate
	s.record.Sequence++
	s.record.UpdatedAt = time.Now().UTC()
	nextDigest, err := persistRecord(s.workspace, s.transaction, s.view, &s.record, s.recordDigest)
	if nextDigest != "" {
		s.recordDigest = nextDigest
	} else if err != nil {
		s.record = previous
	}
	return err
}

func (s *Session) RequireRestoreAppGroupClosures() error {
	if s == nil || s.transaction == nil {
		return errors.New("restore close requires held Session")
	}
	actual, digest, exists, err := loadRecord(s.workspace, s.transaction)
	if err != nil {
		return err
	}
	if !exists || digest != s.recordDigest || actual.OperationID != s.record.OperationID {
		return errors.New("restore closure journal changed")
	}
	if RestoreAppGroupClosuresPending(actual) {
		return errors.New("original restore retains native close obligations")
	}
	return nil
}
