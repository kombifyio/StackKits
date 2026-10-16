package coolify

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mtalavi/coolify-mirror/internal/run"
)

//go:embed runner.php
var runnerPHP string

const resultMarker = "@@CM_RESULT@@"

// PHP runs one action of the embedded helper inside the Coolify container and
// decodes its JSON result into out (may be nil).
func (in *Instance) PHP(ctx context.Context, action string, input any, out any) error {
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	stdout, err := run.Do(ctx, run.Spec{
		Name: "docker",
		Args: []string{"exec", "-i", "-u", "www-data",
			"-e", "CM_ACTION=" + action,
			"-e", "CM_INPUT=" + base64.StdEncoding.EncodeToString(payload),
			AppContainer, "php"},
		Stdin: strings.NewReader(runnerPHP),
	})
	idx := bytes.LastIndex(stdout, []byte(resultMarker))
	if idx < 0 {
		if err != nil {
			return fmt.Errorf("coolify helper (%s): %w", action, err)
		}
		return fmt.Errorf("coolify helper (%s) returned no result: %s", action, strings.TrimSpace(tail(string(stdout), 800)))
	}
	line := stdout[idx+len(resultMarker):]
	if nl := bytes.IndexByte(line, '\n'); nl >= 0 {
		line = line[:nl]
	}
	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if jerr := json.Unmarshal(line, &envelope); jerr != nil {
		return fmt.Errorf("coolify helper (%s): bad result: %v", action, jerr)
	}
	if !envelope.OK {
		return errors.New("coolify: " + envelope.Error)
	}
	if out != nil {
		if jerr := json.Unmarshal(line, out); jerr != nil {
			return fmt.Errorf("coolify helper (%s): %v", action, jerr)
		}
	}
	return nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// EnsureLocalServer makes sure Coolify considers its own server usable,
// running Coolify's validation when it does not (fresh installs).
func (in *Instance) EnsureLocalServer(ctx context.Context) error {
	var out struct {
		Before     bool   `json:"before"`
		Functional bool   `json:"functional"`
		Error      string `json:"validation_error"`
	}
	if err := in.PHP(ctx, "validate_local", nil, &out); err != nil {
		return err
	}
	if !out.Functional {
		msg := "Coolify cannot use this server yet"
		if out.Error != "" {
			msg += ": " + out.Error
		}
		return errors.New(msg + " - open Coolify → Servers → localhost → Validate, then run the restore again")
	}
	return nil
}

// CheckCrypto verifies both directions of APP_KEY compatibility against
// Laravel running in the Coolify container.
func (in *Instance) CheckCrypto(ctx context.Context) error {
	plain := "coolify-mirror-check-" + base64.RawURLEncoding.EncodeToString([]byte(in.Hostname))
	enc, err := in.Crypt.EncryptString([]byte(plain))
	if err != nil {
		return err
	}
	var out struct {
		Decrypted string `json:"decrypted"`
		Encrypted string `json:"encrypted"`
	}
	if err := in.PHP(ctx, "crypt_check", map[string]string{"payload": enc, "plain": plain}, &out); err != nil {
		return err
	}
	if out.Decrypted != plain {
		return errors.New("Laravel decrypted a different value than expected")
	}
	back, err := in.Crypt.DecryptString(out.Encrypted)
	if err != nil {
		return fmt.Errorf("cannot decrypt a value produced by Coolify: %w", err)
	}
	if string(back) != plain {
		return errors.New("round trip mismatch")
	}
	return nil
}

// TransferBundle builds Coolify's own Server Transfer bundle (schema_version
// 1) for the given resources of this server, using Coolify's exporter code.
// It returns nil when this Coolify has no Server Transfer support.
func (in *Instance) TransferBundle(ctx context.Context, uuids []string) (json.RawMessage, error) {
	var out struct {
		Supported bool            `json:"supported"`
		Bundle    json.RawMessage `json:"bundle"`
	}
	if err := in.PHP(ctx, "transfer_bundle", map[string]any{"uuids": uuids}, &out); err != nil {
		return nil, err
	}
	if !out.Supported {
		return nil, nil
	}
	return out.Bundle, nil
}

// BundleCheck is the result of Coolify's own bundle validation.
type BundleCheck struct {
	Supported bool     `json:"supported"`
	Valid     bool     `json:"valid"`
	Errors    []string `json:"errors"`
	Warnings  []string `json:"warnings"`
	UUIDs     []string `json:"uuids"`
}

// ValidateBundle runs ServerTransferBundle::validate on a bundle. The bundle
// holds secrets, so it is handed over as a private file, never as an argument.
func (in *Instance) ValidateBundle(ctx context.Context, bundle []byte) (*BundleCheck, error) {
	file := "/tmp/cm-bundle-" + randomHex(8) + ".json"
	if _, err := run.Do(ctx, run.Spec{Name: "docker", Args: []string{"exec", "-i", "-u", "www-data", AppContainer,
		"sh", "-c", "umask 077 && cat > " + file}, Stdin: bytes.NewReader(bundle)}); err != nil {
		return nil, fmt.Errorf("hand the transfer bundle to Coolify: %w", err)
	}
	var out BundleCheck
	if err := in.PHP(ctx, "transfer_validate", map[string]string{"file": file}, &out); err != nil {
		_, _ = run.Output(context.Background(), "docker", "exec", AppContainer, "rm", "-f", file)
		return nil, err
	}
	return &out, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
