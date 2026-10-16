package architecturev2renderer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

func (r *nativeDockerRender) secretEnvironment(servicePath, name string, secrets []NativeDockerSecretBinding, environment *[]string) ([]NativeDockerSecretFile, error) {
	if len(secrets) == 0 {
		return nil, nil
	}
	reader, governed := r.readers[name]
	if !governed {
		return nil, fail(ErrRendererFailure, servicePath+".environment", "service has secret values but no governed file-based secret input")
	}
	var files []NativeDockerSecretFile
	switch {
	case reader.EnvFile != "":
		files = append(files, NativeDockerSecretFile{
			Service: name, RelPath: NativeDockerSecretDir + "/" + name + ".env",
			Format: NativeDockerSecretFormatDotenv, Bindings: secrets,
		})
		*environment = append(*environment, reader.EnvFile+"="+nativeDockerSecretMountDir+"/"+name+".env")
	case reader.FileSuffix || reader.Shim != nil:
		for _, binding := range secrets {
			if reader.Shim != nil {
				// The shim reads the file; the container gets no variable.
				files = append(files, NativeDockerSecretFile{
					Service: name, RelPath: NativeDockerSecretDir + "/" + name + "." + binding.Name,
					Format: NativeDockerSecretFormatValue, Bindings: []NativeDockerSecretBinding{binding},
				})
				continue
			}
			files = append(files, NativeDockerSecretFile{
				Service: name, RelPath: NativeDockerSecretDir + "/" + name + "." + binding.Name,
				Format: NativeDockerSecretFormatValue, Bindings: []NativeDockerSecretBinding{binding},
			})
			*environment = append(*environment, binding.Name+"_FILE="+nativeDockerSecretMountDir+"/"+name+"."+binding.Name)
		}
	case len(reader.Files) > 0:
		readerFiles, err := nativeReaderFiles(servicePath, name, reader, secrets)
		if err != nil {
			return nil, err
		}
		files = append(files, readerFiles...)
		for _, variable := range sortedKeys(reader.Environment) {
			*environment = append(*environment, variable+"="+reader.Environment[variable])
		}
	default:
		return nil, fail(ErrRendererFailure, servicePath+".environment", "governed secret reader declares no form")
	}
	seen := map[string]bool{}
	for _, entry := range *environment {
		variable, _, _ := strings.Cut(entry, "=")
		if seen[variable] {
			return nil, fail(ErrRendererFailure, servicePath+".environment", "secret file variable %s conflicts with a declared variable", variable)
		}
		seen[variable] = true
	}
	sort.Strings(*environment)
	return files, nil
}

var (
	nativeSecretTargetPattern = regexp.MustCompile(`^(/[A-Za-z0-9._-]+)+$`)
	nativeSecretKeyPattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}(\.[A-Za-z_][A-Za-z0-9_]{0,63}){0,3}$`)
)

// nativeReaderFiles binds the secret values of one container to the
// fixed-path files of its Files reader. Every secret is bound by exactly
// one file and every file variable is a secret of the container, so a
// catalog change on either side fails closed instead of losing a value.
//
//nolint:gocyclo // moved unchanged from compose_native_docker_opentofu.go; one case per secret reader
func nativeReaderFiles(servicePath, name string, reader nativeDockerSecretReader, secrets []NativeDockerSecretBinding) ([]NativeDockerSecretFile, error) {
	byName := map[string]NativeDockerSecretBinding{}
	for _, binding := range secrets {
		byName[binding.Name] = binding
	}
	bound := map[string]bool{}
	targets := map[string]bool{}
	var files []NativeDockerSecretFile
	for index, spec := range reader.Files {
		specPath := fmt.Sprintf("%s.secretReader.files[%d]", servicePath, index)
		if !nativeSecretTargetPattern.MatchString(spec.Target) || path.Clean(spec.Target) != spec.Target || targets[spec.Target] ||
			!nativeNumericIDPattern.MatchString(spec.UID) || !nativeNumericIDPattern.MatchString(spec.GID) ||
			spec.Mode <= 0 || spec.Mode > 0o777 || len(spec.Variables) == 0 ||
			(len(spec.Keys) != 0 && len(spec.Keys) != len(spec.Variables)) {
			return nil, fail(ErrRendererFailure, specPath, "secret file needs an exact path, owner, mode and variables")
		}
		targets[spec.Target] = true
		single := spec.Format == NativeDockerSecretFormatValue || spec.Format == NativeDockerSecretFormatPgpass
		switch {
		case single && (len(spec.Variables) != 1 || len(spec.Keys) != 0):
			return nil, fail(ErrRendererFailure, specPath, "a %s file holds exactly one value", spec.Format)
		case spec.Format == NativeDockerSecretFormatYAML && len(spec.Keys) != len(spec.Variables):
			return nil, fail(ErrRendererFailure, specPath, "a YAML file names a key for every value")
		case !single && spec.Format != NativeDockerSecretFormatYAML && spec.Format != NativeDockerSecretFormatDotenv:
			return nil, fail(ErrRendererFailure, specPath, "unknown secret file format %q", spec.Format)
		}
		file := NativeDockerSecretFile{
			Service: name, RelPath: NativeDockerSecretDir + "/" + name + ".file-" + strconv.Itoa(index), Format: spec.Format,
			Uploaded: true, Target: spec.Target, Owner: spec.UID, Group: spec.GID, Permissions: spec.Mode,
		}
		for position, variable := range spec.Variables {
			binding, secret := byName[variable]
			if !secret && slices.Contains(spec.Optional, variable) {
				continue
			}
			if !secret || bound[variable] {
				return nil, fail(ErrRendererFailure, specPath, "variable %s is not an unbound secret of the container", variable)
			}
			bound[variable] = true
			if len(spec.Keys) != 0 {
				if !nativeSecretKeyPattern.MatchString(spec.Keys[position]) {
					return nil, fail(ErrRendererFailure, specPath, "key %q is not a plain key path", spec.Keys[position])
				}
				binding.Name = spec.Keys[position]
			}
			file.Bindings = append(file.Bindings, binding)
		}
		if len(file.Bindings) > 0 {
			files = append(files, file)
		}
	}
	for _, binding := range secrets {
		if !bound[binding.Name] {
			return nil, fail(ErrRendererFailure, servicePath+".environment."+binding.Name, "secret has no file of the governed reader")
		}
	}
	for variable, value := range reader.Environment {
		if !nativeSecretVariablePattern.MatchString(variable) || strings.ContainsAny(value, "\x00\r\n") {
			return nil, fail(ErrRendererFailure, servicePath+".secretReader.environment", "reader variable %s is not a plain literal", variable)
		}
	}
	return files, nil
}

// secretUploads renders the Compose secret files of one container. Compose
// copies an environment-sourced secret into the container with its owner and
// mode; the provider's upload does the same from the owner-only file the
// executor writes. The root and state hold the path and the file digest only.
func (r *nativeDockerRender) secretUploads(servicePath, name string, block *hclwrite.Body, placements []WorkloadComposeServiceSecret) ([]NativeDockerSecretFile, error) {
	var files []NativeDockerSecretFile
	targets := map[string]bool{}
	for _, placement := range placements {
		secret, declared := r.document.Secrets[placement.Source]
		if !declared || !nativeIdentifierPattern.MatchString(placement.Source) || !nativeSecretVariablePattern.MatchString(secret.Environment) {
			return nil, fail(ErrRendererFailure, servicePath+".secrets", "secret %q is not a declared environment-sourced secret", placement.Source)
		}
		if !path.IsAbs(placement.Target) || path.Clean(placement.Target) != placement.Target || targets[placement.Target] ||
			!nativeNumericIDPattern.MatchString(placement.UID) || !nativeNumericIDPattern.MatchString(placement.GID) ||
			placement.Mode < 0 || placement.Mode > 0o777 {
			return nil, fail(ErrRendererFailure, servicePath+".secrets."+placement.Source, "secret file placement is not an exact path, owner and mode")
		}
		targets[placement.Target] = true
		file := NativeDockerSecretFile{
			Service: name, RelPath: NativeDockerSecretDir + "/" + name + ".secret-" + placement.Source,
			Format: NativeDockerSecretFormatValue, Uploaded: true,
			Bindings: []NativeDockerSecretBinding{{Name: placement.Source, Variable: secret.Environment}},
			Target:   placement.Target, Owner: placement.UID, Group: placement.GID, Permissions: placement.Mode,
		}
		appendNativeUpload(block, file)
		files = append(files, file)
	}
	return files, nil
}

// RenderNativeDockerSecretFile renders one secret file from the private .env
// the native preparation wrote. A dotenv file single-quotes each value
// (dotenv literal form); a value file holds exactly the one value without a
// newline (some readers keep a trailing newline). A value that cannot be
// expressed exactly fails closed instead of being reinterpreted.
//
//nolint:gocyclo // moved unchanged from compose_native_docker_opentofu.go; one case per secret reader
func RenderNativeDockerSecretFile(file NativeDockerSecretFile, dotenv []byte) ([]byte, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(dotenv))
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		variable, value, ok := strings.Cut(line, "=")
		if !ok || variable == "" {
			return nil, fail(ErrRendererFailure, "renderer.native-docker.env", "private .env has a malformed line")
		}
		values[variable] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.native-docker.env", "read private .env", err)
	}
	var out bytes.Buffer
	tree := nativeYAMLNode{}
	for _, binding := range file.Bindings {
		value, ok := values[binding.Variable]
		if !ok || value == "" {
			return nil, fail(ErrRendererFailure, "renderer.native-docker.env", "private .env has no value for a bound secret")
		}
		switch file.Format {
		case NativeDockerSecretFormatYAML:
			if strings.ContainsAny(value, "\r\n\x00") || !tree.set(strings.Split(binding.Name, "."), value) {
				return nil, fail(ErrRendererFailure, "renderer.native-docker.env", "secret value cannot be written as a YAML scalar")
			}
		case NativeDockerSecretFormatPgpass:
			// libpq: hostname:port:database:username:password, `*` matches
			// anything, and `\` escapes a colon or backslash in a field.
			if len(file.Bindings) != 1 || strings.ContainsAny(value, "\r\n\x00") {
				return nil, fail(ErrRendererFailure, "renderer.native-docker.env", "secret value cannot be written as a password file line")
			}
			out.WriteString("*:*:*:*:" + strings.NewReplacer(`\`, `\\`, `:`, `\:`).Replace(value) + "\n")
		case NativeDockerSecretFormatValue:
			if len(file.Bindings) != 1 || strings.ContainsAny(value, "\r\n\x00") || strings.TrimSpace(value) != value {
				return nil, fail(ErrRendererFailure, "renderer.native-docker.env", "secret value cannot be written as an exact value file")
			}
			out.WriteString(value)
		case NativeDockerSecretFormatDotenv:
			if strings.ContainsAny(value, "'\r\n\x00") {
				return nil, fail(ErrRendererFailure, "renderer.native-docker.env", "secret value cannot be written as a literal dotenv value")
			}
			fmt.Fprintf(&out, "%s='%s'\n", binding.Name, value)
		default:
			return nil, fail(ErrRendererFailure, "renderer.native-docker.env", "unknown secret file format")
		}
	}
	if file.Format == NativeDockerSecretFormatYAML {
		tree.write(&out, 0)
	}
	return out.Bytes(), nil
}

// nativeYAMLNode is a mapping of a YAML secret file: a key holds either a
// string scalar or a nested mapping.
type nativeYAMLNode map[string]any

// set stores value under the key path; a path that collides with another
// value or mapping is refused.
func (n nativeYAMLNode) set(keys []string, value string) bool {
	if len(keys) == 1 {
		if _, taken := n[keys[0]]; taken {
			return false
		}
		n[keys[0]] = value
		return true
	}
	child, exists := n[keys[0]]
	if !exists {
		child = nativeYAMLNode{}
		n[keys[0]] = child
	}
	mapping, ok := child.(nativeYAMLNode)
	return ok && mapping.set(keys[1:], value)
}

// write emits the mapping in key order, each scalar double-quoted with JSON
// escapes (a JSON string is a YAML double-quoted scalar).
func (n nativeYAMLNode) write(out *bytes.Buffer, depth int) {
	for _, key := range sortedKeys(n) {
		out.WriteString(strings.Repeat("  ", depth) + key + ":")
		switch value := n[key].(type) {
		case nativeYAMLNode:
			out.WriteString("\n")
			value.write(out, depth+1)
		case string:
			var quoted bytes.Buffer
			encoder := json.NewEncoder(&quoted)
			encoder.SetEscapeHTML(false)
			_ = encoder.Encode(value)
			out.WriteString(" " + strings.TrimSuffix(quoted.String(), "\n") + "\n")
		}
	}
}
