package commands

import (
	"fmt"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/spf13/cobra"
	"strings"
)

func rilPrimitiveHash(id string) (string, error) {
	s, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return "", err
	}
	hash, err := s.RILPrimitiveHash(id)
	if err != nil {
		return "", err
	}
	if id == "verify-stackkit-state" && hash != architecturev2.ProductRILVerifyHashV1 {
		return "", fmt.Errorf("embedded RIL primitive capability hash differs from catalog")
	}
	return hash, nil
}
func requireRILPrimitiveHash(id, expected string) error {
	if id != "verify-stackkit-state" || expected == "" {
		return fmt.Errorf("RIL primitive identity required")
	}
	actual, err := rilPrimitiveHash(id)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("RIL primitive contract hash mismatch")
	}
	return nil
}
func init() {
	var expected string
	c := &cobra.Command{Use: "ril-primitive-hash <id>", Short: "Read the verified embedded RIL primitive contract hash", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		h, err := rilPrimitiveHash(args[0])
		if err != nil {
			return err
		}
		if strings.TrimSpace(expected) != "" && expected != h {
			return fmt.Errorf("RIL primitive contract hash mismatch")
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), h)
		return err
	}}
	c.Flags().StringVar(&expected, "expect", "", "Refuse a different primitive contract hash")
	rootCmd.AddCommand(c)
}
