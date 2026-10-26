package commands

import (
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/hostsecurity"
	"github.com/kombifyio/stackkits/internal/runtimeprofile"
	"github.com/spf13/cobra"
)

// This is the operator entry to the same owner used by native pre-start
// admission. It accepts a verified product plan, never raw firewall rules.
func newHostSecurityNetworkCommand() *cobra.Command {
	var path, node string
	var apply bool
	cmd := &cobra.Command{Use: "network", Short: "Admit the verified Full Local cohort's container destinations", Args: machineAwareNoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		file, err := os.Open(resolvePathFromWorkDir(getWorkDir(), path))
		if err != nil {
			return err
		}
		defer file.Close()
		raw, err := io.ReadAll(io.LimitReader(file, 32<<20+1))
		if err != nil {
			return err
		}
		if len(raw) > 32<<20 {
			return errors.New("resolved plan exceeds limit")
		}
		service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
		if err != nil {
			return err
		}
		plan, err := service.VerifyCanonicalPlan(raw)
		if err != nil {
			return err
		}
		network, err := architecturev2renderer.LocalNetworkForPlan(plan)
		if err != nil {
			return err
		}
		if network == nil {
			return errors.New("network admission requires a Full Local plan")
		}
		if err := runtimeprofile.AdmitSpec("full-local"); err != nil {
			return err
		}
		var identity struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
			PlanHash string `json:"planHash"`
		}
		if err := json.Unmarshal(plan.Canonical(), &identity); err != nil {
			return err
		}
		found := false
		for _, candidate := range identity.Nodes {
			if candidate.ID == node {
				found = true
			}
		}
		if !found {
			return errors.New("--local-node must name the exact planned local node")
		}
		policy := hostsecurity.CoreContainerDestinations(network.StackID)
		if apply {
			if err := (hostsecurity.Engine{Host: hostsecurity.LocalHost{}}).EnforceContainerDestinations(cmd.Context(), policy); err != nil {
				return err
			}
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			PlanHash string                                  `json:"planHash"`
			Applied  bool                                    `json:"applied"`
			Policy   hostsecurity.ContainerDestinationPolicy `json:"policy"`
		}{identity.PlanHash, apply, policy})
	}}
	cmd.Flags().StringVar(&path, "resolved-plan", "", "Verified canonical Full Local ResolvedPlan")
	cmd.Flags().StringVar(&node, "local-node", "", "Exact local node in that plan")
	cmd.Flags().BoolVar(&apply, "apply", false, "Install and independently read back the scoped destination policy")
	_ = cmd.MarkFlagRequired("resolved-plan")
	_ = cmd.MarkFlagRequired("local-node")
	return cmd
}
