// Package guardenrollment reads the Techstack Guard enrollment of this node:
// the server and worker the Guard was enrolled for. StackKits never imports
// Techstack; it reads the enrollment file the Guard keeps on the host, so a
// credential or a link can be bound to this node's own Guard identity and
// never to one a request names.
package guardenrollment

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
)

// Paths are where a Guard keeps its enrollment; the first readable one wins.
var Paths = []string{
	"/etc/techstack/agent-enrollment.json",
	"/var/lib/techstack/guard/agent-enrollment.json",
}

// ErrAbsent means this node has no readable Guard enrollment.
var ErrAbsent = errors.New("guardenrollment: this node has no Techstack Guard enrollment")

var ref = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// Enrollment is the Guard identity of this node.
type Enrollment struct {
	ServerID string
	WorkerID string
}

// Identity is the Guard identity as one string: "guard:<server>/<worker>".
func (e Enrollment) Identity() string { return "guard:" + e.ServerID + "/" + e.WorkerID }

// Read returns the Guard enrollment of this node, or ErrAbsent.
func Read() (Enrollment, error) {
	for _, path := range Paths {
		file, err := os.Open(path) //nolint:gosec // fixed Guard enrollment paths
		if err != nil {
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, 64<<10))
		_ = file.Close()
		if readErr != nil {
			continue
		}
		var document struct {
			Data struct {
				ServerID string `json:"server_id"`
				WorkerID string `json:"worker_id"`
			} `json:"data"`
			ServerID string `json:"server_id"`
			WorkerID string `json:"worker_id"`
		}
		if json.NewDecoder(bytes.NewReader(raw)).Decode(&document) != nil {
			continue
		}
		enrollment := Enrollment{ServerID: document.ServerID, WorkerID: document.WorkerID}
		if enrollment.ServerID == "" && enrollment.WorkerID == "" {
			enrollment = Enrollment{ServerID: document.Data.ServerID, WorkerID: document.Data.WorkerID}
		}
		if ref.MatchString(enrollment.ServerID) && ref.MatchString(enrollment.WorkerID) {
			return enrollment, nil
		}
	}
	return Enrollment{}, ErrAbsent
}
