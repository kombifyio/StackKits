package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/kombifyio/stackkits/internal/apisurfacev2"
	"github.com/spf13/cobra"
)

// Runtime flags added next to the argument flags.
const (
	flagFields         = "fields"
	flagNDJSON         = "ndjson"
	flagDryRun         = "dry-run"
	flagIdempotencyKey = "idempotency-key"
	flagBody           = "body"
	flagFile           = "file"
	flagYes            = "yes"
)

// JSON Schema types that map to typed flags.
const (
	typeInteger = "integer"
	typeNumber  = "number"
	typeBoolean = "boolean"
	typeArray   = "array"
	typeObject  = "object"
)

// runner executes one operation.
type runner struct {
	op         *apisurface.Operation
	cfg        Config
	positional []apisurface.Argument
	flagged    []apisurface.Argument
}

// request is a fully built HTTP call, printable for --dry-run.
type request struct {
	method   string
	url      string
	headers  http.Header
	body     []byte
	bodyJSON any    // decoded JSON body for --dry-run
	file     string // file-mode body source for --dry-run
}

func newCommand(op *apisurface.Operation, cfg Config) *cobra.Command {
	r := &runner{op: op, cfg: cfg}
	byName := map[string]apisurface.Argument{}
	for _, a := range op.Arguments {
		byName[a.Name] = a
	}
	isPositional := map[string]bool{}
	for _, name := range op.CLI.Args {
		if a, ok := byName[name]; ok {
			r.positional = append(r.positional, a)
			isPositional[name] = true
		}
	}
	for _, a := range op.Arguments {
		if !isPositional[a.Name] && !wholeBody(a) {
			r.flagged = append(r.flagged, a)
		}
	}
	use := op.CLI.Command[len(op.CLI.Command)-1]
	for _, a := range r.positional {
		use += " <" + apisurface.KebabCase(a.Name) + ">"
	}
	cmd := &cobra.Command{
		Use:           use,
		Short:         op.Summary,
		Long:          longHelp(op),
		Aliases:       op.CLI.Aliases,
		Hidden:        op.CLI.Hidden,
		Args:          cobra.ArbitraryArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE:          r.run,
	}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return r.report(&ExitError{Code: ExitInvalid, Err: err})
	})
	r.addFlags(cmd)
	return cmd
}

func longHelp(op *apisurface.Operation) string {
	parts := []string{op.Summary}
	if op.Description != "" {
		parts = append(parts, op.Description)
	}
	if op.Confirmation != "" {
		parts = append(parts, "Requires confirmation: "+op.Confirmation)
	}
	if op.Deprecated {
		parts = append(parts, "Deprecated.")
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func wholeBody(a apisurface.Argument) bool { return a.In == apisurface.InBody && a.WireName == "" }

func (r *runner) addFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	for _, a := range r.flagged {
		name, usage := apisurface.KebabCase(a.Name), a.Description
		if a.Required {
			usage = strings.TrimSpace(usage + " (required)")
		}
		switch schemaType(a.Schema) {
		case typeInteger:
			f.Int64(name, 0, usage)
		case typeNumber:
			f.Float64(name, 0, usage)
		case typeBoolean:
			f.Bool(name, false, usage)
		case typeArray:
			f.StringSlice(name, nil, usage+" (repeat or comma-separate)")
		case typeObject:
			f.String(name, "", usage+" (JSON object)")
		default:
			f.String(name, "", usage)
		}
	}
	f.String(flagFields, "", "comma-separated dotted paths to keep in the output (per element for arrays)")
	f.Bool(flagNDJSON, false, "print arrays as one compact JSON value per line")
	if r.op.Mutating {
		f.Bool(flagDryRun, false, "print the request instead of sending it")
		f.String(flagIdempotencyKey, "", "Idempotency-Key header (default: a fresh UUID)")
	}
	if b := r.op.Body; b != nil {
		if b.Mode == apisurface.BodyFile {
			f.String(flagFile, "", "request body file ("+b.ContentType+"); - reads stdin")
		} else {
			f.String(flagBody, "", "JSON request body: a literal, @path or - for stdin; argument flags override its properties")
		}
	}
	if r.op.Confirmation != "" {
		f.Bool(flagYes, false, "confirm: "+r.op.Confirmation)
	}
}

func (r *runner) run(cmd *cobra.Command, args []string) error {
	return r.report(r.execute(cmd, args))
}

// report writes an unreported failure to Err and normalizes it to ExitError.
func (r *runner) report(err error) error {
	if err == nil {
		return nil
	}
	var ee *ExitError
	if !errors.As(err, &ee) {
		ee = &ExitError{Code: ExitFailure, Err: err}
	}
	if !ee.reported {
		fmt.Fprintf(r.cfg.Err, "Error: %v\n", ee.Err)
		ee.reported = true
	}
	return ee
}

func invalid(format string, args ...any) error {
	return &ExitError{Code: ExitInvalid, Err: fmt.Errorf(format, args...)}
}

func (r *runner) execute(cmd *cobra.Command, args []string) error {
	values, err := r.values(cmd, args)
	if err != nil {
		return err
	}
	req, err := r.build(cmd, values)
	if err != nil {
		return err
	}
	f := cmd.Flags()
	if dry, _ := f.GetBool(flagDryRun); dry {
		return r.printDryRun(req)
	}
	if err := r.confirm(cmd); err != nil {
		return err
	}
	return r.send(cmd, req)
}

// values collects typed argument values from positionals and changed flags.
func (r *runner) values(cmd *cobra.Command, args []string) (map[string]any, error) {
	if len(args) != len(r.positional) {
		return nil, invalid("%s expects %d positional argument(s), got %d", cmd.CommandPath(), len(r.positional), len(args))
	}
	values := map[string]any{}
	for i, a := range r.positional {
		v, err := parseValue(a, args[i])
		if err != nil {
			return nil, invalid("argument <%s>: %v", apisurface.KebabCase(a.Name), err)
		}
		values[a.Name] = v
	}
	f := cmd.Flags()
	for _, a := range r.flagged {
		name := apisurface.KebabCase(a.Name)
		if !f.Changed(name) {
			continue
		}
		v, err := flagValue(cmd, a, name)
		if err != nil {
			return nil, invalid("--%s: %v", name, err)
		}
		values[a.Name] = v
	}
	return values, nil
}

func flagValue(cmd *cobra.Command, a apisurface.Argument, name string) (any, error) {
	f := cmd.Flags()
	switch schemaType(a.Schema) {
	case typeInteger:
		return f.GetInt64(name)
	case typeNumber:
		return f.GetFloat64(name)
	case typeBoolean:
		return f.GetBool(name)
	case typeArray:
		items, err := f.GetStringSlice(name)
		if err != nil {
			return nil, err
		}
		return parseItems(a.Schema, items)
	default:
		s, err := f.GetString(name)
		if err != nil {
			return nil, err
		}
		return parseValue(a, s)
	}
}

// parseValue converts a string to the argument's schema type.
func parseValue(a apisurface.Argument, s string) (any, error) {
	switch schemaType(a.Schema) {
	case typeInteger:
		return strconv.ParseInt(s, 10, 64)
	case typeNumber:
		return strconv.ParseFloat(s, 64)
	case typeBoolean:
		return strconv.ParseBool(s)
	case typeArray:
		return parseItems(a.Schema, strings.Split(s, ","))
	case typeObject:
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			return nil, fmt.Errorf("not valid JSON: %w", err)
		}
		return v, nil
	default:
		return s, nil
	}
}

func parseItems(schema map[string]any, items []string) ([]any, error) {
	item := apisurface.Argument{}
	item.Schema, _ = schema["items"].(map[string]any)
	out := make([]any, 0, len(items))
	for _, s := range items {
		v, err := parseValue(item, strings.TrimSpace(s))
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// schemaType returns the first non-null JSON Schema type, defaulting to
// string.
func schemaType(schema map[string]any) string {
	switch t := schema["type"].(type) {
	case string:
		return t
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok && s != "null" {
				return s
			}
		}
	}
	return "string"
}

func (r *runner) build(cmd *cobra.Command, values map[string]any) (*request, error) {
	for _, a := range r.op.Arguments {
		if _, ok := values[a.Name]; a.Required && !ok && a.In != apisurface.InBody {
			return nil, invalid("missing required argument %s", r.display(a))
		}
	}
	base, err := r.cfg.BaseURL(cmd.Context())
	if err != nil {
		return nil, &ExitError{Code: ExitFailure, Err: fmt.Errorf("resolve base URL: %w", err)}
	}
	req := &request{method: r.op.Method, headers: http.Header{}}
	path, query := r.bindArguments(values, req.headers)
	req.url = strings.TrimRight(base, "/") + r.cfg.ResolvePath(path)
	if len(query) > 0 {
		req.url += "?" + query.Encode()
	}
	if err := r.buildBody(cmd, values, req); err != nil {
		return nil, err
	}
	if r.op.Output != nil {
		req.headers.Set("Accept", r.op.Output.ContentType)
	}
	if r.op.Mutating {
		key, _ := cmd.Flags().GetString(flagIdempotencyKey)
		if key == "" {
			key = uuid.NewString()
		}
		req.headers.Set("Idempotency-Key", key)
	}
	return req, nil
}

// bindArguments substitutes escaped path parameters and collects query
// values (arrays as repeated keys) and header arguments.
func (r *runner) bindArguments(values map[string]any, headers http.Header) (string, url.Values) {
	path := r.op.Path
	query := url.Values{}
	for _, a := range r.op.Arguments {
		v, ok := values[a.Name]
		if !ok {
			continue
		}
		switch a.In {
		case apisurface.InPath:
			path = strings.ReplaceAll(path, "{"+a.WireName+"}", url.PathEscape(format(v)))
		case apisurface.InQuery:
			list, isList := v.([]any)
			if !isList {
				list = []any{v}
			}
			for _, e := range list {
				query.Add(a.WireName, format(e))
			}
		case apisurface.InHeader:
			headers.Set(a.WireName, format(v))
		}
	}
	return path, query
}

func (r *runner) display(a apisurface.Argument) string {
	for _, p := range r.positional {
		if p.Name == a.Name {
			return "<" + apisurface.KebabCase(a.Name) + ">"
		}
	}
	return "--" + apisurface.KebabCase(a.Name)
}

func format(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = format(e)
		}
		return strings.Join(parts, ",")
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func (r *runner) buildBody(cmd *cobra.Command, values map[string]any, req *request) error {
	b := r.op.Body
	if b == nil {
		return nil
	}
	switch b.Mode {
	case apisurface.BodyFile:
		return r.fileBody(cmd, req)
	case apisurface.BodyJSON:
		raw, set, err := r.readBodyFlag(cmd)
		if err != nil {
			return err
		}
		if !set {
			if b.Required {
				return invalid("missing required request body; pass --body")
			}
			return nil
		}
		return setJSONBody(req, b.ContentType, raw)
	default:
		return r.propertiesBody(cmd, values, req)
	}
}

func (r *runner) propertiesBody(cmd *cobra.Command, values map[string]any, req *request) error {
	obj := map[string]any{}
	raw, set, err := r.readBodyFlag(cmd)
	if err != nil {
		return err
	}
	if set {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if derr := dec.Decode(&obj); derr != nil {
			return invalid("--body must be a JSON object: %v", derr)
		}
	}
	for _, a := range r.op.Arguments {
		if a.In != apisurface.InBody {
			continue
		}
		if v, ok := values[a.Name]; ok {
			obj[a.WireName] = v
		}
		if _, ok := obj[a.WireName]; a.Required && !ok {
			return invalid("missing required argument %s", r.display(a))
		}
	}
	if !set && len(obj) == 0 && !r.op.Body.Required {
		return nil
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return invalid("encode body: %v", err)
	}
	return setJSONBody(req, r.op.Body.ContentType, data)
}

func setJSONBody(req *request, contentType string, raw []byte) error {
	var decoded any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		return invalid("--body is not valid JSON: %v", err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return invalid("--body is not valid JSON: %v", err)
	}
	req.body, req.bodyJSON = compact.Bytes(), decoded
	req.headers.Set("Content-Type", contentType)
	return nil
}

// readBodyFlag reads --body as a literal, @path or - (stdin).
func (r *runner) readBodyFlag(cmd *cobra.Command) ([]byte, bool, error) {
	f := cmd.Flags()
	if !f.Changed(flagBody) {
		return nil, false, nil
	}
	v, _ := f.GetString(flagBody)
	var (
		data []byte
		err  error
	)
	switch {
	case v == "-":
		data, err = io.ReadAll(r.cfg.In)
	case strings.HasPrefix(v, "@"):
		data, err = os.ReadFile(v[1:])
	default:
		data = []byte(v)
	}
	if err != nil {
		return nil, false, invalid("read --body: %v", err)
	}
	return data, true, nil
}

func (r *runner) fileBody(cmd *cobra.Command, req *request) error {
	path, _ := cmd.Flags().GetString(flagFile)
	if path == "" {
		if r.op.Body.Required {
			return invalid("missing required request body; pass --file")
		}
		return nil
	}
	var (
		data []byte
		err  error
	)
	if path == "-" {
		data, err = io.ReadAll(r.cfg.In)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return invalid("read --file: %v", err)
	}
	req.body, req.file = data, path
	req.headers.Set("Content-Type", r.op.Body.ContentType)
	return nil
}

func (r *runner) printDryRun(req *request) error {
	headers := map[string]string{}
	for k := range req.headers {
		if k != "Authorization" {
			headers[k] = req.headers.Get(k)
		}
	}
	return writeJSON(r.cfg.Out, struct {
		DryRun  bool              `json:"dryRun"`
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
		Body    any               `json:"body,omitempty"`
		File    string            `json:"file,omitempty"`
	}{true, req.method, req.url, headers, req.bodyJSON, req.file}, true)
}

// confirm enforces x-kombify-confirmation: interactive callers type "yes";
// everyone else fails closed before any request is sent.
func (r *runner) confirm(cmd *cobra.Command) error {
	if r.op.Confirmation == "" {
		return nil
	}
	if yes, _ := cmd.Flags().GetBool(flagYes); yes {
		return nil
	}
	if !r.cfg.Interactive() {
		_ = writeJSON(r.cfg.Err, map[string]string{
			"error_code":  "CONFIRMATION_REQUIRED",
			"message":     r.op.Confirmation,
			"remediation": "Re-run with --yes after reviewing the operation.",
		}, false)
		return &ExitError{Code: ExitConfirmation, Err: errors.New("confirmation required"), reported: true}
	}
	fmt.Fprintf(r.cfg.Err, "%s\nType yes to continue: ", r.op.Confirmation)
	line, _ := bufio.NewReader(r.cfg.In).ReadString('\n')
	if strings.TrimSpace(line) != "yes" {
		fmt.Fprintln(r.cfg.Err, "Aborted.")
		return &ExitError{Code: ExitConfirmation, Err: errors.New("confirmation declined"), reported: true}
	}
	return nil
}

func (r *runner) send(cmd *cobra.Command, spec *request) error {
	var body io.Reader
	if spec.body != nil {
		body = bytes.NewReader(spec.body)
	}
	req, err := http.NewRequestWithContext(cmd.Context(), spec.method, spec.url, body)
	if err != nil {
		return invalid("build request: %v", err)
	}
	req.Header = spec.headers
	if r.cfg.Authorize != nil {
		if aerr := r.cfg.Authorize(req); aerr != nil {
			return &ExitError{Code: ExitUnauthorized, Err: fmt.Errorf("authorize: %w", aerr)}
		}
	}
	resp, err := r.cfg.HTTPClient.Do(req)
	if err != nil {
		return &ExitError{Code: ExitUnavailable, Err: err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return &ExitError{Code: ExitUnavailable, Err: fmt.Errorf("read response: %w", err)}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return r.printSuccess(cmd, resp.Header.Get("Content-Type"), data)
	}
	printFailure(r.cfg.Err, resp.Header.Get("Content-Type"), data)
	return &ExitError{Code: statusExitCode(resp.StatusCode), Err: fmt.Errorf("HTTP %s", resp.Status), reported: true}
}

func statusExitCode(status int) int {
	switch {
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return ExitInvalid
	case status == http.StatusUnauthorized:
		return ExitUnauthorized
	case status == http.StatusPaymentRequired || status == http.StatusForbidden || status == http.StatusTooManyRequests:
		return ExitDenied
	case status == http.StatusNotFound:
		return ExitNotFound
	case status == http.StatusConflict:
		return ExitConflict
	case status >= 500:
		return ExitUnavailable
	default:
		return ExitFailure
	}
}
