package backuplifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

func validDigest(value string) bool {
	raw := strings.TrimPrefix(value, "sha256:")
	if len(raw) != sha256.Size*2 || raw == value {
		return false
	}
	_, err := hex.DecodeString(raw)
	return err == nil
}

func digestJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("backuplifecycle: canonical JSON digest: %v", err))
	}
	return digestBytes(encoded)
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
