package nativehost

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// platformSecretEnvFile maps container variables to the node's secret values
// for a Coolify-managed workload. platformNodeCompose drops `${NAME}` entries
// so Coolify never sees or stores them as its own variables; without this file
// the container lost those variables (for example Vaultwarden's
// SSO_CLIENT_SECRET). Coolify reads the file by absolute path on the node, so
// the API content still carries only a private file reference.
const platformSecretEnvFile = ".env.platform"

var platformSecretReference = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)(?::[?-][^}]*)?\}$`)

// coolifyNodeCompose renders the platform Compose for the Coolify API path and
// writes the owner-only derived secret environment beside the node `.env`.
func coolifyNodeCompose(p NativeWorkloadCompose) ([]byte, error) {
	var source struct {
		Services map[string]struct {
			Environment map[string]any `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(p.Compose, &source); err != nil {
		return nil, err
	}
	references := map[string]string{}
	for _, service := range source.Services {
		for key, value := range service.Environment {
			text, ok := value.(string)
			if !ok || !strings.Contains(text, "${") {
				continue
			}
			match := platformSecretReference.FindStringSubmatch(text)
			if match == nil {
				return nil, fmt.Errorf("Coolify workload variable %s uses an unsupported interpolation", key)
			}
			if prior, exists := references[key]; exists && prior != match[1] {
				return nil, fmt.Errorf("Coolify workload variable %s names two node secrets", key)
			}
			references[key] = match[1]
		}
	}
	compose, err := platformNodeCompose(p)
	if err != nil {
		return nil, err
	}
	if compose, err = coolifyOneShotRestart(compose); err != nil {
		return nil, err
	}
	if len(references) == 0 {
		// A revision without secret references must not leave an earlier
		// derived secret file beside the node environment.
		if err := os.Remove(filepath.Join(p.Directory, platformSecretEnvFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return compose, nil
	}
	values, err := readNodeEnvironment(filepath.Join(p.Directory, p.EnvFile))
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(references))
	for key := range references {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var derived bytes.Buffer
	for _, key := range keys {
		value, ok := values[references[key]]
		if !ok {
			return nil, fmt.Errorf("Coolify workload variable %s has no node secret", key)
		}
		// Single quotes keep Compose from interpolating the secret value.
		if strings.ContainsAny(value, "'\r\n\x00") {
			return nil, fmt.Errorf("Coolify workload secret for %s cannot be carried literally", key)
		}
		fmt.Fprintf(&derived, "%s='%s'\n", key, value)
	}
	if err := writeOwnerFileAtomic(filepath.Join(p.Directory, platformSecretEnvFile), derived.Bytes()); err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(compose, &doc); err != nil {
		return nil, err
	}
	services, _ := doc["services"].(map[string]any)
	for _, value := range services {
		service := value.(map[string]any)
		files, _ := service["env_file"].([]any)
		service["env_file"] = append(files, filepath.Join(p.Directory, platformSecretEnvFile))
	}
	return yaml.Marshal(doc)
}

func readNodeEnvironment(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return nil, errors.New("node workload environment is malformed")
		}
		values[key] = value
	}
	return values, scanner.Err()
}

func writeOwnerFileAtomic(path string, content []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
