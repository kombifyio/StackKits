package backuphooks

import _ "embed"

// dbHooksCUE is the package-local database quiesce-hook contract. Keeping the
// embed beside its only consumer makes the public Go package self-contained
// without exporting the private add-on coating.
//
//go:embed db-hooks.cue
var dbHooksCUE []byte
