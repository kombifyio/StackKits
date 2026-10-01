package hostsecurity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"
)

// DefaultExceptionsPath is the owner-controlled exceptions file. It is local
// to the host: no service writes it, and consumers of the evidence only read the
// exceptions it reports back.
const DefaultExceptionsPath = "/etc/stackkit/host-security/exceptions.json"

// maxExceptionsBytes bounds the owner file; it is a short list of decisions.
const maxExceptionsBytes = 64 << 10

// Exception is one owner-approved deviation from a baseline control.
type Exception struct {
	Control   string    `json:"control"`
	Owner     string    `json:"owner"`
	Reason    string    `json:"reason"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ExceptionsFile is the stackkit.host-security-exceptions/v1 document.
type ExceptionsFile struct {
	SchemaVersion string      `json:"schema_version"`
	Exceptions    []Exception `json:"exceptions"`
}

// LoadExceptions reads the owner-controlled file. A missing file is no
// exceptions. A file another user could have edited is refused: an exception is
// an owner decision, so its provenance is part of its validity. The notices
// explain anything that was ignored.
func LoadExceptions(host Host, path string) (exceptions []Exception, notices []string) {
	if path == "" {
		return nil, nil
	}
	info, err := host.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []string{"exceptions file could not be inspected and was ignored: " + err.Error()}
	}
	if info.Mode().Perm()&0o022 != 0 {
		return nil, []string{"exceptions file " + path + " is writable by group or others; it was ignored because an exception must be an owner decision"}
	}
	raw, err := host.ReadFile(path)
	if err != nil {
		return nil, []string{"exceptions file could not be read and was ignored: " + err.Error()}
	}
	if len(raw) > maxExceptionsBytes {
		return nil, []string{"exceptions file exceeds its size bound and was ignored"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var file ExceptionsFile
	if err := decoder.Decode(&file); err != nil {
		return nil, []string{"exceptions file is not valid " + ExceptionsSchemaVersion + " and was ignored: " + err.Error()}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, []string{"exceptions file has trailing data and was ignored"}
	}
	if file.SchemaVersion != ExceptionsSchemaVersion {
		return nil, []string{fmt.Sprintf("exceptions file schema %q is not %q and was ignored", file.SchemaVersion, ExceptionsSchemaVersion)}
	}
	return file.Exceptions, nil
}

// applyExceptions turns drifted controls into approved exceptions when an
// unexpired, attributed exception names them. It never applies to a compliant
// control (nothing to approve) and never to an unknown one (an unobserved
// control cannot be approved). Every declared exception is reported.
func applyExceptions(controls []Control, exceptions []Exception, now time.Time) ([]Control, []ExceptionRecord) {
	controls = append([]Control(nil), controls...)
	index := map[string]int{}
	for i, control := range controls {
		index[control.ID] = i
	}
	records := make([]ExceptionRecord, 0, len(exceptions))
	for _, exception := range exceptions {
		record := ExceptionRecord{
			Control: exception.Control, Owner: strings.TrimSpace(exception.Owner),
			Reason: strings.TrimSpace(exception.Reason), ExpiresAt: exception.ExpiresAt.UTC(),
		}
		position, known := index[exception.Control]
		switch {
		case !known:
			record.Status, record.Detail = ExceptionInvalid, "names no control of this baseline"
		case record.Owner == "" || record.Reason == "" || exception.ExpiresAt.IsZero():
			record.Status, record.Detail = ExceptionInvalid, "an exception needs an owner, a reason and an expiry"
		case !now.Before(exception.ExpiresAt):
			record.Status, record.Detail = ExceptionExpired, "expired at "+exception.ExpiresAt.UTC().Format(time.RFC3339)+"; the control is judged on its own"
		case controls[position].State != StateDrifted:
			record.Status, record.Detail = ExceptionUnused, "the control is "+string(controls[position].State)+", so there is nothing to approve"
		default:
			record.Status = ExceptionActive
			controls[position].State = StateException
			controls[position].Exception = &AppliedException{Owner: record.Owner, Reason: record.Reason, ExpiresAt: record.ExpiresAt}
		}
		records = append(records, record)
	}
	return controls, records
}
