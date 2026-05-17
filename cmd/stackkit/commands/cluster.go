package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/cluster"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

var (
	clusterTokenHomelabID string
	clusterTokenMainNode  string
	clusterTokenEndpoint  string
	clusterTokenTTL       time.Duration
	clusterTokenOutput    string
)

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Manage StackKits multi-node cluster membership",
}

var clusterJoinTokenCmd = &cobra.Command{
	Use:   "join-token",
	Short: "Create a join token for worker or storage nodes",
	Long: `Create a join token for worker or storage nodes.

The token is the only supported path for treating another BaseKit install as a
member of this homelab. Coolify remote-server registration and TechStack mirror
updates consume this cluster identity in later rollout steps.`,
	RunE: runClusterJoinToken,
}

func init() {
	clusterJoinTokenCmd.Flags().StringVar(&clusterTokenHomelabID, "homelab-id", "", "Homelab ID (defaults to stack spec name)")
	clusterJoinTokenCmd.Flags().StringVar(&clusterTokenMainNode, "main-node", "", "Main node ID/name (defaults to the main-like node in stack spec)")
	clusterJoinTokenCmd.Flags().StringVar(&clusterTokenEndpoint, "endpoint", "", "Main node endpoint workers use to join")
	clusterJoinTokenCmd.Flags().DurationVar(&clusterTokenTTL, "ttl", 24*time.Hour, "Token validity duration")
	clusterJoinTokenCmd.Flags().StringVarP(&clusterTokenOutput, "output", "o", filepath.Join(".stackkit", "cluster", "join-token.json"), "Output path for the token JSON")
	clusterCmd.AddCommand(clusterJoinTokenCmd)
}

func runClusterJoinToken(cmd *cobra.Command, args []string) error {
	wd := getWorkDir()
	spec, _ := config.NewLoader(wd).LoadStackSpec(specFile)

	homelabID := clusterTokenHomelabID
	if homelabID == "" && spec != nil {
		homelabID = spec.Name
	}
	main := cluster.MainNode{
		ID:       clusterTokenMainNode,
		Name:     clusterTokenMainNode,
		Endpoint: clusterTokenEndpoint,
	}
	if spec != nil {
		main = applySpecClusterDefaults(spec, main)
	}

	token, err := cluster.GenerateJoinToken(homelabID, main, clusterTokenTTL)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal join token: %w", err)
	}
	data = append(data, '\n')

	if clusterTokenOutput != "" {
		outPath := clusterTokenOutput
		if !filepath.IsAbs(outPath) {
			outPath = filepath.Join(wd, outPath)
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0750); err != nil {
			return fmt.Errorf("create token output directory: %w", err)
		}
		if err := os.WriteFile(outPath, data, 0600); err != nil {
			return fmt.Errorf("write join token: %w", err)
		}
		printSuccess("Wrote join token: %s", outPath)
	}

	_, err = fmt.Fprintln(cmd.OutOrStdout(), token.Token)
	return err
}

func applySpecClusterDefaults(spec *models.StackSpec, main cluster.MainNode) cluster.MainNode {
	if main.ID == "" {
		if node, ok := mainNodeFromSpec(spec); ok {
			main.ID = node.Name
			main.Name = node.Name
			if node.Host != "" {
				main.Endpoint = node.Host
			}
		}
	}
	if main.Endpoint == "" {
		main.Endpoint = defaultMainEndpoint(spec)
	}
	return main
}

func mainNodeFromSpec(spec *models.StackSpec) (models.NodeSpec, bool) {
	if spec == nil {
		return models.NodeSpec{}, false
	}
	for _, node := range spec.Nodes {
		if models.IsMainNodeRole(node.Role) {
			return node, true
		}
	}
	if len(spec.Nodes) == 1 {
		return spec.Nodes[0], true
	}
	return models.NodeSpec{}, false
}

func defaultMainEndpoint(spec *models.StackSpec) string {
	domain := strings.TrimSpace(spec.Domain)
	if domain == "" {
		domain = models.DomainHomeLab
	}
	proto := "http"
	if !models.IsLocalDomain(domain) || models.IsKombifyMeDomain(domain) {
		proto = "https"
	}
	if spec.SubdomainPrefix != "" {
		return fmt.Sprintf("%s://%s-base.%s", proto, spec.SubdomainPrefix, domain)
	}
	return fmt.Sprintf("%s://base.%s", proto, domain)
}
