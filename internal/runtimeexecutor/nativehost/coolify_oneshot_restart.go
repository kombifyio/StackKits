package nativehost

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// coolifyOneShotRestart pins `restart: "no"` on one-shot services that another
// service waits for with service_completed_successfully. Coolify injects
// `restart: unless-stopped` into every service without a policy, which turns
// a completed init (for example Immich's postgres init) into a restart loop
// that never satisfies its dependent.
func coolifyOneShotRestart(compose []byte) ([]byte, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(compose, &doc); err != nil {
		return nil, err
	}
	services, _ := doc["services"].(map[string]any)
	oneShot := map[string]bool{}
	for _, value := range services {
		service, _ := value.(map[string]any)
		depends, _ := service["depends_on"].(map[string]any)
		for name, condition := range depends {
			entry, _ := condition.(map[string]any)
			if entry["condition"] == "service_completed_successfully" {
				oneShot[name] = true
			}
		}
	}
	if len(oneShot) == 0 {
		return compose, nil
	}
	for name := range oneShot {
		service, ok := services[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Coolify workload dependency %s is not a declared service", name)
		}
		if policy, set := service["restart"]; set && policy != "no" {
			return nil, fmt.Errorf("Coolify one-shot service %s declares restart %v", name, policy)
		}
		service["restart"] = "no"
	}
	return yaml.Marshal(doc)
}
