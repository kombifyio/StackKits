package coolify

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// EnvFile is an order and comment preserving view of /data/coolify/source/.env.
type EnvFile struct {
	Path  string
	lines []string
}

// LoadEnv reads an env file from disk.
func LoadEnv(path string) (*EnvFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	e := ParseEnv(b)
	e.Path = path
	return e, nil
}

// ParseEnv parses env file content.
func ParseEnv(b []byte) *EnvFile {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	e := &EnvFile{}
	if s != "" {
		e.lines = strings.Split(s, "\n")
	}
	return e
}

func splitLine(line string) (key, value string, ok bool) {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") {
		return "", "", false
	}
	t = strings.TrimPrefix(t, "export ")
	k, v, found := strings.Cut(t, "=")
	if !found {
		return "", "", false
	}
	k = strings.TrimSpace(k)
	v = strings.TrimSpace(v)
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		v = v[1 : len(v)-1]
	}
	return k, v, k != ""
}

// Get returns the last definition of key (docker compose / Laravel semantics differ;
// the Coolify installer never writes duplicates, so either is fine).
func (e *EnvFile) Get(key string) (string, bool) {
	val, found := "", false
	for _, l := range e.lines {
		if k, v, ok := splitLine(l); ok && k == key {
			val, found = v, true
		}
	}
	return val, found
}

// Value returns Get(key) or def when missing/empty.
func (e *EnvFile) Value(key, def string) string {
	if v, ok := e.Get(key); ok && v != "" {
		return v
	}
	return def
}

// Keys lists defined keys in file order.
func (e *EnvFile) Keys() []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range e.lines {
		if k, _, ok := splitLine(l); ok && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

func quoteEnv(v string) string {
	if v == "" || !strings.ContainsAny(v, " \t#\"'$`\\") {
		return v
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

// Set replaces every definition of key (or appends one).
func (e *EnvFile) Set(key, value string) {
	line := key + "=" + quoteEnv(value)
	replaced := false
	out := e.lines[:0:0]
	for _, l := range e.lines {
		if k, _, ok := splitLine(l); ok && k == key {
			if !replaced {
				out = append(out, line)
				replaced = true
			}
			continue
		}
		out = append(out, l)
	}
	if !replaced {
		out = append(out, line)
	}
	e.lines = out
}

// Delete removes key.
func (e *EnvFile) Delete(key string) {
	out := e.lines[:0:0]
	for _, l := range e.lines {
		if k, _, ok := splitLine(l); ok && k == key {
			continue
		}
		out = append(out, l)
	}
	e.lines = out
}

// Bytes renders the file.
func (e *EnvFile) Bytes() []byte {
	var b bytes.Buffer
	for _, l := range e.lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// Save writes the file atomically, keeping mode and ownership, after copying
// the previous version to <path>.coolify-mirror-<timestamp>. Returns the backup path.
func (e *EnvFile) Save() (string, error) {
	if e.Path == "" {
		return "", fmt.Errorf("env file has no path")
	}
	st, err := os.Stat(e.Path)
	if err != nil {
		return "", err
	}
	old, err := os.ReadFile(e.Path)
	if err != nil {
		return "", err
	}
	backup := e.Path + ".coolify-mirror-" + time.Now().Format("20060102-150405")
	if err := os.WriteFile(backup, old, 0o600); err != nil {
		return "", err
	}
	tmp := filepath.Join(filepath.Dir(e.Path), ".env.coolify-mirror.tmp")
	if err := os.WriteFile(tmp, e.Bytes(), st.Mode().Perm()); err != nil {
		return backup, err
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		_ = os.Chown(tmp, int(sys.Uid), int(sys.Gid))
		_ = os.Chown(backup, int(sys.Uid), int(sys.Gid))
	}
	if err := os.Rename(tmp, e.Path); err != nil {
		return backup, err
	}
	return backup, nil
}
