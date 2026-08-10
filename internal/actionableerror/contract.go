// Package actionableerror owns the stable CLI/MCP recovery guidance envelope.
package actionableerror

import (
	"errors"
	"fmt"
	"strings"
)

const SchemaVersionV1 = "stackkit.actionable-error/v1"

type Contract struct {
	SchemaVersion string   `json:"schemaVersion"`
	Code          string   `json:"code"`
	ReasonCode    string   `json:"reasonCode"`
	Message       string   `json:"message"`
	UserGuidance  []string `json:"userGuidance"`
	Retryable     bool     `json:"retryable"`
}

func New(code, reason, message string, guidance []string, retryable bool) Contract {
	clean := make([]string, 0, len(guidance))
	for _, item := range guidance {
		if item = strings.TrimSpace(item); item != "" {
			clean = append(clean, item)
		}
	}
	return Contract{
		SchemaVersion: SchemaVersionV1,
		Code:          strings.TrimSpace(code), ReasonCode: strings.TrimSpace(reason), Message: strings.TrimSpace(message),
		UserGuidance: clean, Retryable: retryable,
	}
}

func (c Contract) Validate() error {
	if c.SchemaVersion != SchemaVersionV1 {
		return fmt.Errorf("actionable error schemaVersion must be %q", SchemaVersionV1)
	}
	for field, value := range map[string]string{"code": c.Code, "reasonCode": c.ReasonCode, "message": c.Message} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("actionable error %s is required and canonical", field)
		}
	}
	if len(c.UserGuidance) == 0 {
		return errors.New("actionable error requires at least one recovery action")
	}
	for index, guidance := range c.UserGuidance {
		if strings.TrimSpace(guidance) == "" || guidance != strings.TrimSpace(guidance) || strings.ContainsRune(guidance, '\x00') {
			return fmt.Errorf("actionable error userGuidance[%d] is not canonical", index)
		}
	}
	return nil
}
