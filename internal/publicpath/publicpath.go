// Package publicpath is the single source of the commands the public install
// text prints: the install command, the init command a fresh workspace accepts
// and the steps that follow it. The CLI hints, the published documents and the
// public-package proof all derive from public-path.json, so the text a person
// copies and the product that runs it cannot drift apart
// (scripts/docs/public-path.mjs rewrites and checks the published copies).
package publicpath

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed public-path.json
var sourceJSON []byte

type source struct {
	SchemaVersion         string `json:"schemaVersion"`
	InstallCommand        string `json:"installCommand"`
	CLIOnlyInstallCommand string `json:"cliOnlyInstallCommand"`
	VersionCommand        string `json:"versionCommand"`
	DefaultKit            string `json:"defaultKit"`
	WorkspaceDir          string `json:"workspaceDir"`
	OwnerEmail            struct {
		Example     string `json:"example"`
		Placeholder string `json:"placeholder"`
	} `json:"ownerEmail"`
	Init struct {
		SelectionFlag      string `json:"selectionFlag"`
		OwnerFlag          string `json:"ownerFlag"`
		EmailFlag          string `json:"emailFlag"`
		DomainFlag         string `json:"domainFlag"`
		NonInteractiveFlag string `json:"nonInteractiveFlag"`
	} `json:"init"`
	CustodyRepair struct {
		SpecFlag string `json:"specFlag"`
		SpecPath string `json:"specPath"`
	} `json:"custodyRepair"`
	AfterInit      []string `json:"afterInit"`
	AfterInstaller []string `json:"afterInstaller"`
	ConnectCommand string   `json:"connectCommand"`
	AccessEvidence string   `json:"accessEvidence"`
}

var public = mustLoad()

func mustLoad() source {
	var s source
	if err := json.Unmarshal(sourceJSON, &s); err != nil {
		panic(fmt.Sprintf("publicpath: embedded public-path.json: %v", err))
	}
	if s.SchemaVersion != "stackkits-public-path/v1" || s.InstallCommand == "" || s.ConnectCommand == "" || s.CLIOnlyInstallCommand == "" || s.DefaultKit == "" ||
		s.Init.SelectionFlag == "" || s.Init.OwnerFlag == "" || s.Init.EmailFlag == "" ||
		s.OwnerEmail.Example == "" || s.OwnerEmail.Placeholder == "" ||
		s.CustodyRepair.SpecFlag == "" || s.CustodyRepair.SpecPath == "" {
		panic("publicpath: embedded public-path.json is incomplete")
	}
	return s
}

// ExampleEmail is the owner address the published text shows.
func ExampleEmail() string { return public.OwnerEmail.Example }

// EmailPlaceholder is the owner address placeholder agent text shows.
func EmailPlaceholder() string { return public.OwnerEmail.Placeholder }

// InstallCommand is the public install command.
func InstallCommand() string { return public.InstallCommand }

// CLIOnlyInstallCommand is the install command that stops after the CLI and the
// public kit catalog, without installing a kit.
func CLIOnlyInstallCommand() string { return public.CLIOnlyInstallCommand }

// InstallerResult is what a person runs after the one-command installer has
// finished: into the workspace it created, then the checks that show the result.
func InstallerResult() []string {
	return append([]string{"cd ~/" + public.WorkspaceDir}, public.AfterInstaller...)
}

// ConnectCommand is the offer at the end of the public path: bind this server
// to the kombify account that created the pairing (stackkit connect).
func ConnectCommand() string { return public.ConnectCommand }

// AccessEvidence is the file that names the first reachable app.
func AccessEvidence() string { return public.AccessEvidence }

// DefaultKit is the Kit `stackkit init` selects when none is named.
func DefaultKit() string { return public.DefaultKit }

// InitOptions selects the parts of a published init command.
type InitOptions struct {
	// Kit is omitted from the command when empty (init then selects the default Kit).
	Kit string
	// Email defaults to the example address.
	Email string
	// Domain adds the domain flag when non-empty.
	Domain         string
	NonInteractive bool
}

// InitCommand renders the init command a fresh workspace accepts: a Kit
// selection, the local owner and the owner address the local PocketID owner
// needs.
func InitCommand(o InitOptions) string {
	email := strings.TrimSpace(o.Email)
	if email == "" {
		email = public.OwnerEmail.Example
	}
	parts := []string{"stackkit", "init"}
	if kit := strings.TrimSpace(o.Kit); kit != "" {
		parts = append(parts, kit)
	}
	parts = append(parts, public.Init.SelectionFlag, public.Init.OwnerFlag, public.Init.EmailFlag, email)
	if domain := strings.TrimSpace(o.Domain); domain != "" {
		parts = append(parts, public.Init.DomainFlag, domain)
	}
	if o.NonInteractive {
		parts = append(parts, public.Init.NonInteractiveFlag)
	}
	return strings.Join(parts, " ")
}

// CustodyRepairCommand establishes the local owner for a workspace that holds a
// StackSpec and no owner custody. It re-persists the spec unchanged, so no
// expected spec hash and no new selection are needed.
func CustodyRepairCommand(specPath string) string {
	specPath = strings.TrimSpace(specPath)
	if specPath == "" {
		specPath = public.CustodyRepair.SpecPath
	}
	return strings.Join([]string{
		"stackkit", "init", public.CustodyRepair.SpecFlag, specPath,
		public.Init.OwnerFlag, public.Init.EmailFlag, public.OwnerEmail.Placeholder,
	}, " ")
}

// QuickStart is the published command chain after the install command: the
// workspace, the init command and the steps that follow it up to the access
// evidence of the first reachable app.
func QuickStart() []string {
	chain := []string{
		"mkdir " + public.WorkspaceDir + " && cd " + public.WorkspaceDir,
		InitCommand(InitOptions{}),
	}
	return append(chain, public.AfterInit...)
}
