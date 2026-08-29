package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// HasRemovedAgentConfig detects whether onboarding must run before normal
// config repair. It deliberately decodes no removed DTOs.
func HasRemovedAgentConfig(path string) (bool, error) {
	seen := map[string]bool{}
	var visit func(string) (bool, error)
	visit = func(current string) (bool, error) {
		abs, err := filepath.Abs(current)
		if err != nil {
			return false, err
		}
		if seen[abs] {
			return false, nil
		}
		seen[abs] = true
		data, err := os.ReadFile(abs)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return false, fmt.Errorf("parse config for onboarding detection: %w", err)
		}
		if value := bytes.TrimSpace(raw["agents"]); len(value) > 0 && !bytes.Equal(value, []byte("null")) && !bytes.Equal(value, []byte("{}")) {
			return true, nil
		}
		delete(raw, "agents")
		if err := validateRemovedAgentConfigFields(raw); err != nil {
			return true, nil
		}
		var includes []string
		if value := raw["$include"]; len(value) > 0 {
			if err := json.Unmarshal(value, &includes); err != nil {
				return false, err
			}
		}
		for _, include := range includes {
			if !filepath.IsAbs(include) {
				include = filepath.Join(filepath.Dir(abs), include)
			}
			found, err := visit(include)
			if err != nil || found {
				return found, err
			}
		}
		return false, nil
	}
	return visit(path)
}
