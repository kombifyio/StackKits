package tofu

import (
	"encoding/hex"
	"fmt"
)

// StateEncryptionConfig supplies the common owner-bound state and plan policy.
// Plaintext fallback is permitted only for the isolated legacy migration.
func StateEncryptionConfig(key []byte, migration bool) string {
	fallback := ""
	unencrypted := ""
	if migration {
		unencrypted = "method \"unencrypted\" \"stackkit_migration\" {}\n"
		fallback = "\n  fallback { method = method.unencrypted.stackkit_migration }"
	}
	return fmt.Sprintf(`key_provider "pbkdf2" "stackkit_root" {
	passphrase = %q
}
method "aes_gcm" "stackkit_root" {
	keys = key_provider.pbkdf2.stackkit_root
}
%sstate {
  method = method.aes_gcm.stackkit_root%s
  enforced = %t
}
plan {
  method = method.aes_gcm.stackkit_root
  enforced = true
}
`, hex.EncodeToString(key), unencrypted, fallback, !migration)
}
