package crypto

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	plaintext := []byte("secret bundle payload\nline 2\n")
	pass := "correct horse battery staple"

	encrypted, err := EncryptWithPassphrase(plaintext, pass)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(encrypted, plaintext) {
		t.Fatal("not encrypted")
	}

	decrypted, err := DecryptWithPassphrase(encrypted, pass)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Errorf("got %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptWrongPassphrase(t *testing.T) {
	encrypted, err := EncryptWithPassphrase([]byte("secret"), "right")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptWithPassphrase(encrypted, "wrong"); err == nil {
		t.Error("wrong passphrase should fail")
	}
}
