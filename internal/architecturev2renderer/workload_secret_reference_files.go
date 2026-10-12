package architecturev2renderer

import (
	"encoding/base64"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"regexp"
)

// WorkloadSecretReferenceFile delivers one secret environment value of a
// pinned image as a YAML file that the image resolves through a reference.
// The variable receives only the reference; the value reaches the container
// as a Compose secret file outside every data volume. The native renderer
// translates that file like any other Compose secret file (a provider
// upload), so both executions deliver it identically.
type WorkloadSecretReferenceFile struct {
	// Target is the absolute container path of the YAML file.
	Target string
	// Key is the mapping key that holds the value.
	Key string
	// Reference is the literal the secret variable receives.
	Reference string
	// UID and GID own the file (mode 0400) as the image's reader needs.
	UID, GID int
	// BcryptBase64 delivers only a bcrypt verifier, encoded for exact .env transport.
	BcryptBase64 bool
}

// workloadSecretReferenceFiles is the governed inventory, keyed by module,
// component and secret variable. Each entry is verified against the
// upstream source of the pinned image; an image upgrade re-verifies it.
var workloadSecretReferenceFiles = map[string]map[string]map[string]WorkloadSecretReferenceFile{
	"stackkits-adguard-home-runtime": {"adguard-home": {"STACKKITS_OWNER_HASH_FILE": {Target: "/run/secrets/adguard-owner-bcrypt", Reference: "/run/secrets/adguard-owner-bcrypt", BcryptBase64: true}}},
	// zigbee2mqtt 2.14.1 writes every ZIGBEE2MQTT_CONFIG_* variable into
	// configuration.yaml in its data volume on each start
	// (lib/util/settings.ts write), so a plain password variable persists
	// the value there. A `!<file> <key>` value is written as the reference
	// instead and resolved on read from <file> (path.resolve against the
	// data directory, so an absolute path stays outside the volume). An
	// existing configuration.yaml is rewritten with the reference at the
	// next start. The process runs as root.
	"stackkits-zigbee2mqtt-runtime": {"zigbee2mqtt": {"ZIGBEE2MQTT_CONFIG_MQTT_PASSWORD": {
		Target: "/run/secrets/zigbee2mqtt-mqtt.yaml", Key: "mqtt_password",
		Reference: "!/run/secrets/zigbee2mqtt-mqtt.yaml mqtt_password",
	}}},
}

// GovernedWorkloadSecretReferenceFile returns the reference file of one
// secret variable, if the image reads it that way.
func GovernedWorkloadSecretReferenceFile(moduleRef, componentID, environmentName string) (WorkloadSecretReferenceFile, bool) {
	file, ok := workloadSecretReferenceFiles[moduleRef][componentID][environmentName]
	return file, ok
}

// workloadSecretReferenceValuePattern admits values that stay exact both as
// an unquoted private .env value (no quote, space, `#` or `$`) and inside a
// YAML double-quoted scalar without escapes. Custody-generated material is
// base64 and always matches.
var workloadSecretReferenceValuePattern = regexp.MustCompile(`^[A-Za-z0-9+/=._~-]+$`)

// Content is the YAML file for value, one line without a trailing newline.
// A value outside the admitted form fails closed instead of being escaped.
func (f WorkloadSecretReferenceFile) Content(value string) (string, error) {
	if len(value) > 4096 || !workloadSecretReferenceValuePattern.MatchString(value) {
		return "", fail(ErrInvalidPlan, "workloadSecretReferenceFile."+f.Key, "secret value cannot be written exactly as a YAML reference file")
	}
	if f.BcryptBase64 {
		if len(value) < 16 || len(value) > 72 {
			return "", fail(ErrInvalidPlan, "workloadSecretReferenceFile", "bcrypt custody requires 16 to 72 bytes")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(value), bcrypt.DefaultCost)
		if err != nil {
			return "", fail(ErrInvalidPlan, "workloadSecretReferenceFile", "bcrypt custody encoding failed")
		}
		return base64.StdEncoding.EncodeToString(hash), nil
	}
	return fmt.Sprintf("%s: %q", f.Key, value), nil
}

// ContentWithPrevious keeps the existing verifier stable only while it still
// authenticates the current custody material. A changed or invalid verifier is
// regenerated; plaintext never enters the verifier transport.
func (f WorkloadSecretReferenceFile) ContentWithPrevious(value, previous string) (string, error) {
	if f.BcryptBase64 && (len(value) < 16 || len(value) > 72 || !workloadSecretReferenceValuePattern.MatchString(value)) {
		return f.Content(value)
	}
	if f.BcryptBase64 && len(previous) <= 128 && workloadSecretReferenceValuePattern.MatchString(previous) {
		verifier, err := base64.StdEncoding.DecodeString(previous)
		cost, costErr := bcrypt.Cost(verifier)
		if err == nil && costErr == nil && cost == bcrypt.DefaultCost && bcrypt.CompareHashAndPassword(verifier, []byte(value)) == nil {
			return previous, nil
		}
	}
	return f.Content(value)
}
