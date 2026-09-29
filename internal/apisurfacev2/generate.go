package apisurface

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Result is a generated surface plus the non-failing report.
type Result struct {
	Surface *Surface
	// ParityGaps lists operationIds that are exposed to API and CLI but have
	// no MCP decision: no x-kombify-mcp and no x-kombify-surface.mcp.defaults.
	ParityGaps []string
	// Excluded lists operationIds with the reviewed decision
	// x-kombify-mcp: false.
	Excluded []string
	// Catalog is x-kombify-surface.mcp.catalog, nil when absent. It feeds
	// Surface.CatalogFragment.
	Catalog *Catalog
}

// HTTP methods the generator classifies.
const (
	methodGet    = "GET"
	methodHead   = "HEAD"
	methodPut    = "PUT"
	methodDelete = "DELETE"
)

var (
	productPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	methods        = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}
	// Header parameters owned by the runtime, never surface arguments.
	runtimeHeaders = map[string]bool{"idempotency-key": true, "authorization": true}
)

// Generate reads an OpenAPI 3.1 document (YAML or JSON) with x-kombify-*
// extensions and builds its Surface. specPath is recorded as the source path.
// Contract violations are returned together as a *ValidationError.
func Generate(specPath string, spec []byte) (*Result, error) {
	doc, err := loadDocument(spec)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(spec)
	g := &generator{doc: doc}
	s := &Surface{
		SchemaVersion: SchemaVersion,
		Source:        Source{Path: filepath.ToSlash(specPath), SHA256: hex.EncodeToString(sum[:])},
	}
	if !g.root(s) {
		return nil, g.failure()
	}
	s.Operations = g.operations(s.Envelope)
	validate(s, g)
	if len(g.problems) > 0 {
		return nil, g.failure()
	}
	res := &Result{Surface: s, Catalog: g.catalog}
	for _, op := range s.Operations {
		switch {
		case op.MCPExcluded:
			res.Excluded = append(res.Excluded, op.OperationID)
		case op.MCP == nil:
			res.ParityGaps = append(res.ParityGaps, op.OperationID)
		}
	}
	sort.Strings(res.ParityGaps)
	sort.Strings(res.Excluded)
	return res, nil
}

type generator struct {
	doc      *document
	problems []Problem
	defaults *mcpDefaults
	catalog  *Catalog
}

// mcpDefaults is x-kombify-surface.mcp.defaults: the capabilities of tools
// derived for operations without an x-kombify-mcp decision.
type mcpDefaults struct {
	read, write, confirm string
}

func (g *generator) add(opID, rule, format string, args ...any) {
	g.problems = append(g.problems, Problem{OperationID: opID, Rule: rule, Message: fmt.Sprintf(format, args...)})
}

func (g *generator) failure() error {
	return newValidationError(g.problems)
}

// root reads x-kombify-surface into s.
func (g *generator) root(s *Surface) bool {
	ext, ok := g.doc.root["x-kombify-surface"].(map[string]any)
	if !ok {
		g.add("", "surface", "the document root has no x-kombify-surface object; add it with at least `product`")
		return false
	}
	s.Product = str(ext, "product")
	if !productPattern.MatchString(s.Product) {
		g.add("", "surface", "x-kombify-surface.product %q must match %s", s.Product, productPattern)
	}
	s.Capability = str(ext, "capability")
	s.Envelope = str(ext, "envelope")
	if c := obj(ext, "cli"); c != nil {
		s.CLI = &RootCLI{Command: str(c, "command")}
	}
	if m := obj(ext, "mcp"); m != nil {
		s.MCP = &RootMCP{Transport: str(m, "transport"), Endpoint: str(m, "endpoint"), ProtocolVersion: str(m, "protocolVersion")}
		if d, present := m["defaults"]; present {
			g.rootDefaults(d)
		}
		if c, present := m["catalog"]; present {
			g.catalog = g.rootCatalog(c)
		}
	}
	return len(g.problems) == 0
}

func (g *generator) rootDefaults(v any) {
	d, ok := v.(map[string]any)
	if !ok {
		g.add("", "mcp-defaults", "x-kombify-surface.mcp.defaults must be an object")
		return
	}
	g.defaults = &mcpDefaults{read: str(d, "readCapability"), write: str(d, "writeCapability"), confirm: str(d, "confirmCapability")}
}

func (g *generator) rootCatalog(v any) *Catalog {
	c, ok := v.(map[string]any)
	if !ok {
		g.add("", "mcp-catalog", "x-kombify-surface.mcp.catalog must be an object")
		return nil
	}
	up := obj(c, "upstream")
	cat := &Catalog{
		ServerKey:             str(c, "serverKey"),
		ConnectorFamily:       str(c, "connectorFamily"),
		Upstream:              CatalogUpstream{Kind: str(up, "kind"), Ref: str(up, "ref")},
		PortalVisibilityGroup: str(c, "portalVisibilityGroup"),
		RequiredScopes:        stringList(c["requiredScopes"]),
		FeaturePrefix:         str(c, "featurePrefix"),
		QuotaPrefix:           str(c, "quotaPrefix"),
		AuditPrefix:           str(c, "auditPrefix"),
	}
	for _, f := range []struct{ name, value string }{
		{"serverKey", cat.ServerKey}, {"connectorFamily", cat.ConnectorFamily},
		{"upstream.kind", cat.Upstream.Kind}, {"upstream.ref", cat.Upstream.Ref},
		{"portalVisibilityGroup", cat.PortalVisibilityGroup}, {"featurePrefix", cat.FeaturePrefix},
		{"quotaPrefix", cat.QuotaPrefix}, {"auditPrefix", cat.AuditPrefix},
	} {
		if f.value == "" {
			g.add("", "mcp-catalog", "x-kombify-surface.mcp.catalog.%s is required", f.name)
		}
	}
	if len(cat.RequiredScopes) == 0 {
		g.add("", "mcp-catalog", "x-kombify-surface.mcp.catalog.requiredScopes must list at least one scope")
	}
	return cat
}

// operations walks all path items in (path, method) order.
func (g *generator) operations(envelope string) []Operation {
	paths := obj(g.doc.root, "paths")
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var ops []Operation
	for _, path := range keys {
		raw, _ := paths[path].(map[string]any)
		// A path item that references another path is an alias of an
		// operation already emitted under its canonical path.
		if ref := str(raw, "$ref"); strings.HasPrefix(ref, "#/paths/") {
			continue
		}
		item, err := g.doc.deref(raw)
		if err != nil || item == nil {
			g.add("", "reference", "path %s: %v", path, err)
			continue
		}
		for _, method := range methods {
			rawOp, ok := item[method].(map[string]any)
			if !ok || rawOp["x-kombify-internal"] == true {
				continue
			}
			ops = append(ops, g.operation(path, method, item, rawOp, envelope))
		}
	}
	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return ops[i].Method < ops[j].Method
	})
	return ops
}

func (g *generator) operation(path, method string, item, raw map[string]any, rootEnvelope string) Operation {
	op := Operation{
		OperationID:  str(raw, "operationId"),
		Method:       strings.ToUpper(method),
		Path:         path,
		Summary:      strings.TrimSpace(str(raw, "summary")),
		Description:  strings.TrimSpace(str(raw, "description")),
		Tags:         stringList(raw["tags"]),
		Availability: str(raw, "x-kombify-availability"),
		Deprecated:   raw["deprecated"] == true,
		Confirmation: str(raw, "x-kombify-confirmation"),
	}
	switch op.Method {
	case "POST", methodPut, "PATCH", methodDelete:
		op.Mutating = true
	}
	id := op.OperationID
	if id == "" {
		id = op.Method + " " + path
	}
	op.Arguments = g.parameters(id, item, raw)
	op.Body, op.Arguments = g.requestBody(id, raw, op.Arguments)
	op.Output = g.output(id, raw, operationEnvelope(raw, rootEnvelope))
	op.CLI = g.cli(id, raw)
	op.MCP, op.MCPExcluded = g.mcp(id, raw, &op)
	if readOnlyPost(&op) {
		// A side-effect-free POST (Connect RPC, validator) changes nothing, so
		// generated clients send no idempotency key and offer no dry run.
		op.Mutating = false
	}
	return op
}

// readOnlyPost reports an explicitly declared side-effect-free POST: an
// explicit (never derived) x-kombify-mcp object with readOnlyHint, no
// destructive hint and no confirmation.
func readOnlyPost(op *Operation) bool {
	return op.Method == "POST" && op.MCP != nil && !op.MCP.Derived &&
		op.MCP.Annotations.ReadOnlyHint && !op.MCP.Annotations.DestructiveHint && op.Confirmation == ""
}

func operationEnvelope(raw map[string]any, rootEnvelope string) string {
	switch v := raw["x-kombify-envelope"].(type) {
	case string:
		return v
	case bool:
		if !v {
			return ""
		}
	}
	return rootEnvelope
}

// parameters merges path-item and operation parameters (operation wins).
func (g *generator) parameters(id string, item, raw map[string]any) []Argument {
	type key struct{ in, name string }
	var order []key
	byKey := map[key]map[string]any{}
	for _, list := range [][]any{anyList(item["parameters"]), anyList(raw["parameters"])} {
		for _, p := range list {
			param, err := g.doc.deref(p)
			if err != nil || param == nil {
				g.add(id, "reference", "parameter: %v", err)
				continue
			}
			k := key{str(param, "in"), str(param, "name")}
			if _, seen := byKey[k]; !seen {
				order = append(order, k)
			}
			byKey[k] = param
		}
	}
	var args []Argument
	for _, k := range order {
		if arg, ok := g.parameter(id, byKey[k]); ok {
			args = append(args, arg)
		}
	}
	return args
}

func (g *generator) parameter(id string, param map[string]any) (Argument, bool) {
	in, wire := str(param, "in"), str(param, "name")
	switch in {
	case InPath, InQuery:
	case InHeader:
		if runtimeHeaders[strings.ToLower(wire)] {
			return Argument{}, false
		}
	default:
		g.add(id, "parameter", "parameter %q in %q is not supported; use path, query or header", wire, in)
		return Argument{}, false
	}
	name := str(param, "x-kombify-name")
	if name == "" {
		name = SnakeCase(wire)
	}
	rawSchema := param["schema"]
	if rawSchema == nil {
		content := obj(param, "content")
		if ct := pickContentType(content); ct != "" {
			rawSchema = obj(content, ct)["schema"]
		}
	}
	schema, err := g.doc.emitSchema(rawSchema)
	if err != nil {
		g.add(id, "reference", "parameter %q: %v", wire, err)
	}
	return Argument{
		Name:        name,
		In:          in,
		WireName:    wire,
		Required:    in == InPath || param["required"] == true,
		Description: strings.TrimSpace(str(param, "description")),
		Schema:      schema,
	}, true
}

// requestBody classifies the body and appends body arguments.
func (g *generator) requestBody(id string, raw map[string]any, args []Argument) (*Body, []Argument) {
	if raw["requestBody"] == nil {
		return nil, args
	}
	rb, err := g.doc.deref(raw["requestBody"])
	if err != nil || rb == nil {
		g.add(id, "reference", "requestBody: %v", err)
		return nil, args
	}
	content := obj(rb, "content")
	ct := pickContentType(content)
	if ct == "" {
		return nil, args
	}
	body := &Body{ContentType: ct, Required: rb["required"] == true, Mode: BodyFile}
	if !isJSON(ct) {
		return body, args
	}
	rawSchema := obj(content, ct)["schema"]
	schema, err := g.doc.deref(rawSchema)
	if err != nil {
		g.add(id, "reference", "requestBody schema: %v", err)
		return body, args
	}
	props := obj(schema, "properties")
	if len(props) == 0 {
		body.Mode = BodyJSON
		emitted, err := g.doc.emitSchema(rawSchema)
		if err != nil {
			g.add(id, "reference", "requestBody schema: %v", err)
		}
		return body, append(args, Argument{
			Name: "body", In: InBody, Required: body.Required,
			Description: strings.TrimSpace(str(rb, "description")), Schema: emitted,
		})
	}
	body.Mode = BodyProperties
	required := map[string]bool{}
	for _, r := range stringList(schema["required"]) {
		required[r] = true
	}
	for _, prop := range sortedKeys(props) {
		args = append(args, g.bodyProperty(id, prop, props[prop], required[prop]))
	}
	return body, args
}

func (g *generator) bodyProperty(id, prop string, raw any, required bool) Argument {
	name := ""
	if m, ok := raw.(map[string]any); ok {
		name = str(m, "x-kombify-name")
	}
	if name == "" {
		name = SnakeCase(prop)
	}
	schema, err := g.doc.emitSchema(raw)
	if err != nil {
		g.add(id, "reference", "body property %q: %v", prop, err)
	}
	return Argument{
		Name: name, In: InBody, WireName: prop, Required: required,
		Description: strings.TrimSpace(str(schema, "description")), Schema: schema,
	}
}

// output describes the first 2xx response with JSON content.
func (g *generator) output(id string, raw map[string]any, envelope string) *Output {
	responses := obj(raw, "responses")
	for _, code := range sortedKeys(responses) {
		if !strings.HasPrefix(code, "2") {
			continue
		}
		resp, err := g.doc.deref(responses[code])
		if err != nil {
			g.add(id, "reference", "response %s: %v", code, err)
			continue
		}
		content := obj(resp, "content")
		ct := pickContentType(content)
		if ct == "" || !isJSON(ct) {
			continue
		}
		return g.payload(id, ct, obj(content, ct)["schema"], envelope)
	}
	return nil
}

func (g *generator) payload(id, ct string, rawSchema any, envelope string) *Output {
	out := &Output{ContentType: ct}
	if rawSchema == nil {
		out.Envelope = envelope
		return out
	}
	target := rawSchema
	if envelope != "" {
		if inner, ok := g.envelopeProperty(rawSchema, envelope); ok {
			out.Envelope, target = envelope, inner
		}
	}
	schema, err := g.doc.emitSchema(target)
	if err != nil {
		g.add(id, "reference", "response schema: %v", err)
	}
	out.Schema = schema
	return out
}

// envelopeProperty finds the envelope property on the schema or one of its
// allOf members.
func (g *generator) envelopeProperty(raw any, envelope string) (any, bool) {
	schema, err := g.doc.deref(raw)
	if err != nil || schema == nil {
		return nil, false
	}
	if p, ok := obj(schema, "properties")[envelope]; ok {
		return p, true
	}
	for _, member := range anyList(schema["allOf"]) {
		if p, ok := g.envelopeProperty(member, envelope); ok {
			return p, true
		}
	}
	return nil, false
}

func (g *generator) cli(id string, raw map[string]any) CLI {
	ext, _ := raw["x-kombify-cli"].(map[string]any)
	c := CLI{
		Command: strings.Fields(str(ext, "command")),
		Aliases: stringList(ext["aliases"]),
		Args:    stringList(ext["args"]),
		Hidden:  ext["hidden"] == true,
	}
	if len(c.Command) == 0 {
		if tags := stringList(raw["tags"]); len(tags) > 0 {
			c.Command = append(c.Command, KebabCase(tags[0]))
		}
		c.Command = append(c.Command, KebabCase(str(raw, "operationId")))
	}
	for i, w := range c.Command {
		if w == "" && str(raw, "operationId") != "" { // a missing operationId is reported once
			g.add(id, "cli-command", "command word %d is empty", i+1)
		}
	}
	return c
}

// mcp reads the operation's MCP decision: an explicit x-kombify-mcp object,
// x-kombify-mcp: false (excluded), or, without the key, a tool derived from
// x-kombify-surface.mcp.defaults when the document declares them.
func (g *generator) mcp(id string, raw map[string]any, op *Operation) (*MCP, bool) {
	v, present := raw["x-kombify-mcp"]
	if !present {
		if g.defaults != nil {
			return g.derive(id, op), false
		}
		return nil, false
	}
	if v == false {
		return nil, true
	}
	ext, ok := v.(map[string]any)
	if !ok {
		g.add(id, "mcp", "x-kombify-mcp must be an object or false")
		return nil, false
	}
	m := &MCP{
		ToolName:           str(ext, "toolName"),
		Title:              str(ext, "title"),
		RequiredCapability: str(ext, "requiredCapability"),
		ActionClass:        str(ext, "actionClass"),
	}
	if rb, present := ext["resourceBinding"]; present {
		if b, ok := rb.(map[string]any); ok {
			m.ResourceBinding = &ResourceBinding{Argument: str(b, "argument"), Dimension: str(b, "dimension")}
		} else {
			g.add(id, "mcp", "x-kombify-mcp.resourceBinding must be an object with argument and dimension")
		}
	}
	if cb, present := ext["costBearing"]; present {
		b, ok := cb.(bool)
		if !ok {
			g.add(id, "mcp", "x-kombify-mcp.costBearing must be a boolean")
		}
		m.CostBearing = b
	}
	if m.ToolName == "" {
		m.ToolName = SnakeCase(str(raw, "operationId"))
	}
	if m.RequiredCapability == "" {
		g.add(id, "mcp", "x-kombify-mcp.requiredCapability is required")
	}
	ann := obj(ext, "annotations")
	for _, hint := range []struct {
		name string
		dst  *bool
	}{
		{"readOnlyHint", &m.Annotations.ReadOnlyHint},
		{"destructiveHint", &m.Annotations.DestructiveHint},
		{"idempotentHint", &m.Annotations.IdempotentHint},
		{"openWorldHint", &m.Annotations.OpenWorldHint},
	} {
		b, ok := ann[hint.name].(bool)
		if !ok {
			g.add(id, "annotations", "x-kombify-mcp.annotations.%s must be a boolean", hint.name)
		}
		*hint.dst = b
	}
	return m, false
}

// derive builds the default tool of an operation without an x-kombify-mcp
// decision. Confirmation-gated operations need the confirm capability,
// GET/HEAD the read capability and every other method the write capability.
func (g *generator) derive(id string, op *Operation) *MCP {
	readOnly := op.Method == methodGet || op.Method == methodHead
	confirmed := op.Confirmation != ""
	key, capability := "writeCapability", g.defaults.write
	switch {
	case confirmed:
		key, capability = "confirmCapability", g.defaults.confirm
	case readOnly:
		key, capability = "readCapability", g.defaults.read
	}
	if capability == "" {
		g.add(id, "mcp-defaults", "no x-kombify-mcp decision and x-kombify-surface.mcp.defaults.%s is not set; set the default, annotate the operation or exclude it with x-kombify-mcp: false", key)
	}
	return &MCP{
		ToolName:           SnakeCase(op.OperationID),
		Title:              op.Summary,
		RequiredCapability: capability,
		Annotations: Annotations{
			ReadOnlyHint: readOnly,
			// A read never destroys; a confirmation on a GET only raises the
			// required capability.
			DestructiveHint: confirmed && !readOnly,
			IdempotentHint:  readOnly || op.Method == methodPut || op.Method == methodDelete,
			OpenWorldHint:   false,
		},
		Derived: true,
	}
}

// pickContentType prefers application/json, then any JSON media type, then
// the first declared type.
func pickContentType(content map[string]any) string {
	keys := sortedKeys(content)
	if _, ok := content["application/json"]; ok {
		return "application/json"
	}
	for _, k := range keys {
		if isJSON(k) {
			return k
		}
	}
	if len(keys) > 0 {
		return keys[0]
	}
	return ""
}

// isJSON reports whether a media type carries JSON.
func isJSON(ct string) bool {
	base, _, err := mime.ParseMediaType(ct)
	if err != nil {
		base = ct
	}
	return base == "application/json" || strings.HasSuffix(base, "+json")
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func anyList(v any) []any {
	l, _ := v.([]any)
	return l
}

func stringList(v any) []string {
	var out []string
	for _, e := range anyList(v) {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
