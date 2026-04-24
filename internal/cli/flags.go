package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

const defaultProviderFlagHelp = "provider name (e.g. brew, node, python, pip, system)"

// addProviderFlag binds --provider to dst on cmd. Pass help="" to use the
// shared default text.
func addProviderFlag(cmd *cobra.Command, dst *string, help string) {
	if help == "" {
		help = defaultProviderFlagHelp
	}
	cmd.Flags().StringVar(dst, "provider", "", help)
}

// requireProvider returns an error if name is empty.
func requireProvider(name string) error {
	if name == "" {
		return errors.New("--provider is required (e.g. --provider brew)")
	}
	return nil
}

// addFormatFlag binds --format to dst on cmd with the given default. The
// help text lists the allowed values.
func addFormatFlag(cmd *cobra.Command, dst *string, defaultVal string, allowed ...string) {
	help := fmt.Sprintf("output format (%s)", strings.Join(allowed, ", "))
	cmd.Flags().StringVar(dst, "format", defaultVal, help)
}

// validateFormat returns nil if got is in allowed, else a uniform error.
func validateFormat(got string, allowed ...string) error {
	for _, a := range allowed {
		if got == a {
			return nil
		}
	}
	return fmt.Errorf("unknown format %q (supported: %s)", got, strings.Join(allowed, ", "))
}
