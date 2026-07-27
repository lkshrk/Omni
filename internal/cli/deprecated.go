package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Catalog coverage skips commands carrying this annotation: the canonical verb owns the binding.
const annotationDeprecatedAlias = "omni.deprecated_alias"

func deprecatedAlias(canonical *cobra.Command, use string, prepare func()) *cobra.Command {
	return deprecatedAliasNamed(canonical, use, "", prepare)
}

// The flag set is shared rather than copied so every flag keeps working on the old spelling; prepare pins whatever flag state that spelling implied.
// replacement must be given whenever prepare changes behaviour, since the bare canonical verb is then not an equivalent.
func deprecatedAliasNamed(canonical *cobra.Command, use, replacement string, prepare func()) *cobra.Command {
	alias := &cobra.Command{
		Use:               use,
		Short:             canonical.Short,
		Long:              canonical.Long,
		Args:              canonical.Args,
		Hidden:            true,
		ValidArgsFunction: canonical.ValidArgsFunction,
		Annotations:       map[string]string{annotationDeprecatedAlias: canonical.Name()},
		RunE: func(cmd *cobra.Command, args []string) error {
			printRenameNotice(cmd, canonical, replacement)
			if prepare != nil {
				prepare()
			}
			return canonical.RunE(cmd, args)
		},
	}
	alias.Flags().AddFlagSet(canonical.Flags())
	return alias
}

// Goes to stderr so scripted stdout consumers keep parsing the command's real output.
func printRenameNotice(alias *cobra.Command, canonical *cobra.Command, replacement string) {
	path := commandPath(alias)
	if replacement == "" {
		replacement = replaceLeaf(path, canonical.Name())
	}
	fmt.Fprintf(alias.ErrOrStderr(), "note: %q renamed to %q\n", path, replacement)
}

// For an old spelling that kept its own behaviour instead of aliasing, so the replacement must be named explicitly.
func deprecationNotice(cmd *cobra.Command, replacement string) {
	fmt.Fprintf(cmd.ErrOrStderr(), "note: %q is deprecated; use %q\n", commandPath(cmd), replacement)
}

func commandPath(cmd *cobra.Command) string {
	path := cmd.CommandPath()
	return strings.TrimPrefix(path, "omni ")
}

func replaceLeaf(path, leaf string) string {
	parts := strings.Fields(path)
	if len(parts) == 0 {
		return leaf
	}
	parts[len(parts)-1] = leaf
	return strings.Join(parts, " ")
}
