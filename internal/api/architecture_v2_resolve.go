package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"sync"

	"github.com/kombifyio/stackkits/internal/architecturev2"
)

const (
	architectureV2ResolveBodyLimit          int64 = 1 << 20
	architectureV2ResolveDefaultConcurrency       = 2
	architectureV2ResolveMaxJSONDepth             = 64
	architectureV2ResolveMaxJSONTokens            = 65_536
	architectureV2ResolveMaxCollectionItems       = 4_096
	architectureV2ResolveRetryAfterSeconds        = "1"
)

type architectureV2ServiceState struct {
	once       sync.Once
	service    *architecturev2.Service
	err        error
	slots      chan struct{}
	rilMu      sync.RWMutex
	rilCurrent map[string]architecturev2.CurrentResolution
}

type architectureV2RequestError struct {
	status  int
	code    architecturev2.ErrorCode
	message string
	cause   error
}

func (e *architectureV2RequestError) Error() string {
	if e == nil {
		return "invalid Architecture v2 resolve request"
	}
	return e.message
}

func (e *architectureV2RequestError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func newArchitectureV2ServiceState(configuredConcurrency int) architectureV2ServiceState {
	if configuredConcurrency <= 0 {
		configuredConcurrency = architectureV2ResolveDefaultConcurrency
	}
	return architectureV2ServiceState{
		slots:      make(chan struct{}, configuredConcurrency),
		rilCurrent: make(map[string]architecturev2.CurrentResolution),
	}
}

func architectureV2RequestFailure(status int, code architecturev2.ErrorCode, message string, cause error) error {
	return &architectureV2RequestError{status: status, code: code, message: message, cause: cause}
}

type architectureV2ResolveEnvelope struct {
	StackSpec json.RawMessage `json:"stackSpec"`
	Inventory json.RawMessage `json:"inventory,omitempty"`
}

func (e *architectureV2ResolveEnvelope) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return errors.New("resolve envelope must be a JSON object")
	}
	seen := make(map[string]struct{}, 2)
	for decoder.More() {
		rawKey, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := rawKey.(string)
		if !ok {
			return errors.New("resolve envelope contains a non-string field name")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate JSON envelope field %q", key)
		}
		seen[key] = struct{}{}
		switch key {
		case "stackSpec":
			if err := decoder.Decode(&e.StackSpec); err != nil {
				return err
			}
		case "inventory":
			if err := decoder.Decode(&e.Inventory); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown JSON envelope field %q", key)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	return nil
}

func (s *Server) architectureV2ResolveService() (*architecturev2.Service, error) {
	s.architectureV2.once.Do(func() {
		s.architectureV2.service, s.architectureV2.err = architecturev2.NewEmbeddedService(
			architecturev2.StackKitsV2Contract(s.config.Version),
		)
	})
	return s.architectureV2.service, s.architectureV2.err
}

func (s *Server) handleArchitectureV2Resolve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if err := requireArchitectureV2JSONMediaType(r); err != nil {
		writeArchitectureV2RequestError(w, err)
		return
	}
	envelope, err := decodeArchitectureV2ResolveEnvelope(w, r)
	if err != nil {
		writeArchitectureV2RequestError(w, err)
		return
	}
	if !s.tryAcquireArchitectureV2Resolve() {
		w.Header().Set("Retry-After", architectureV2ResolveRetryAfterSeconds)
		writeArchitectureV2ResolveError(w, http.StatusTooManyRequests, &architecturev2.ResolveError{
			Code: architecturev2.ErrResolveBusy, Message: "Architecture v2 resolver concurrency limit reached",
		})
		return
	}
	defer s.releaseArchitectureV2Resolve()

	service, err := s.architectureV2ResolveService()
	if err != nil {
		writeMappedArchitectureV2ResolveError(w, err)
		return
	}
	result, err := service.Resolve(architecturev2.ResolveInput{
		StackSpec: envelope.StackSpec,
		Inventory: envelope.Inventory,
	})
	if err != nil {
		writeMappedArchitectureV2ResolveError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", `"`+result.PlanHash+`"`)
	w.Header().Set("X-StackKit-Plan-Hash", result.PlanHash)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.CanonicalPlan)
}

func requireArchitectureV2JSONMediaType(r *http.Request) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return architectureV2RequestFailure(
			http.StatusUnsupportedMediaType,
			architecturev2.ErrUnsupportedMedia,
			"Content-Type must be application/json",
			err,
		)
	}
	return nil
}

func (s *Server) tryAcquireArchitectureV2Resolve() bool {
	if s == nil || s.architectureV2.slots == nil {
		return false
	}
	select {
	case s.architectureV2.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseArchitectureV2Resolve() {
	if s == nil || s.architectureV2.slots == nil {
		return
	}
	<-s.architectureV2.slots
}

func decodeArchitectureV2ResolveEnvelope(w http.ResponseWriter, r *http.Request) (architectureV2ResolveEnvelope, error) {
	if r.ContentLength > architectureV2ResolveBodyLimit {
		return architectureV2ResolveEnvelope{}, architectureV2RequestFailure(
			http.StatusRequestEntityTooLarge,
			architecturev2.ErrRequestTooLarge,
			fmt.Sprintf("request body exceeds %d bytes", architectureV2ResolveBodyLimit),
			nil,
		)
	}
	reader := http.MaxBytesReader(w, r.Body, architectureV2ResolveBodyLimit)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()

	var envelope architectureV2ResolveEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return architectureV2ResolveEnvelope{}, architectureV2RequestFailure(
				http.StatusRequestEntityTooLarge,
				architecturev2.ErrRequestTooLarge,
				fmt.Sprintf("request body exceeds %d bytes", architectureV2ResolveBodyLimit),
				err,
			)
		}
		if errors.Is(err, io.EOF) {
			return architectureV2ResolveEnvelope{}, architectureV2RequestFailure(
				http.StatusBadRequest, architecturev2.ErrInvalidStackSpec, "request body is empty", err,
			)
		}
		return architectureV2ResolveEnvelope{}, architectureV2RequestFailure(
			http.StatusBadRequest,
			architecturev2.ErrInvalidStackSpec,
			fmt.Sprintf("invalid JSON envelope: %v", err),
			err,
		)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return architectureV2ResolveEnvelope{}, err
	}
	if err := requireJSONObject(envelope.StackSpec, "stackSpec", true, architecturev2.ErrInvalidStackSpec); err != nil {
		return architectureV2ResolveEnvelope{}, err
	}
	if len(bytes.TrimSpace(envelope.Inventory)) > 0 {
		if err := requireJSONObject(envelope.Inventory, "inventory", false, architecturev2.ErrInvalidInventory); err != nil {
			return architectureV2ResolveEnvelope{}, err
		}
	}
	return envelope, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return architectureV2RequestFailure(
				http.StatusBadRequest,
				architecturev2.ErrInvalidStackSpec,
				"request body contains multiple JSON values",
				nil,
			)
		}
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return architectureV2RequestFailure(
				http.StatusRequestEntityTooLarge,
				architecturev2.ErrRequestTooLarge,
				fmt.Sprintf("request body exceeds %d bytes", architectureV2ResolveBodyLimit),
				err,
			)
		}
		return architectureV2RequestFailure(
			http.StatusBadRequest,
			architecturev2.ErrInvalidStackSpec,
			fmt.Sprintf("invalid trailing JSON data: %v", err),
			err,
		)
	}
	return nil
}

func requireJSONObject(raw json.RawMessage, field string, required bool, code architecturev2.ErrorCode) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		if required {
			return architectureV2RequestFailure(http.StatusBadRequest, code, field+" is required", nil)
		}
		return nil
	}
	if err := validateArchitectureV2JSONObject(trimmed, field); err != nil {
		return architectureV2RequestFailure(http.StatusBadRequest, code, err.Error(), err)
	}
	return nil
}

type architectureV2JSONScanner struct {
	decoder *json.Decoder
	field   string
	tokens  int
}

func validateArchitectureV2JSONObject(raw []byte, field string) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	scanner := architectureV2JSONScanner{decoder: decoder, field: field}
	token, err := scanner.nextToken()
	if err != nil {
		return fmt.Errorf("%s must be a JSON object: %w", field, err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return fmt.Errorf("%s must be a JSON object", field)
	}
	if err := scanner.scanCollection(delimiter, 1); err != nil {
		return err
	}
	if _, err := scanner.nextToken(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%s contains multiple JSON values", field)
		}
		return fmt.Errorf("invalid trailing %s JSON data: %w", field, err)
	}
	return nil
}

func (s *architectureV2JSONScanner) nextToken() (json.Token, error) {
	token, err := s.decoder.Token()
	if err != nil {
		return nil, err
	}
	s.tokens++
	if s.tokens > architectureV2ResolveMaxJSONTokens {
		return nil, fmt.Errorf("%s exceeds the JSON token limit of %d", s.field, architectureV2ResolveMaxJSONTokens)
	}
	return token, nil
}

func (s *architectureV2JSONScanner) scanValue(depth int) error {
	token, err := s.nextToken()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delimiter != '{' && delimiter != '[' {
		return fmt.Errorf("%s contains an unexpected JSON delimiter %q", s.field, delimiter)
	}
	return s.scanCollection(delimiter, depth)
}

func (s *architectureV2JSONScanner) scanCollection(open json.Delim, depth int) error {
	if depth > architectureV2ResolveMaxJSONDepth {
		return fmt.Errorf("%s exceeds the JSON nesting depth limit of %d", s.field, architectureV2ResolveMaxJSONDepth)
	}
	items := 0
	if open == '{' {
		seen := make(map[string]struct{})
		for s.decoder.More() {
			items++
			if items > architectureV2ResolveMaxCollectionItems {
				return fmt.Errorf("%s contains an object with more than %d fields", s.field, architectureV2ResolveMaxCollectionItems)
			}
			rawKey, err := s.nextToken()
			if err != nil {
				return err
			}
			key, ok := rawKey.(string)
			if !ok {
				return fmt.Errorf("%s contains a non-string JSON field name", s.field)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("%s contains duplicate field %q", s.field, key)
			}
			seen[key] = struct{}{}
			if err := s.scanValue(depth + 1); err != nil {
				return err
			}
		}
	} else {
		for s.decoder.More() {
			items++
			if items > architectureV2ResolveMaxCollectionItems {
				return fmt.Errorf("%s contains an array with more than %d items", s.field, architectureV2ResolveMaxCollectionItems)
			}
			if err := s.scanValue(depth + 1); err != nil {
				return err
			}
		}
	}
	closing, err := s.nextToken()
	if err != nil {
		return err
	}
	want := json.Delim('}')
	if open == '[' {
		want = ']'
	}
	if closing != want {
		return fmt.Errorf("%s contains mismatched JSON delimiters", s.field)
	}
	return nil
}

func writeArchitectureV2RequestError(w http.ResponseWriter, err error) {
	var requestErr *architectureV2RequestError
	if !errors.As(err, &requestErr) {
		writeArchitectureV2ResolveError(w, http.StatusBadRequest, &architecturev2.ResolveError{
			Code: architecturev2.ErrInvalidStackSpec, Message: "invalid Architecture v2 resolve request",
		})
		return
	}
	writeArchitectureV2ResolveError(w, requestErr.status, &architecturev2.ResolveError{
		Code: requestErr.code, Message: requestErr.message,
	})
}

func writeMappedArchitectureV2ResolveError(w http.ResponseWriter, err error) {
	var resolveErr *architecturev2.ResolveError
	if !errors.As(err, &resolveErr) {
		slog.Error("unexpected Architecture v2 resolution error", "error", err)
		writeArchitectureV2ResolveError(w, http.StatusInternalServerError, &architecturev2.ResolveError{
			Code: architecturev2.ErrResolveFailed, Message: "internal error",
		})
		return
	}

	var status int
	publicError := &architecturev2.ResolveError{
		Code: resolveErr.Code, Message: resolveErr.Message, Report: resolveErr.Report,
	}
	switch resolveErr.Code {
	case architecturev2.ErrInvalidStackSpec, architecturev2.ErrInvalidInventory:
		status = http.StatusBadRequest
	case architecturev2.ErrRequestTooLarge:
		status = http.StatusRequestEntityTooLarge
	case architecturev2.ErrUnsupportedMedia:
		status = http.StatusUnsupportedMediaType
	case architecturev2.ErrResolveBusy:
		status = http.StatusTooManyRequests
		w.Header().Set("Retry-After", architectureV2ResolveRetryAfterSeconds)
	case architecturev2.ErrMigrationRequired, architecturev2.ErrMigrationBlocked, architecturev2.ErrResolveFailed:
		status = http.StatusUnprocessableEntity
	case architecturev2.ErrOperationalUnavailable:
		status = http.StatusNotImplemented
	case architecturev2.ErrAuthorityLoad:
		status = http.StatusServiceUnavailable
		publicError.Message = "Architecture v2 authority is unavailable"
		slog.Error("Architecture v2 authority unavailable", "error", err)
	default:
		status = http.StatusInternalServerError
		publicError.Code = architecturev2.ErrResolveFailed
		publicError.Message = "internal error"
		slog.Error("unmapped Architecture v2 resolution error", "error", err)
	}
	writeArchitectureV2ResolveError(w, status, publicError)
}

func writeArchitectureV2ResolveError(w http.ResponseWriter, status int, resolveErr *architecturev2.ResolveError) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(resolveErr); err != nil {
		slog.Error("failed to encode Architecture v2 error response", "error", err)
	}
}
