package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	skerrors "github.com/kombifyio/stackkits/internal/errors"
	"github.com/kombifyio/stackkits/internal/tofu"
)

func stackActionOperationResultError(operation string, result *tofu.Result, err error) error {
	if err != nil {
		return err
	}
	if result == nil {
		return fmt.Errorf("%s did not return a result", operation)
	}
	if !result.Success {
		detail := resultStderrStackAction(result)
		if strings.TrimSpace(detail) == "" {
			detail = fmt.Sprintf("%s exited with code %d", operation, result.ExitCode)
		}
		return fmt.Errorf("%s failed: %s", operation, detail)
	}
	return nil
}

func requireLocalTofuDirStackAction(dir string) *skerrors.StackKitError {
	if strings.TrimSpace(dir) == "" {
		return skerrors.NewValidationError("missing_tofu_dir", "tofu_dir is required in apply mode")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return skerrors.NewValidationError("invalid_tofu_dir", "tofu_dir must be readable in apply mode", skerrors.WithField("path", dir), skerrors.WithField("error", err.Error()))
	}
	if !info.IsDir() {
		return skerrors.NewValidationError("invalid_tofu_dir", "tofu_dir must be a directory in apply mode", skerrors.WithField("path", dir))
	}
	hasTF, err := tofu.HasTerraformFiles(filepath.Clean(dir))
	if err != nil {
		return skerrors.NewValidationError("invalid_tofu_dir", "failed to inspect tofu_dir", skerrors.WithField("path", dir), skerrors.WithField("error", err.Error()))
	}
	if !hasTF {
		return skerrors.NewValidationError("missing_tofu_files", "tofu_dir must contain .tf files in apply mode", skerrors.WithField("path", dir))
	}
	return nil
}

func tofuActionErrorStackAction(code, message string, err error, stderr string) *skerrors.StackKitError {
	fields := []skerrors.ErrorOption{}
	if err != nil {
		fields = append(fields, skerrors.WithField("error", err.Error()))
	}
	if strings.TrimSpace(stderr) != "" {
		fields = append(fields, skerrors.WithField("stderr", strings.TrimSpace(stderr)))
	}
	return skerrors.NewDeploymentError(code, message, fields...)
}

func resultStderrStackAction(result *tofu.Result) string {
	if result == nil {
		return ""
	}
	return compactStackActionStderr(result.Stderr)
}

func compactStackActionStderr(stderr string) string {
	const maxRunes = 6000
	trimmed := strings.TrimSpace(stderr)
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) <= maxRunes {
		return trimmed
	}
	return fmt.Sprintf("[stderr truncated; showing last %d runes]\n%s", maxRunes, string(runes[len(runes)-maxRunes:]))
}
