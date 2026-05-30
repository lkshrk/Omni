package cli

import (
	"io"
	"os"

	"github.com/spf13/cobra"

	textutil "github.com/lkshrk/omni/internal/text"
)

func cmdOut(cmd *cobra.Command) io.Writer {
	return textutil.SymbolsFromEnv().Writer(cmd.OutOrStdout())
}

func cmdErr(cmd *cobra.Command) io.Writer {
	return textutil.SymbolsFromEnv().Writer(cmd.ErrOrStderr())
}

func stdOut() io.Writer {
	return textutil.SymbolsFromEnv().Writer(os.Stdout)
}

func stdErr() io.Writer {
	return textutil.SymbolsFromEnv().Writer(os.Stderr)
}
