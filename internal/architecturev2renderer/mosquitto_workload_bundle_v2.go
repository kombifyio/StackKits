package architecturev2renderer

import "encoding/json"

// Frozen public0.53.2 v2 recipe: retained applied custody must stay readable
// after the v3 renderer requires a selected profile. These historical values
// do not select or configure current compute profiles.
const (
	mosquittoV2APIVersion  = "stackkit.workload-bundle/v2"
	mosquittoV2Release     = "2.1.2-alpine"
	mosquittoV2ImageRef    = "docker.io/library/eclipse-mosquitto:2.1.2-alpine"
	mosquittoV2ImageDigest = "sha256:38c0da4f2ef84284d47b3b3eeea1cb3bdeabe81ee10caf0cd5c5ff61ee3ea408"
	mosquittoV2CommandJSON = "[\"/bin/sh\",\"-ec\",\"printf 'per_listener_settings true\\\\npersistence true\\\\npersistence_location /mosquitto/data/\\\\nlistener 1883\\\\nallow_anonymous false\\\\npassword_file /mosquitto/data/passwd\\\\nlistener 8080\\\\nprotocol http_api\\\\nallow_anonymous true\\\\n' > /mosquitto/data/mosquitto.conf && password=$(cat \\\"$MQTT_PASSWORD_FILE\\\") && rm -f /mosquitto/data/passwd && mosquitto_passwd -b -c /mosquitto/data/passwd stackkit \\\"$password\\\" && chown mosquitto:mosquitto /mosquitto/data/mosquitto.conf /mosquitto/data/passwd && exec mosquitto -c /mosquitto/data/mosquitto.conf\"]"
)

func validateMosquittoV2RuntimeComponents(components []selectedPaaSRuntimeComponent, path string) ([]selectedPaaSRuntimeComponent, error) {
	var command []string
	if err := json.Unmarshal([]byte(mosquittoV2CommandJSON), &command); err != nil {
		return nil, err
	}
	limits := &selectedPaaSRuntimeLimits{MemoryLimit: "128m", MemoryReservation: "32m"}
	return validateMosquittoRuntimeComponentsAgainst(components, command, limits, mosquittoV2ImageRef, mosquittoV2ImageDigest, path)
}
