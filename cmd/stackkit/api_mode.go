package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	apicli "github.com/kombifyio/stackkits/internal/apisurfacev2/cli"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/kombifyio/stackkits/api/surface"
	"github.com/kombifyio/stackkits/internal/stackkitmcp"
)

// API client environment keys, shared with stackkit-mcp. The default base URL
// is the loopback stackkit-server of Standard Mode, never a hosted origin.
const (
	envServerURL = "STACKKITS_SERVER_URL"
	envAPIKey    = "STACKKITS_API_KEY"
)

// isAPIMode reports whether the binary was invoked as `stackkit api ...`, the
// generated client for every published stackkit-server contract operation.
func isAPIMode(args []string) bool {
	return len(args) > 1 && strings.TrimSpace(args[1]) == "api"
}

// runAPIMode executes one generated contract command outside the lifecycle
// command tree, so no deploy logging or rollout evidence starts, and returns
// the generated CLI's stable exit code.
func runAPIMode(ctx context.Context, args []string) int {
	s, err := apicli.LoadSurface(surface.Raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: load api surface:", err)
		return apicli.ExitFailure
	}
	root := &cobra.Command{Use: "stackkit", SilenceErrors: true, SilenceUsage: true}
	api := &cobra.Command{
		Use:           "api",
		Short:         "Call the local stackkit-server API (generated from the OpenAPI contract)",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.AddCommand(api)
	base := strings.TrimRight(stackkitmcp.FirstNonEmpty(os.Getenv(envServerURL), stackkitmcp.DefaultLocalServerURL), "/")
	err = apicli.Mount(api, s, apicli.Config{
		BaseURL:     func(context.Context) (string, error) { return base, nil },
		HTTPClient:  &http.Client{Timeout: 60 * time.Second},
		Authorize:   authorizeAPIRequest,
		Interactive: func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: mount api commands:", err)
		return apicli.ExitFailure
	}
	root.SetArgs(append([]string{"api"}, args...))
	if err := root.ExecuteContext(ctx); err != nil {
		var exitErr *apicli.ExitError
		if !errors.As(err, &exitErr) {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		return apicli.ExitCode(err)
	}
	return 0
}

// authorizeAPIRequest sends STACKKITS_API_KEY as the contract's X-API-Key
// scheme when set; a local development server may run without one.
func authorizeAPIRequest(req *http.Request) error {
	if key := strings.TrimSpace(os.Getenv(envAPIKey)); key != "" {
		req.Header.Set("X-API-Key", key)
	}
	return nil
}
