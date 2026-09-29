package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"strings"

	"github.com/spf13/cobra"
)

// printSuccess writes a 2xx response: JSON is unwrapped from the operation's
// envelope, projected by --fields and printed pretty or as NDJSON; other
// content is copied verbatim.
func (r *runner) printSuccess(cmd *cobra.Command, contentType string, data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if !isJSON(contentType) {
		_, err := r.cfg.Out.Write(data)
		return err
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		_, werr := r.cfg.Out.Write(data)
		return werr
	}
	if out := r.op.Output; out != nil && out.Envelope != "" {
		if m, ok := v.(map[string]any); ok {
			if inner, has := m[out.Envelope]; has {
				v = inner
			}
		}
	}
	f := cmd.Flags()
	if fields, _ := f.GetString(flagFields); fields != "" {
		v = project(v, parseFields(fields))
	}
	if nd, _ := f.GetBool(flagNDJSON); nd {
		return writeNDJSON(r.cfg.Out, v)
	}
	return writeJSON(r.cfg.Out, v, true)
}

// printFailure writes an error response body, pretty-printed when JSON.
func printFailure(w io.Writer, contentType string, data []byte) {
	if isJSON(contentType) {
		var v any
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if dec.Decode(&v) == nil {
			_ = writeJSON(w, v, true)
			return
		}
	}
	if len(data) > 0 {
		_, _ = w.Write(data)
		if data[len(data)-1] != '\n' {
			_, _ = io.WriteString(w, "\n")
		}
	}
}

func writeJSON(w io.Writer, v any, pretty bool) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if pretty {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(v)
}

func writeNDJSON(w io.Writer, v any) error {
	list, ok := v.([]any)
	if !ok {
		return writeJSON(w, v, false)
	}
	for _, e := range list {
		if err := writeJSON(w, e, false); err != nil {
			return err
		}
	}
	return nil
}

func parseFields(s string) [][]string {
	var paths [][]string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			paths = append(paths, strings.Split(f, "."))
		}
	}
	return paths
}

// project keeps only the given dotted paths; arrays are projected per element
// at any depth.
func project(v any, paths [][]string) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = project(e, paths)
		}
		return out
	case map[string]any:
		rest := map[string][][]string{}
		var order []string
		for _, p := range paths {
			if _, seen := rest[p[0]]; !seen {
				order = append(order, p[0])
			}
			rest[p[0]] = append(rest[p[0]], p[1:])
		}
		out := map[string]any{}
		for _, key := range order {
			val, ok := t[key]
			if !ok {
				continue
			}
			out[key] = projectRest(val, rest[key])
		}
		return out
	default:
		return v
	}
}

// projectRest keeps the whole value when any path ends at it.
func projectRest(v any, rests [][]string) any {
	for _, r := range rests {
		if len(r) == 0 {
			return v
		}
	}
	return project(v, rests)
}

func isJSON(ct string) bool {
	base, _, err := mime.ParseMediaType(ct)
	if err != nil {
		base = ct
	}
	return base == "application/json" || strings.HasSuffix(base, "+json")
}
