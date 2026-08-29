package testflow

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const (
	evidenceSchemaVersion = 1
	evidenceEventsFile    = "go-test.jsonl"
)

// EvidenceReport summarizes catalog requirements proven by collected CI artifacts.
type EvidenceReport struct {
	Verified int
	Gaps     []EvidenceGap
}

type EvidenceGap struct {
	FlowID      string
	Level       Level
	Reason      string
	TargetStage string
}

type evidenceMeta struct {
	SchemaVersion int      `json:"schema_version"`
	Lane          string   `json:"lane"`
	GOOS          string   `json:"goos"`
	Tags          []string `json:"tags"`
	Count         int      `json:"count"`
}

type gateReceipt struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Lane          string `json:"lane"`
	GOOS          string `json:"goos"`
	ImageRef      string `json:"image_ref"`
	ImageID       string `json:"image_id"`
	BinarySHA256  string `json:"binary_sha256"`
	CommandID     string `json:"command_id"`
	ExitCode      int    `json:"exit_code"`
	Status        string `json:"status"`
	Events        string `json:"events"`
}

type goTestEvent struct {
	Time        string  `json:"Time"`
	Action      string  `json:"Action"`
	Package     string  `json:"Package"`
	Test        string  `json:"Test"`
	Elapsed     float64 `json:"Elapsed"`
	Output      string  `json:"Output"`
	FailedBuild string  `json:"FailedBuild"`
}

type laneEvidence struct {
	meta   evidenceMeta
	events []goTestEvent
}

// VerifyEvidence proves required catalog evidence from already-collected lane artifacts.
// It never executes tests.
func VerifyEvidence(catalog Catalog, evidenceDir string) (EvidenceReport, error) {
	lanes, err := loadLaneEvidence(evidenceDir)
	if err != nil {
		return EvidenceReport{}, err
	}
	report := EvidenceReport{}
	var problems []string
	for _, flow := range catalog.Flows {
		for _, requirement := range flow.Requirements {
			if requirement.Status == StatusGap {
				report.Gaps = append(report.Gaps, EvidenceGap{FlowID: flow.ID, Level: requirement.Level, Reason: requirement.Reason, TargetStage: requirement.TargetStage})
				continue
			}
			if requirement.Status != StatusRequired {
				problems = append(problems, fmt.Sprintf("flow %q %s: unknown requirement status %q", flow.ID, requirement.Level, requirement.Status))
				continue
			}
			if len(requirement.Evidence) == 0 {
				problems = append(problems, fmt.Sprintf("flow %q %s: required evidence has no references", flow.ID, requirement.Level))
				continue
			}
			for _, claimed := range requirement.Evidence {
				if claimed.Type != requirement.Level {
					problems = append(problems, fmt.Sprintf("flow %q %s: evidence type %q does not match", flow.ID, requirement.Level, claimed.Type))
					continue
				}
				if claimed.Role != EvidencePrimary && claimed.Role != EvidenceRegression && claimed.Role != EvidenceSupplemental {
					problems = append(problems, fmt.Sprintf("flow %q %s: unknown evidence role %q", flow.ID, requirement.Level, claimed.Role))
					continue
				}
				if claimed.Role == EvidenceRegression && strings.TrimSpace(claimed.Reference) == "" {
					problems = append(problems, fmt.Sprintf("flow %q %s: regression evidence has no reference", flow.ID, requirement.Level))
					continue
				}
				lane, ok := lanes[claimed.Selector.Lane]
				if !ok {
					problems = append(problems, fmt.Sprintf("flow %q %s: missing evidence lane %q", flow.ID, requirement.Level, claimed.Selector.Lane))
					continue
				}
				if err := verifyClaim(lane, claimed.Selector); err != nil {
					problems = append(problems, fmt.Sprintf("flow %q %s: %v", flow.ID, requirement.Level, err))
					continue
				}
				report.Verified++
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return report, errors.New(strings.Join(problems, "\n"))
	}
	return report, nil
}

func loadLaneEvidence(root string) (map[string]laneEvidence, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read evidence directory: %w", err)
	}
	lanes := make(map[string]laneEvidence, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			return nil, fmt.Errorf("evidence entry %q must be a real directory", entry.Name())
		}
		dir := filepath.Join(root, entry.Name())
		var meta evidenceMeta
		if err := decodeStrictFile(filepath.Join(dir, "meta.json"), &meta); err != nil {
			return nil, fmt.Errorf("lane %q metadata: %w", entry.Name(), err)
		}
		if meta.SchemaVersion != evidenceSchemaVersion || meta.Lane == "" || meta.GOOS == "" || meta.Tags == nil || meta.Count != 1 {
			return nil, fmt.Errorf("lane %q has invalid metadata (schema_version=%d lane=%q goos=%q tags=%v count=%d)", entry.Name(), meta.SchemaVersion, meta.Lane, meta.GOOS, meta.Tags, meta.Count)
		}
		if meta.Lane != entry.Name() {
			return nil, fmt.Errorf("lane directory %q disagrees with metadata lane %q", entry.Name(), meta.Lane)
		}
		if !validLane(meta.Lane) || !validOS(meta.GOOS) {
			return nil, fmt.Errorf("lane %q has unknown lane or goos", meta.Lane)
		}
		seenTags := map[string]bool{}
		for _, tag := range meta.Tags {
			if !validTag(tag) || seenTags[tag] {
				return nil, fmt.Errorf("lane %q has unknown or duplicate tag %q", meta.Lane, tag)
			}
			seenTags[tag] = true
		}
		if _, exists := lanes[meta.Lane]; exists {
			return nil, fmt.Errorf("ambiguous duplicate evidence for lane %q", meta.Lane)
		}
		eventsPath := filepath.Join(dir, evidenceEventsFile)
		gatePath := filepath.Join(dir, "gate.json")
		_, gateErr := os.Stat(gatePath)
		if strings.HasPrefix(meta.Lane, "docker-") || gateErr == nil {
			if err := verifyGate(gatePath, meta); err != nil {
				return nil, fmt.Errorf("lane %q gate: %w", meta.Lane, err)
			}
		} else if !errors.Is(gateErr, os.ErrNotExist) {
			return nil, fmt.Errorf("lane %q gate: %w", meta.Lane, gateErr)
		}
		events, err := loadGoTestEvents(eventsPath)
		if err != nil {
			return nil, fmt.Errorf("lane %q events: %w", meta.Lane, err)
		}
		lanes[meta.Lane] = laneEvidence{meta: meta, events: events}
	}
	return lanes, nil
}

func verifyGate(path string, meta evidenceMeta) error {
	var gate gateReceipt
	if err := decodeStrictFile(path, &gate); err != nil {
		return err
	}
	if gate.SchemaVersion != evidenceSchemaVersion || gate.Kind != "container_gate" {
		return fmt.Errorf("invalid schema_version or kind")
	}
	if gate.Lane != meta.Lane || gate.GOOS != meta.GOOS {
		return fmt.Errorf("lane/goos does not match metadata")
	}
	if gate.ImageRef == "" || gate.ImageID == "" || len(gate.BinarySHA256) != 64 || gate.CommandID == "" {
		return fmt.Errorf("container identity fields must be nonempty")
	}
	if gate.ExitCode != 0 || gate.Status != "pass" {
		return fmt.Errorf("container command did not pass (status=%q exit_code=%d)", gate.Status, gate.ExitCode)
	}
	if gate.Events != evidenceEventsFile {
		return fmt.Errorf("unknown events reference %q", gate.Events)
	}
	return nil
}

func loadGoTestEvents(path string) ([]goTestEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var events []goTestEvent
	for line := 1; scanner.Scan(); line++ {
		body := scanner.Bytes()
		if len(bytes.TrimSpace(body)) == 0 {
			return nil, fmt.Errorf("line %d is empty", line)
		}
		var event goTestEvent
		if err := decodeStrict(body, &event); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if event.Action == "" || event.Package == "" {
			return nil, fmt.Errorf("line %d lacks Action or Package", line)
		}
		if strings.Contains(event.Output, "(cached)") {
			return nil, fmt.Errorf("line %d contains cached test output", line)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, errors.New("event stream is empty")
	}
	return events, nil
}

func verifyClaim(lane laneEvidence, selector Selector) error {
	if lane.meta.GOOS == "" || !slices.Contains(selector.OS, lane.meta.GOOS) {
		return fmt.Errorf("selector %s.%s does not allow goos %q", selector.Package, selector.Test, lane.meta.GOOS)
	}
	if !sameStrings(lane.meta.Tags, selector.Tags) {
		return fmt.Errorf("selector %s.%s tags %v do not match lane tags %v", selector.Package, selector.Test, selector.Tags, lane.meta.Tags)
	}
	testTerminal := 0
	packageTerminal := 0
	for _, event := range lane.events {
		if event.Package != selector.Package {
			continue
		}
		if event.Test == selector.Test {
			switch event.Action {
			case "pass":
				testTerminal++
			case "fail", "skip":
				return fmt.Errorf("selector %s.%s ended with %s", selector.Package, selector.Test, event.Action)
			}
		}
		if event.Test == "" {
			switch event.Action {
			case "pass":
				packageTerminal++
			case "fail", "skip":
				return fmt.Errorf("package %s ended with %s", selector.Package, event.Action)
			}
		}
	}
	if testTerminal != 1 {
		return fmt.Errorf("selector %s.%s has %d terminal pass events, want exactly 1", selector.Package, selector.Test, testTerminal)
	}
	if packageTerminal != 1 {
		return fmt.Errorf("package %s has %d terminal pass events, want exactly 1", selector.Package, packageTerminal)
	}
	return nil
}

func sameStrings(left, right []string) bool {
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	return slices.Equal(left, right)
}

func decodeStrictFile(path string, target any) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return decodeStrict(body, target)
}

func decodeStrict(body []byte, target any) error {
	if err := rejectDuplicateKeys(body); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	var trailer any
	if err := dec.Decode(&trailer); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return errors.New("multiple JSON values")
	}
	return nil
}
