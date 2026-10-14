package appsetup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
)

// OllamaModelsAction is the owner-approved model manager of the Private AI
// workload: list the models installed in the node's Ollama, pull one reviewed
// model, or remove an installed one. Nothing is pulled at install and nothing
// outside the reviewed table (privateAIModelTags) is ever pulled.
const OllamaModelsAction = "ollama-models"

const (
	OllamaOperationList   = "list"
	OllamaOperationPull   = "pull"
	OllamaOperationRemove = "remove"

	// OllamaDiskMarginBytes is kept free on the models volume on top of the
	// model's declared size.
	OllamaDiskMarginBytes int64 = 2_000_000_000
)

// Typed refusals; callers use errors.Is.
var (
	ErrOllamaOperationUnknown     = errors.New("model manager operation must be list, pull or remove")
	ErrOllamaModelNotReviewed     = errors.New("model is not in the reviewed Private AI model list")
	ErrOllamaLicenseNotAccepted   = errors.New("the model license was not accepted")
	ErrOllamaModelSizeUnknown     = errors.New("the model has no declared size, so its disk budget cannot be checked")
	ErrOllamaInsufficientDisk     = errors.New("not enough free space on the Ollama models volume")
	ErrOllamaModelNotInstalled    = errors.New("model is not installed")
	ErrOllamaModelNotVerified     = errors.New("Ollama does not list the model after the operation")
	ErrOllamaModelNameNotAccepted = errors.New("model name is not a valid Ollama tag")
)

var ollamaModelTagPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}:[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// OllamaModelsRequest is the owner's private input.
type OllamaModelsRequest struct {
	Operation string `json:"operation"`
	Model     string `json:"model"`
	// AcceptLicense must equal the model's license identifier to pull it.
	AcceptLicense string `json:"acceptLicense"`
}

// OllamaInstalledModel is one row of `ollama list`.
type OllamaInstalledModel struct {
	Name      string `json:"name"`
	ID        string `json:"id"`
	SizeBytes int64  `json:"sizeBytes"`
	Modified  string `json:"modified"`
}

// OllamaModelsResult is the structured outcome of one operation.
type OllamaModelsResult struct {
	Operation string                 `json:"operation"`
	Model     string                 `json:"model,omitempty"`
	Installed []OllamaInstalledModel `json:"installed"`
	// Skipped is set when a pull found the model already installed.
	Skipped bool `json:"skipped,omitempty"`
}

// OllamaHost is the node's Ollama as the model manager sees it.
type OllamaHost interface {
	// Ollama runs `ollama <args...>`, writing progress to out as it happens.
	Ollama(ctx context.Context, out io.Writer, args ...string) ([]byte, error)
	// FreeBytes is the free space of the Ollama models volume.
	FreeBytes(ctx context.Context) (int64, error)
}

// ValidateOllamaModelsRequest checks the request shape without touching the
// node, so the CLI can refuse before any lifecycle operation begins.
func ValidateOllamaModelsRequest(request OllamaModelsRequest) (OllamaModelsRequest, error) {
	request.Operation, request.Model = strings.TrimSpace(request.Operation), strings.TrimSpace(request.Model)
	request.AcceptLicense = strings.TrimSpace(request.AcceptLicense)
	switch request.Operation {
	case OllamaOperationList:
		if request.Model != "" {
			return request, errors.New("the list operation takes no model")
		}
	case OllamaOperationPull:
		if !ollamaModelTagPattern.MatchString(request.Model) {
			return request, fmt.Errorf("%w: %q", ErrOllamaModelNameNotAccepted, request.Model)
		}
		model, reviewed := architecturev2renderer.ResolvePrivateAIReviewedModel(request.Model)
		if !reviewed {
			return request, fmt.Errorf("%w: %s; reviewed models are %s", ErrOllamaModelNotReviewed, request.Model, reviewedOllamaTags())
		}
		if request.AcceptLicense != model.License {
			return request, fmt.Errorf("%w: %s is licensed %s; set acceptLicense to %q to accept it", ErrOllamaLicenseNotAccepted, model.Tag, model.License, model.License)
		}
		if model.Bytes <= 0 {
			return request, fmt.Errorf("%w: %s", ErrOllamaModelSizeUnknown, model.Tag)
		}
	case OllamaOperationRemove:
		if !ollamaModelTagPattern.MatchString(request.Model) {
			return request, fmt.Errorf("%w: %q", ErrOllamaModelNameNotAccepted, request.Model)
		}
	default:
		return request, fmt.Errorf("%w, not %q", ErrOllamaOperationUnknown, request.Operation)
	}
	return request, nil
}

func reviewedOllamaTags() string {
	var tags []string
	for _, model := range architecturev2renderer.PrivateAIReviewedModels() {
		tags = append(tags, model.Tag)
	}
	return strings.Join(tags, ", ")
}

// RunOllamaModels performs one operation. Pull refuses an unreviewed model, an
// unaccepted license and a full disk before it contacts the registry, streams
// Ollama's progress to progress and requires the model in `ollama list`
// afterwards. Remove only removes an installed model. Cancelling ctx stops the
// pull.
func RunOllamaModels(ctx context.Context, host OllamaHost, request OllamaModelsRequest, progress io.Writer) (OllamaModelsResult, error) {
	request, err := ValidateOllamaModelsRequest(request)
	if err != nil {
		return OllamaModelsResult{}, err
	}
	result := OllamaModelsResult{Operation: request.Operation, Model: request.Model}
	installed, err := listOllamaModels(ctx, host)
	if err != nil {
		return OllamaModelsResult{}, err
	}
	has := func(models []OllamaInstalledModel) bool {
		return slices.ContainsFunc(models, func(m OllamaInstalledModel) bool { return m.Name == request.Model })
	}
	switch request.Operation {
	case OllamaOperationList:
		result.Installed = installed
		return result, nil
	case OllamaOperationPull:
		if has(installed) {
			result.Installed, result.Skipped = installed, true
			return result, nil
		}
		model, _ := architecturev2renderer.ResolvePrivateAIReviewedModel(request.Model)
		free, err := host.FreeBytes(ctx)
		if err != nil {
			return OllamaModelsResult{}, err
		}
		if need := model.Bytes + OllamaDiskMarginBytes; free < need {
			return OllamaModelsResult{}, fmt.Errorf("%w: %s needs about %.1f GB plus a %.1f GB margin, %.1f GB is free", ErrOllamaInsufficientDisk,
				model.Tag, float64(model.Bytes)/1e9, float64(OllamaDiskMarginBytes)/1e9, float64(free)/1e9)
		}
		if _, err := host.Ollama(ctx, progress, "pull", request.Model); err != nil {
			return OllamaModelsResult{}, fmt.Errorf("pull %s: %w", request.Model, err)
		}
	case OllamaOperationRemove:
		if !has(installed) {
			return OllamaModelsResult{}, fmt.Errorf("%w: %s", ErrOllamaModelNotInstalled, request.Model)
		}
		if _, err := host.Ollama(ctx, progress, "rm", request.Model); err != nil {
			return OllamaModelsResult{}, fmt.Errorf("remove %s: %w", request.Model, err)
		}
	}
	after, err := listOllamaModels(ctx, host)
	if err != nil {
		return OllamaModelsResult{}, err
	}
	if has(after) != (request.Operation == OllamaOperationPull) {
		return OllamaModelsResult{}, fmt.Errorf("%w: %s", ErrOllamaModelNotVerified, request.Model)
	}
	result.Installed = after
	return result, nil
}

func listOllamaModels(ctx context.Context, host OllamaHost) ([]OllamaInstalledModel, error) {
	raw, err := host.Ollama(ctx, nil, "list")
	if err != nil {
		return nil, fmt.Errorf("list Ollama models: %w", err)
	}
	return ParseOllamaList(string(raw))
}

var ollamaColumnSplit = regexp.MustCompile(`\s{2,}`)

// ParseOllamaList reads `ollama list`: a NAME/ID/SIZE/MODIFIED table whose
// columns are separated by two or more spaces. Anything else is an error, not
// an empty list.
func ParseOllamaList(output string) ([]OllamaInstalledModel, error) {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(output, "\r", "")), "\n")
	if len(lines) == 0 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "NAME") {
		return nil, errors.New("unexpected `ollama list` output")
	}
	models := []OllamaInstalledModel{}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		columns := ollamaColumnSplit.Split(strings.TrimSpace(line), -1)
		if len(columns) < 3 {
			return nil, fmt.Errorf("unexpected `ollama list` row %q", line)
		}
		size, err := parseOllamaSize(columns[2])
		if err != nil {
			return nil, err
		}
		model := OllamaInstalledModel{Name: columns[0], ID: columns[1], SizeBytes: size}
		if len(columns) > 3 {
			model.Modified = columns[3]
		}
		models = append(models, model)
	}
	return models, nil
}

func parseOllamaSize(value string) (int64, error) {
	number, unit, found := strings.Cut(strings.TrimSpace(value), " ")
	factor, known := map[string]float64{"B": 1, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12}[strings.TrimSpace(unit)]
	parsed, err := strconv.ParseFloat(number, 64)
	if !found || !known || err != nil || parsed < 0 {
		return 0, fmt.Errorf("unexpected `ollama list` size %q", value)
	}
	return int64(parsed * factor), nil
}
