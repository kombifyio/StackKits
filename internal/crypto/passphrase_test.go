package crypto

import "testing"

func TestArgon2idHashVerifyRoundtrip(t *testing.T) {
	pass := "correct horse battery staple"
	hash, err := HashPassphrase(pass)
	if err != nil {
		t.Fatal(err)
	}

	if !VerifyPassphrase(pass, hash) {
		t.Error("correct passphrase failed to verify")
	}
	if VerifyPassphrase("wrong passphrase", hash) {
		t.Error("wrong passphrase passed verification")
	}
}

func TestArgon2idEncodingFormat(t *testing.T) {
	hash, err := HashPassphrase("x")
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) < 80 || hash[:10] != "$argon2id$" {
		t.Errorf("unexpected hash format: %q", hash)
	}
}

func TestVerifyMalformedHash(t *testing.T) {
	if VerifyPassphrase("anything", "not-a-valid-hash") {
		t.Error("malformed hash should not verify")
	}
}
