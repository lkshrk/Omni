package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/securefile"
)

type onboardJournal struct {
	SchemaVersion          int                  `json:"schema_version"`
	OperationID            string               `json:"operation_id"`
	PlanID                 string               `json:"plan_id"`
	ResolutionID           string               `json:"resolution_id"`
	CandidateSetID         string               `json:"candidate_set_id"`
	PreimageSet            string               `json:"preimage_set"`
	Phase                  string               `json:"phase"`
	Documents              []journalDocument    `json:"documents"`
	ManifestPath           string               `json:"manifest_path"`
	ManifestData           string               `json:"manifest_data,omitempty"`
	ManifestExisted        bool                 `json:"manifest_existed"`
	ManifestMode           uint32               `json:"manifest_mode,omitempty"`
	ManifestHash           string               `json:"manifest_hash,omitempty"`
	ProposedManifestHash   string               `json:"proposed_manifest_hash,omitempty"`
	MarketplaceData        string               `json:"marketplace_data,omitempty"`
	MarketplaceExisted     bool                 `json:"marketplace_existed"`
	MarketplaceHash        string               `json:"marketplace_hash,omitempty"`
	Targets                []string             `json:"targets,omitempty"`
	Marketplaces           []OnboardMarketplace `json:"marketplaces,omitempty"`
	Packages               []journalPackage     `json:"packages,omitempty"`
	MaterializedItems      []string             `json:"materialized_items,omitempty"`
	PendingMaterializeItem string               `json:"pending_materialize_item,omitempty"`
	UpdatedAt              time.Time            `json:"updated_at"`
}

type journalPackage struct {
	ItemID     string `json:"item_id"`
	StagedPath string `json:"staged_path"`
	Hash       string `json:"hash"`
}

type journalDocument struct {
	Path           string `json:"logical_path"`
	CanonicalPath  string `json:"canonical_path"`
	Kind           string `json:"kind"`
	Hash           string `json:"hash"`
	Mode           uint32 `json:"mode"`
	UID            uint32 `json:"uid,omitempty"`
	GID            uint32 `json:"gid,omitempty"`
	ACLFingerprint string `json:"acl_fingerprint,omitempty"`
	Data           string `json:"data"`
}

var onboardingFragmentFailpoint func(boundary, path string) error

func onboardingRoot(stateDir string) (*securefile.Root, error) {
	if strings.TrimSpace(stateDir) == "" || !filepath.IsAbs(stateDir) {
		return nil, errors.New("absolute Omni state directory is required")
	}
	root, err := securefile.NewRoot(filepath.Join(stateDir, "onboarding"))
	if err != nil {
		return nil, err
	}
	return root, nil
}

func writeOnboardJournal(root *securefile.Root, journal onboardJournal) error {
	journal.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := root.WriteFileAtomic("journal.json", data); err != nil {
		return err
	}
	return root.Verify("journal.json")
}

func readOnboardJournal(root *securefile.Root) (onboardJournal, error) {
	data, err := os.ReadFile(filepath.Join(root.Path(), "journal.json"))
	if err != nil {
		return onboardJournal{}, err
	}
	var journal onboardJournal
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil {
		return onboardJournal{}, err
	}
	if journal.SchemaVersion != 1 || !hexID(journal.OperationID, 32) || !hexID(journal.PlanID, 64) || !hexID(journal.ResolutionID, 64) || !hexID(journal.CandidateSetID, 64) {
		return onboardJournal{}, errors.New("invalid onboarding journal identity")
	}
	if journal.Phase == "" || !filepath.IsAbs(journal.ManifestPath) {
		return onboardJournal{}, errors.New("invalid onboarding journal state")
	}
	return journal, nil
}

func hexID(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

func captureJournalDocuments(paths []string) ([]journalDocument, error) {
	out := make([]journalDocument, 0, len(paths))
	for _, path := range paths {
		identity, err := captureFragmentIdentity(path)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, journalDocument{Path: path, CanonicalPath: identity.CanonicalPath, Kind: identity.Kind, Hash: byteHash(data), Mode: identity.Mode, UID: identity.UID, GID: identity.GID, ACLFingerprint: identity.ACLFingerprint, Data: base64.StdEncoding.EncodeToString(data)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func commitLegacyFragments(documents []journalDocument, rootConfig string) (retErr error) {
	staged := make(map[string]string, len(documents))
	stagedPreimages := make(map[string]string, len(documents))
	defer func() {
		for _, path := range staged {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				retErr = errors.Join(retErr, fmt.Errorf("remove staged config fragment: %w", err))
			}
		}
	}()
	for _, doc := range documents {
		if err := verifyFragmentIdentity(doc); err != nil {
			return err
		}
		current, err := os.ReadFile(doc.Path)
		if err != nil {
			return err
		}
		oldData, err := base64.StdEncoding.DecodeString(doc.Data)
		if err != nil {
			return err
		}
		var oldRaw, raw map[string]any
		if err := json.Unmarshal(oldData, &oldRaw); err != nil {
			return err
		}
		if err := json.Unmarshal(current, &raw); err != nil {
			return err
		}
		if !reflect.DeepEqual(legacyProjection(oldRaw), legacyProjection(raw)) {
			if legacyAlreadyRemoved(raw, samePath(doc.Path, rootConfig)) {
				continue
			}
			return fmt.Errorf("fragment-conflict: %s legacy nodes changed since onboarding plan", doc.Path)
		}
		delete(raw, "agents")
		removeLegacyAgentFields(raw)
		if samePath(doc.Path, rootConfig) {
			raw["version"] = config.CurrentVersion
			raw["$schema"] = config.SchemaURL
		}
		rendered, err := json.MarshalIndent(raw, "", "  ")
		if err != nil {
			return err
		}
		rendered = append(rendered, '\n')
		f, err := os.CreateTemp(filepath.Dir(doc.Path), ".onboard-v24-*")
		if err != nil {
			return err
		}
		name := f.Name()
		err = f.Chmod(os.FileMode(doc.Mode).Perm())
		if err == nil {
			_, err = f.Write(rendered)
		}
		if err == nil {
			err = f.Sync()
		}
		err = errors.Join(err, f.Close())
		if err != nil {
			removeErr := os.Remove(name)
			if errors.Is(removeErr, os.ErrNotExist) {
				removeErr = nil
			}
			return errors.Join(err, removeErr)
		}
		staged[doc.Path] = name
		stagedPreimages[doc.Path] = byteHash(current)
	}
	for _, doc := range documents {
		if staged[doc.Path] == "" {
			continue
		}
		if err := verifyFragmentIdentity(doc); err != nil {
			return err
		}
		current, err := os.ReadFile(doc.Path)
		if err != nil {
			return err
		}
		if byteHash(current) != stagedPreimages[doc.Path] {
			return fmt.Errorf("fragment-conflict: %s changed during commit", doc.Path)
		}
		if onboardingFragmentFailpoint != nil {
			if err := onboardingFragmentFailpoint("before-rename", doc.Path); err != nil {
				return err
			}
		}
		if err := os.Rename(staged[doc.Path], doc.Path); err != nil {
			return err
		}
		delete(staged, doc.Path)
		if onboardingFragmentFailpoint != nil {
			if err := onboardingFragmentFailpoint("after-rename", doc.Path); err != nil {
				return err
			}
		}
	}
	return nil
}

type fragmentIdentity struct {
	CanonicalPath, Kind string
	Mode, UID, GID      uint32
	ACLFingerprint      string
}

func verifyFragmentIdentity(doc journalDocument) error {
	current, err := captureFragmentIdentity(doc.Path)
	if err != nil {
		return fmt.Errorf("fragment-conflict: %s: %w", doc.Path, err)
	}
	if current.CanonicalPath != doc.CanonicalPath || current.Kind != doc.Kind || current.Mode != doc.Mode || current.UID != doc.UID || current.GID != doc.GID || current.ACLFingerprint != doc.ACLFingerprint {
		return fmt.Errorf("fragment-conflict: %s filesystem identity changed", doc.Path)
	}
	return nil
}

func legacyProjection(raw map[string]any) map[string]any {
	out := map[string]any{"agents": raw["agents"]}
	keys := []string{"agents_use", "agents_disabled", "skills_disabled", "mcp_disabled", "plugins_disabled"}
	if settings, ok := raw["settings"].(map[string]any); ok {
		values := map[string]any{}
		for _, key := range keys {
			if value, exists := settings[key]; exists {
				values[key] = value
			}
		}
		out["settings"] = values
	}
	if hosts, ok := raw["host_settings"].(map[string]any); ok {
		values := map[string]any{}
		for hostName, value := range hosts {
			if host, ok := value.(map[string]any); ok {
				selected := map[string]any{}
				for _, key := range keys {
					if item, exists := host[key]; exists {
						selected[key] = item
					}
				}
				if len(selected) > 0 {
					values[hostName] = selected
				}
			}
		}
		out["host_settings"] = values
	}
	if groups, ok := raw["groups"].([]any); ok {
		selected := make([]any, len(groups))
		for i, value := range groups {
			fields := map[string]any{}
			if group, ok := value.(map[string]any); ok {
				for _, key := range []string{"skills", "mcp_servers", "plugins", "marketplaces"} {
					if item, exists := group[key]; exists {
						fields[key] = item
					}
				}
			}
			selected[i] = fields
		}
		out["groups"] = selected
	}
	return out
}

func legacyAlreadyRemoved(raw map[string]any, root bool) bool {
	projection := legacyProjection(raw)
	if agents := projection["agents"]; agents != nil {
		return false
	}
	for key, value := range projection {
		if key == "agents" {
			continue
		}
		if values, ok := value.(map[string]any); ok && len(values) > 0 {
			return false
		}
		if values, ok := value.([]any); ok {
			for _, item := range values {
				if fields, ok := item.(map[string]any); ok && len(fields) > 0 {
					return false
				}
			}
		}
	}
	if !root {
		return true
	}
	version, _ := raw["version"].(float64)
	return int(version) == config.CurrentVersion
}

func removeLegacyAgentFields(raw map[string]any) {
	if settings, ok := raw["settings"].(map[string]any); ok {
		for _, key := range []string{"agents_use", "agents_disabled", "skills_disabled", "mcp_disabled", "plugins_disabled"} {
			delete(settings, key)
		}
	}
	if hosts, ok := raw["host_settings"].(map[string]any); ok {
		for _, value := range hosts {
			if host, ok := value.(map[string]any); ok {
				for _, key := range []string{"agents_use", "agents_disabled", "skills_disabled", "mcp_disabled", "plugins_disabled"} {
					delete(host, key)
				}
			}
		}
	}
	if groups, ok := raw["groups"].([]any); ok {
		for _, value := range groups {
			if group, ok := value.(map[string]any); ok {
				for _, key := range []string{"skills", "mcp_servers", "plugins", "marketplaces"} {
					delete(group, key)
				}
			}
		}
	}
}

func byteHash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func samePath(a, b string) bool   { aa, _ := filepath.Abs(a); bb, _ := filepath.Abs(b); return aa == bb }
