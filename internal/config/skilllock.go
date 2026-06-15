package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SkillLockEntry mirrors a single entry in the upstream skills CLI lockfile
// (vercel-labs/skills, schema v3). Omni reads this for import and drift checks
// and never writes it.
type SkillLockEntry struct {
	Source          string `json:"source"`
	SourceType      string `json:"sourceType"`
	SourceURL       string `json:"sourceUrl"`
	Ref             string `json:"ref,omitempty"`
	SkillPath       string `json:"skillPath,omitempty"`
	SkillFolderHash string `json:"skillFolderHash"`
	InstalledAt     string `json:"installedAt"`
	UpdatedAt       string `json:"updatedAt"`
	PluginName      string `json:"pluginName,omitempty"`
}

type SkillLockFile struct {
	Version            int                       `json:"version"`
	Skills             map[string]SkillLockEntry `json:"skills"`
	LastSelectedAgents []string                  `json:"lastSelectedAgents,omitempty"`
}

func ParseSkillLock(data []byte) (*SkillLockFile, error) {
	var lock SkillLockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parsing skill lock: %w", err)
	}
	if lock.Skills == nil {
		lock.Skills = map[string]SkillLockEntry{}
	}
	return &lock, nil
}

// SkillLockPath returns the global lockfile path: $XDG_STATE_HOME/skills/.skill-lock.json
// when set, else <home>/.agents/.skill-lock.json.
func SkillLockPath(home string) string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "skills", ".skill-lock.json")
	}
	return filepath.Join(home, ".agents", ".skill-lock.json")
}

// LoadSkillLock reads and parses the lockfile at path. A missing file yields an
// empty lockfile and no error.
func LoadSkillLock(path string) (*SkillLockFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &SkillLockFile{Version: 3, Skills: map[string]SkillLockEntry{}}, nil
		}
		return nil, fmt.Errorf("reading skill lock: %w", err)
	}
	return ParseSkillLock(data)
}
