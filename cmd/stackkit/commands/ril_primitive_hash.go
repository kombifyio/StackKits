package commands

import (
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/spf13/cobra"
)

// rilPrimitiveAnnotation names the one executor-bound RIL primitive a command
// realizes. Its --expected-ril-primitive-hash flag is checked against that
// primitive only, so a proof for one primitive never admits another command.
const rilPrimitiveAnnotation = "stackkit.ril-primitive"

const expectedRILPrimitiveHashFlag = "expected-ril-primitive-hash"

func rilPrimitiveHash(id string) (string, error) {
	embedded, ok := architecturev2.EmbeddedRILPrimitiveHash(id)
	if !ok {
		return "", fmt.Errorf("RIL primitive unavailable")
	}
	s, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return "", err
	}
	hash, err := s.RILPrimitiveHash(id)
	if err != nil {
		return "", err
	}
	if hash != embedded {
		return "", fmt.Errorf("embedded RIL primitive capability hash differs from catalog")
	}
	return hash, nil
}

func requireRILPrimitiveHash(id, expected string) error {
	if id == "" || expected == "" {
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

// bindRILPrimitive declares the command's primitive and its expected-hash flag.
func bindRILPrimitive(cmd *cobra.Command, id string) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[rilPrimitiveAnnotation] = id
	cmd.Flags().String(expectedRILPrimitiveHashFlag, "", "Require the verified embedded contract hash of the "+id+" RIL primitive")
}

// rilPrimitiveActionFlagAnnotation names the flag whose value selects the
// command's primitive (application control: its power action).
const rilPrimitiveActionFlagAnnotation = "stackkit.ril-primitive-action-flag"

// rilPrimitiveActionAnnotationPrefix maps one value of that flag to its
// primitive id.
const rilPrimitiveActionAnnotationPrefix = "stackkit.ril-primitive-action:"

// bindRILPrimitiveByAction declares a command whose primitive is selected by
// the value of one of its flags, and its expected-hash flag. A value outside
// the map has no primitive, so a supplied hash refuses.
func bindRILPrimitiveByAction(cmd *cobra.Command, actionFlag string, primitives map[string]string) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[rilPrimitiveActionFlagAnnotation] = actionFlag
	for action, id := range primitives {
		cmd.Annotations[rilPrimitiveActionAnnotationPrefix+action] = id
	}
	cmd.Flags().String(expectedRILPrimitiveHashFlag, "", "Require the verified embedded contract hash of the RIL primitive of this action")
}

// admitRILPrimitive refuses a command whose --expected-ril-primitive-hash
// differs from the embedded hash of the command's own primitive.
func admitRILPrimitive(cmd *cobra.Command) error {
	flag := cmd.Flags().Lookup(expectedRILPrimitiveHashFlag)
	if flag == nil || !flag.Changed {
		return nil
	}
	id := cmd.Annotations[rilPrimitiveAnnotation]
	if actionFlag := cmd.Annotations[rilPrimitiveActionFlagAnnotation]; actionFlag != "" {
		id = ""
		if action := cmd.Flags().Lookup(actionFlag); action != nil {
			id = cmd.Annotations[rilPrimitiveActionAnnotationPrefix+action.Value.String()]
		}
	}
	return requireRILPrimitiveHash(id, flag.Value.String())
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
