package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lkshrk/omni/internal/actions"
	"github.com/lkshrk/omni/internal/testflow"
)

func main() {
	write := flag.Bool("write", false, "update generated tables in docs/test-matrix.md")
	flag.Parse()
	if err := run(*write); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(write bool) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	catalog, err := testflow.Load(filepath.Join(root, "test", "flows.json"))
	if err != nil {
		return err
	}
	surfaces := actionSurfaces()
	if err := testflow.Validate(catalog, surfaces, root); err != nil {
		return err
	}
	path := filepath.Join(root, "docs", "test-matrix.md")
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	generated, err := testflow.RenderMatrix(current, catalog, surfaces)
	if err != nil {
		return err
	}
	return update(path, current, generated, write)
}

func actionSurfaces() []testflow.ActionSurface {
	all := actions.All()
	out := make([]testflow.ActionSurface, 0, len(all))
	for _, action := range all {
		commands := make([]testflow.CLICommandSurface, 0, len(action.CLI))
		for _, binding := range action.CLI {
			commands = append(commands, testflow.CLICommandSurface{Command: binding.Command, RequiredFlags: binding.RequiredFlags})
		}
		out = append(out, testflow.ActionSurface{
			ID:          string(action.ID),
			CLI:         len(action.CLI) > 0,
			CLICommands: commands,
			TUI:         action.TUI != nil || action.Palette != nil,
			Mutates:     action.Mutates,
		})
	}
	return out
}

func update(path string, current, generated []byte, write bool) error {
	if bytes.Equal(current, generated) {
		return nil
	}
	if !write {
		return fmt.Errorf("docs/test-matrix.md is stale; run go run ./scripts/flow-catalog -write")
	}
	return os.WriteFile(path, generated, 0o644)
}
