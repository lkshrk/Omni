package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

const defaultProviderFlagHelp = "provider name (e.g. brew, node, python, pip, system)"

// Pass help="" to use the shared default text.
func addProviderFlag(cmd *cobra.Command, dst *string, help string) {
	if help == "" {
		help = defaultProviderFlagHelp
	}
	cmd.Flags().StringVar(dst, "provider", "", help)
}

func requireProvider(name string) error {
	if name == "" {
		return errors.New("--provider is required (e.g. --provider brew)")
	}
	return nil
}

func addFormatFlag(cmd *cobra.Command, dst *string, defaultVal string, allowed ...string) {
	help := fmt.Sprintf("output format (%s)", strings.Join(allowed, ", "))
	cmd.Flags().StringVar(dst, "format", defaultVal, help)
}

func validateFormat(got string, allowed ...string) error {
	for _, a := range allowed {
		if got == a {
			return nil
		}
	}
	return fmt.Errorf("unknown format %q (supported: %s)", got, strings.Join(allowed, ", "))
}
