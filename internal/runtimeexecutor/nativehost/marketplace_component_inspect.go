package nativehost

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/kombifyio/stackkits/internal/docker"
)

var errNativeComponentPreflight = errors.New("native component configuration is not admitted")

// This native preflight only reads existing membership/configuration. It runs
// before recording preparation or stopping any writer, including resume.
func inspectMarketplaceComponent(ctx context.Context, container string, q marketplaceStoreQuery) error {
	q.Action = "inspect"
	command, err := marketplaceStoreCommand(q)
	if err != nil {
		return err
	}
	raw, err := docker.NewLocalClient().Exec(ctx, container, command)
	if err != nil {
		return errNativeComponentPreflight
	}
	var member struct {
		Server    string `json:"server"`
		Service   string `json:"service"`
		Component string `json:"component"`
		Name      string `json:"name"`
	}
	if json.Unmarshal([]byte(raw), &member) != nil || member.Server != q.Server || member.Service != q.Service || member.Component != q.Component || member.Name != q.Name {
		return errNativeComponentPreflight
	}
	return nil
}
