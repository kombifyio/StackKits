//go:build !linux

package runtimeprofile

func InvalidateRILAuthority() error { return nil }

type RILEvidence struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

func RILWorkspaceConfigured() bool                                                 { return false }
func RILCanonicalWorkspace(string) bool                                            { return false }
func ReadRILEvidence(string, string) ([]byte, error)                               { return nil, nil }
func PublishRILAuthority(string, string, string, string, []string, [][]byte) error { return nil }

func BeginRILAuthorityApply() (func(), error) { return func() {}, nil }
