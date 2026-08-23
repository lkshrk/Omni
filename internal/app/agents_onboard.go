package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/securefile"
)

type AgentsOnboardOptions struct {
	PlanJSON    string
	ApplyPlan   string
	Sources     []string
	ProjectRoot string
}
type AgentsOnboardResolutions struct {
	ApprovedTargets     map[string][]string
	EnvBindings         map[string]map[string]string
	ApprovedExecutables map[string][]string
	Excluded            map[string]bool
}
type AgentsOnboardResult struct {
	Envelope       apm.ImportEnvelope `json:"apm"`
	CandidateSetID string             `json:"candidate_set_id"`
	PreimageSet    string             `json:"preimage_set"`
}
type AgentsOnboardStatusResult struct {
	OperationID string             `json:"operation_id"`
	OmniPhase   string             `json:"omni_phase"`
	APM         apm.ImportEnvelope `json:"apm"`
}

type OnboardingRecoveryError struct {
	OperationID, OmniPhase, APMState string
	Cause                            error
}

func (e *OnboardingRecoveryError) Error() string {
	detail := e.APMState
	if detail == "" {
		detail = "unavailable"
	}
	return fmt.Sprintf("onboarding operation %s is incomplete (Omni=%s, APM=%s); run 'omni agents onboard resume --operation %s'", e.OperationID, e.OmniPhase, detail, e.OperationID)
}
func (e *OnboardingRecoveryError) Unwrap() error { return e.Cause }

func (a *App) detectOnboardingRecovery(ctx context.Context) error {
	rootPath := filepath.Join(a.StateDir, "onboarding")
	entries, err := os.ReadDir(rootPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect onboarding recovery state: %w", err)
	}
	root, err := securefile.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open onboarding recovery state: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		opRoot, openErr := root.OpenChild(entry.Name())
		if openErr != nil {
			return fmt.Errorf("open onboarding operation %q: %w", entry.Name(), openErr)
		}
		journal, readErr := readOnboardJournal(opRoot)
		if readErr != nil {
			return fmt.Errorf("read onboarding operation %q: %w", entry.Name(), readErr)
		}
		recovery := &OnboardingRecoveryError{OperationID: journal.OperationID, OmniPhase: journal.Phase}
		client, clientErr := a.onboardingClient(ctx, journal.ProjectRoot)
		if clientErr != nil {
			recovery.Cause = clientErr
			return recovery
		}
		status, _, statusErr := client.ImportStatusAt(ctx, journal.OperationID, journal.ProjectRoot)
		if statusErr == nil && journal.Phase == "complete" && (status.State == "complete" || status.State == "rolled-back") {
			continue
		}
		recovery.APMState, recovery.Cause = status.State, statusErr
		return recovery
	}
	return nil
}

func (a *App) onboardingClient(ctx context.Context, projectRoot string) (*apm.Client, error) {
	if !a.APMAvailable() {
		return nil, errAPMNotInstalled()
	}
	if err := a.requirePinnedAPM(ctx); err != nil {
		return nil, err
	}
	return a.APMClient(apm.Global).AtProjectRoot(projectRoot), nil
}

func (a *App) AgentsOnboardPlan(ctx context.Context, opts AgentsOnboardOptions) (result AgentsOnboardResult, retErr error) {
	projectRoot, err := canonicalOnboardProjectRoot(opts.ProjectRoot)
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	inventory := LegacyInventory{PreimageSet: projectPreimageSet(projectRoot), Pointers: map[string]string{}}
	if projectRoot == "" {
		inventory, err = ExtractLegacyCandidates(a.ConfigPath)
		if err != nil {
			return AgentsOnboardResult{}, err
		}
	}
	temp, err := os.MkdirTemp("", "omni-onboard-plan-*")
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	defer func() {
		if err := os.RemoveAll(temp); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("remove onboarding plan staging: %w", err))
		}
	}()
	root, err := securefile.NewRoot(temp)
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	candidatePath := filepath.Join(root.Path(), "candidates.json")
	if projectRoot == "" {
		data, marshalErr := json.MarshalIndent(inventory.Envelope, "", " ")
		if marshalErr != nil {
			return AgentsOnboardResult{}, fmt.Errorf("encode onboarding candidates: %w", marshalErr)
		}
		if err := root.WriteFileAtomic("candidates.json", append(data, '\n')); err != nil {
			return AgentsOnboardResult{}, err
		}
	}
	client, err := a.onboardingClient(ctx, projectRoot)
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	sources := opts.Sources
	persist := opts.PlanJSON
	if persist != "" {
		persist, err = filepath.Abs(persist)
		if err != nil {
			return AgentsOnboardResult{}, err
		}
	}
	envelope, _, err := client.ImportPlan(ctx, apm.ImportRequest{CandidateFile: candidatePath, PreimageSet: inventory.PreimageSet, Sources: sources, PersistPlan: persist, ProjectRoot: projectRoot})
	candidateSetID := inventory.Envelope.CandidateSetID
	if envelope.Plan != nil {
		candidateSetID = envelope.Plan.CandidateSetID
	}
	return AgentsOnboardResult{Envelope: envelope, CandidateSetID: candidateSetID, PreimageSet: inventory.PreimageSet}, err
}

func (a *App) AgentsOnboardApply(ctx context.Context, planPath string) (result AgentsOnboardResult, retErr error) {
	planPath, err := filepath.Abs(planPath)
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	plan, err := readReviewedPlan(planPath)
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	projectRoot := plan.ProjectRoot
	inventory := LegacyInventory{PreimageSet: projectPreimageSet(projectRoot), Pointers: map[string]string{}}
	if projectRoot == "" {
		inventory, err = ExtractLegacyCandidates(a.ConfigPath)
		if err != nil {
			return AgentsOnboardResult{}, err
		}
		if err := validateDispositionGate(plan, inventory); err != nil {
			return AgentsOnboardResult{}, err
		}
	}
	lock, err := config.AcquireWriteLock(a.ConfigPath)
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	defer func() {
		if err := lock.Close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("release config lock: %w", err))
		}
	}()
	documents, err := captureJournalDocuments(inventory.Documents)
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	validationTemp, err := os.MkdirTemp("", "omni-onboard-validate-*")
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	defer func() {
		if err := os.RemoveAll(validationTemp); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("remove onboarding validation staging: %w", err))
		}
	}()
	validationRoot, err := securefile.NewRoot(validationTemp)
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	if projectRoot == "" {
		candidateData, marshalErr := json.MarshalIndent(inventory.Envelope, "", " ")
		if marshalErr != nil {
			return AgentsOnboardResult{}, fmt.Errorf("encode onboarding candidates: %w", marshalErr)
		}
		if err := validationRoot.WriteFileAtomic("candidates.json", append(candidateData, '\n')); err != nil {
			return AgentsOnboardResult{}, err
		}
	}
	client, err := a.onboardingClient(ctx, projectRoot)
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	fresh, _, err := client.ImportPlan(ctx, apm.ImportRequest{CandidateFile: filepath.Join(validationRoot.Path(), "candidates.json"), PreimageSet: inventory.PreimageSet, Sources: nativeOnboardSources(plan.Sources), ProjectRoot: projectRoot})
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	if fresh.Plan == nil || fresh.Plan.CandidateSetID != plan.CandidateSetID || fresh.Plan.InventoryFingerprint != plan.InventoryFingerprint || fresh.Plan.PlanID != plan.PlanID {
		return AgentsOnboardResult{}, errors.New("reviewed plan is stale: current legacy/native inventory changed")
	}
	stateRoot, err := onboardingRoot(a.StateDir)
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	opRoot, err := stateRoot.Child(plan.OperationID)
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	randomToken := make([]byte, 32)
	if _, err := rand.Read(randomToken); err != nil {
		return AgentsOnboardResult{}, err
	}
	token := []byte(base64.RawURLEncoding.EncodeToString(randomToken))
	journal := onboardJournal{SchemaVersion: 1, OperationID: plan.OperationID, PlanID: plan.PlanID, ResolutionID: plan.ResolutionID, CandidateSetID: plan.CandidateSetID, PreimageSet: inventory.PreimageSet, Scope: plan.Scope, ProjectRoot: projectRoot, Phase: "planned", FinalizeToken: string(token), Documents: documents}
	if err := writeOnboardJournal(opRoot, journal); err != nil {
		return AgentsOnboardResult{}, err
	}
	if err := opRoot.CopyIn(filepath.Join(validationRoot.Path(), "candidates.json"), "candidates.json"); err != nil {
		return AgentsOnboardResult{}, err
	}
	if err := opRoot.CopyIn(planPath, "plan.json"); err != nil {
		return AgentsOnboardResult{}, err
	}
	journal.Phase = "apm-applying"
	if err := writeOnboardJournal(opRoot, journal); err != nil {
		return AgentsOnboardResult{}, err
	}
	envelope, _, err := client.ImportApply(ctx, apm.ImportRequest{CandidateFile: filepath.Join(opRoot.Path(), "candidates.json"), PlanFile: filepath.Join(opRoot.Path(), "plan.json"), PreimageSet: inventory.PreimageSet, FinalizeToken: token, ProjectRoot: projectRoot})
	if err != nil {
		return AgentsOnboardResult{Envelope: envelope, CandidateSetID: plan.CandidateSetID, PreimageSet: inventory.PreimageSet}, err
	}
	if envelope.OperationID != plan.OperationID {
		return AgentsOnboardResult{}, errors.New("APM apply operation identity mismatch")
	}
	journal.Phase = "apm-applied"
	if err := writeOnboardJournal(opRoot, journal); err != nil {
		return AgentsOnboardResult{}, err
	}
	if err := commitLegacyFragments(documents, a.ConfigPath); err != nil {
		return AgentsOnboardResult{}, err
	}
	journal.Phase = "v24-committed"
	if err := writeOnboardJournal(opRoot, journal); err != nil {
		return AgentsOnboardResult{}, err
	}
	final, _, err := client.ImportFinalize(ctx, apm.ImportRequest{OperationID: plan.OperationID, PreimageSet: inventory.PreimageSet, FinalizeToken: token, ProjectRoot: projectRoot})
	if err != nil {
		return AgentsOnboardResult{Envelope: final, CandidateSetID: plan.CandidateSetID, PreimageSet: inventory.PreimageSet}, err
	}
	if final.OperationID != plan.OperationID {
		return AgentsOnboardResult{}, errors.New("APM finalize operation identity mismatch")
	}
	if final.State != "complete" {
		return AgentsOnboardResult{}, errors.New("APM finalize did not complete")
	}
	journal.Phase = "complete"
	if err := writeOnboardJournal(opRoot, journal); err != nil {
		return AgentsOnboardResult{}, err
	}
	return AgentsOnboardResult{Envelope: final, CandidateSetID: plan.CandidateSetID, PreimageSet: inventory.PreimageSet}, nil
}

func (a *App) AgentsOnboardApplyReviewed(ctx context.Context, plan apm.ImportPlan) (result AgentsOnboardResult, retErr error) {
	if err := apm.BindImportPlanResolution(&plan); err != nil {
		return AgentsOnboardResult{}, err
	}
	temp, err := os.MkdirTemp("", "omni-onboard-reviewed-*")
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	defer func() {
		if err := os.RemoveAll(temp); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("remove reviewed-plan staging: %w", err))
		}
	}()
	root, err := securefile.NewRoot(temp)
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	if err := root.WriteFileAtomic("plan.json", append(data, '\n')); err != nil {
		return AgentsOnboardResult{}, err
	}
	return a.AgentsOnboardApply(ctx, filepath.Join(root.Path(), "plan.json"))
}

func (a *App) AgentsOnboardApplyResolved(ctx context.Context, planPath string, resolutions AgentsOnboardResolutions) (AgentsOnboardResult, error) {
	plan, err := readReviewedPlan(planPath)
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	if err := validateApprovedTargetResolutions(plan, resolutions.ApprovedTargets); err != nil {
		return AgentsOnboardResult{}, err
	}
	for i := range plan.Items {
		item := &plan.Items[i]
		if targets := lookupResolution(resolutions.ApprovedTargets, item.ID, item.Name); len(targets) > 0 {
			item.Resolution.Decision = "import"
			item.Resolution.ApprovedTargets = sortedUnique(targets)
		}
		if bindings := lookupResolution(resolutions.EnvBindings, item.ID, item.Name); len(bindings) > 0 {
			item.Resolution.Decision = "map-secret"
			item.Resolution.EnvBindings = bindings
		}
		if paths := lookupResolution(resolutions.ApprovedExecutables, item.ID, item.Name); len(paths) > 0 {
			item.Resolution.Decision = "import"
			item.Resolution.ApprovedExecutables = sortedUnique(paths)
		}
		if resolutions.Excluded[item.ID] || resolutions.Excluded[item.Name] {
			item.Resolution.Decision = "exclude"
		}
	}
	if err := apm.BindImportPlanResolution(&plan); err != nil {
		return AgentsOnboardResult{}, err
	}
	return a.AgentsOnboardApplyReviewed(ctx, plan)
}

func validateApprovedTargetResolutions(plan apm.ImportPlan, resolutions map[string][]string) error {
	for key, targets := range resolutions {
		if len(targets) == 0 {
			return fmt.Errorf("target resolution item %s has no targets", key)
		}
		matched := false
		for _, item := range plan.Items {
			if key != item.ID && key != item.Name {
				continue
			}
			matched = true
			allowed := item.TargetOptions()
			for _, target := range targets {
				if !apm.ValidImportName(target) || !slices.Contains(allowed, target) {
					return fmt.Errorf("item %s does not allow target %s", item.Name, target)
				}
			}
		}
		if !matched {
			return fmt.Errorf("target resolution item %s is not in the reviewed plan", key)
		}
	}
	return nil
}

func sortedUnique(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return slices.Compact(out)
}
func lookupResolution[T any](values map[string]T, id, name string) T {
	if value, ok := values[id]; ok {
		return value
	}
	return values[name]
}

func (a *App) AgentsOnboardStatus(ctx context.Context, operationID string) (AgentsOnboardStatusResult, error) {
	if strings.TrimSpace(operationID) == "" {
		return AgentsOnboardStatusResult{}, errors.New("operation ID is required")
	}
	stateRoot, err := securefile.OpenRoot(filepath.Join(a.StateDir, "onboarding"))
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	opRoot, err := stateRoot.OpenChild(operationID)
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	journal, err := readOnboardJournal(opRoot)
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	client, err := a.onboardingClient(ctx, journal.ProjectRoot)
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	envelope, _, err := client.ImportStatusAt(ctx, operationID, journal.ProjectRoot)
	return AgentsOnboardStatusResult{OperationID: operationID, OmniPhase: journal.Phase, APM: envelope}, err
}

func (a *App) AgentsOnboardResume(ctx context.Context, operationID string) (result AgentsOnboardStatusResult, retErr error) {
	lock, err := config.AcquireWriteLock(a.ConfigPath)
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	defer func() {
		if err := lock.Close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("release config lock: %w", err))
		}
	}()
	stateRoot, err := securefile.OpenRoot(filepath.Join(a.StateDir, "onboarding"))
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	opRoot, err := stateRoot.OpenChild(operationID)
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	journal, err := readOnboardJournal(opRoot)
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	token := []byte(journal.FinalizeToken)
	plan, err := readReviewedPlan(filepath.Join(opRoot.Path(), "plan.json"))
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	if plan.OperationID != journal.OperationID || plan.PlanID != journal.PlanID || plan.ResolutionID != journal.ResolutionID || plan.CandidateSetID != journal.CandidateSetID {
		return AgentsOnboardStatusResult{}, errors.New("onboarding journal/plan identity mismatch")
	}
	client, err := a.onboardingClient(ctx, journal.ProjectRoot)
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	if journal.Phase == "planned" || journal.Phase == "apm-applying" {
		envelope, _, resumeErr := client.ImportResume(ctx, apm.ImportRequest{OperationID: operationID, CandidateFile: filepath.Join(opRoot.Path(), "candidates.json"), PlanFile: filepath.Join(opRoot.Path(), "plan.json"), PreimageSet: journal.PreimageSet, FinalizeToken: token, ProjectRoot: journal.ProjectRoot})
		if resumeErr != nil {
			return AgentsOnboardStatusResult{OperationID: operationID, OmniPhase: journal.Phase, APM: envelope}, resumeErr
		}
		if envelope.OperationID != operationID {
			return AgentsOnboardStatusResult{}, errors.New("APM resume operation identity mismatch")
		}
		if envelope.State != "awaiting-external-commit" {
			return AgentsOnboardStatusResult{}, fmt.Errorf("APM resume returned state %q", envelope.State)
		}
		journal.Phase = "apm-applied"
		if err := writeOnboardJournal(opRoot, journal); err != nil {
			return AgentsOnboardStatusResult{}, err
		}
	}
	if journal.Phase == "apm-applied" {
		if err := commitLegacyFragments(journal.Documents, a.ConfigPath); err != nil {
			return AgentsOnboardStatusResult{}, err
		}
		journal.Phase = "v24-committed"
		if err := writeOnboardJournal(opRoot, journal); err != nil {
			return AgentsOnboardStatusResult{}, err
		}
	}
	if journal.Phase == "v24-committed" {
		envelope, _, err := client.ImportFinalize(ctx, apm.ImportRequest{OperationID: operationID, PreimageSet: journal.PreimageSet, FinalizeToken: token, ProjectRoot: journal.ProjectRoot})
		if err != nil {
			return AgentsOnboardStatusResult{}, err
		}
		journal.Phase = "complete"
		if err := writeOnboardJournal(opRoot, journal); err != nil {
			return AgentsOnboardStatusResult{}, err
		}
		return AgentsOnboardStatusResult{OperationID: operationID, OmniPhase: journal.Phase, APM: envelope}, nil
	}
	return a.AgentsOnboardStatus(ctx, operationID)
}

type AgentsOnboardCleanupPreview struct {
	OperationID  string   `json:"operation_id"`
	Paths        []string `json:"paths"`
	Count        int      `json:"count"`
	AlreadyClean bool     `json:"already_clean"`
}

func (a *App) AgentsOnboardCleanup(ctx context.Context, operationID string, confirm bool) (AgentsOnboardCleanupPreview, error) {
	preview := AgentsOnboardCleanupPreview{OperationID: operationID, Paths: []string{filepath.Join(a.StateDir, "onboarding", operationID), filepath.Join("~/.apm/import-journal", operationID)}, Count: 2}
	tombstones, tombErr := securefile.NewRoot(filepath.Join(a.StateDir, "onboarding-cleanup"))
	if tombErr != nil {
		return preview, tombErr
	}
	tombstone := operationID + ".json"
	if err := tombstones.Verify(tombstone); err == nil {
		if root, openErr := securefile.OpenRoot(filepath.Join(a.StateDir, "onboarding")); openErr == nil {
			if removeErr := root.Remove(operationID); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return preview, removeErr
			}
		}
		preview.AlreadyClean = true
		return preview, nil
	}
	stateRoot, err := securefile.OpenRoot(filepath.Join(a.StateDir, "onboarding"))
	if err != nil {
		return preview, err
	}
	opRoot, err := stateRoot.OpenChild(operationID)
	if err != nil {
		return preview, err
	}
	journal, err := readOnboardJournal(opRoot)
	if err != nil {
		return preview, err
	}
	if journal.Phase != "complete" {
		return preview, errors.New("onboarding cleanup requires a complete operation")
	}
	client, err := a.onboardingClient(ctx, journal.ProjectRoot)
	if err != nil {
		return preview, err
	}
	status, _, err := client.ImportStatusAt(ctx, operationID, journal.ProjectRoot)
	if err != nil {
		return preview, err
	}
	if status.State != "complete" && status.State != "rolled-back" {
		return preview, errors.New("APM cleanup requires a terminal operation")
	}
	if !confirm {
		return preview, nil
	}
	if _, _, err := client.ImportCleanupAt(ctx, operationID, journal.ProjectRoot); err != nil {
		return preview, err
	}
	data, marshalErr := json.Marshal(preview)
	if marshalErr != nil {
		return preview, marshalErr
	}
	if err := tombstones.WriteFileAtomic(tombstone, data); err != nil {
		return preview, err
	}
	if err := stateRoot.Remove(operationID); err != nil {
		return preview, err
	}
	return preview, nil
}

func readReviewedPlan(path string) (apm.ImportPlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return apm.ImportPlan{}, err
	}
	var envelope apm.ImportEnvelope
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return apm.ImportPlan{}, err
	}
	if _, wrapped := probe["plan"]; wrapped {
		if err := decodeOnboardJSON(data, &envelope); err != nil || envelope.Plan == nil {
			return apm.ImportPlan{}, errors.New("reviewed plan envelope mismatch")
		}
		if err := apm.ValidateImportPlanProtocol(*envelope.Plan); err != nil {
			return apm.ImportPlan{}, err
		}
		if err := apm.ValidateImportPlanBinding(*envelope.Plan); err != nil {
			return apm.ImportPlan{}, err
		}
		return *envelope.Plan, nil
	}
	var plan apm.ImportPlan
	if err := decodeOnboardJSON(data, &plan); err != nil {
		return plan, err
	}
	if err := apm.ValidateImportPlanProtocol(plan); err != nil {
		return plan, err
	}
	if err := apm.ValidateImportPlanBinding(plan); err != nil {
		return plan, err
	}
	return plan, nil
}

func decodeOnboardJSON(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func canonicalOnboardProjectRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve onboarding project root: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errors.New("onboarding project root must be a directory")
	}
	return filepath.Clean(resolved), nil
}

func validateOnboardScope(scope, projectRoot string) error {
	if scope == "" || scope == "global" {
		if projectRoot != "" {
			return errors.New("global onboarding journal cannot declare a project root")
		}
		return nil
	}
	if scope != "project" {
		return fmt.Errorf("unsupported onboarding scope %q", scope)
	}
	canonical, err := canonicalOnboardProjectRoot(projectRoot)
	if err != nil || canonical != projectRoot {
		return errors.New("project onboarding journal has a non-canonical project root")
	}
	return nil
}

func projectPreimageSet(projectRoot string) string {
	sum := sha256.Sum256([]byte("omni-v24-project\x00" + projectRoot))
	return fmt.Sprintf("%x", sum[:])
}

func nativeOnboardSources(sources []string) []string {
	out := make([]string, 0, len(sources))
	for _, source := range sources {
		if source != "omni-v24" {
			out = append(out, source)
		}
	}
	return out
}

func (a *App) AgentsOnboardRollback(ctx context.Context, operationID string) (AgentsOnboardStatusResult, error) {
	stateRoot, err := securefile.OpenRoot(filepath.Join(a.StateDir, "onboarding"))
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	opRoot, err := stateRoot.OpenChild(operationID)
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	journal, err := readOnboardJournal(opRoot)
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	if journal.Phase == "apm-applied" || journal.Phase == "v24-committed" || journal.Phase == "complete" {
		return AgentsOnboardStatusResult{}, errors.New("onboarding rollback is unavailable after APM installation")
	}
	client, err := a.onboardingClient(ctx, journal.ProjectRoot)
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	envelope, _, err := client.ImportRollback(ctx, operationID, journal.ProjectRoot)
	if err != nil {
		return AgentsOnboardStatusResult{OperationID: operationID, OmniPhase: journal.Phase, APM: envelope}, err
	}
	journal.Phase = "complete"
	if err := writeOnboardJournal(opRoot, journal); err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	return AgentsOnboardStatusResult{OperationID: operationID, OmniPhase: journal.Phase, APM: envelope}, nil
}

func validateDispositionGate(plan apm.ImportPlan, inventory LegacyInventory) error {
	seen := map[string]bool{}
	resolutions := map[string]apm.ImportResolution{}
	legacyIDs := map[string]bool{}
	for _, candidate := range inventory.Envelope.Candidates {
		legacyIDs[candidate.ID] = true
	}
	for _, item := range plan.Items {
		isLegacy := false
		for _, id := range item.CandidateIDs {
			if legacyIDs[id] {
				isLegacy = true
				break
			}
		}
		if !isLegacy {
			continue
		}
		exclusionAuthorized := item.Resolution.Decision == "exclude" || slices.Contains(item.ReasonCodes, "legacy-negative-state") || slices.Contains(item.ReasonCodes, "durable-exclusion")
		conditional := slices.Contains(item.ReasonCodes, "conditional-group-host")
		needsTargets := slices.Contains(item.ReasonCodes, "legacy-unscoped-targets")
		choiceResolved := item.Classification == "needs-choice" && item.Resolution.Decision == "import" && (!needsTargets || len(item.Resolution.ApprovedTargets) > 0)
		conditionalDropped := item.Classification == "needs-choice" && conditional && item.Resolution.Decision == "exclude"
		secretResolved := item.Classification == "secret-blocked" && item.Resolution.Decision == "map-secret" && len(item.Resolution.EnvBindings) > 0
		conflictResolved := item.Classification == "conflict" && item.Resolution.Decision == "select-origin" && item.Resolution.SelectedOriginID != ""
		allowed := item.Classification == "already-managed" || item.Classification == "duplicate" || item.Classification == "importable" || item.Classification == "local-package" || choiceResolved || conditionalDropped || secretResolved || conflictResolved || ((item.Classification == "excluded" || item.Classification == "excluded-changed") && exclusionAuthorized)
		if !allowed {
			return fmt.Errorf("legacy item %s remains %s", item.Name, item.Classification)
		}
		effectiveTargets := item.ProposedTargets
		if len(item.Resolution.ApprovedTargets) > 0 {
			effectiveTargets = item.Resolution.ApprovedTargets
		}
		if len(effectiveTargets) == 0 {
			return fmt.Errorf("legacy item %s has no effective targets", item.Name)
		}
		for _, target := range effectiveTargets {
			if len(item.ProposedTargets) > 0 && !slices.Contains(item.ProposedTargets, target) {
				return fmt.Errorf("legacy item %s broadens to unreviewed target %s", item.Name, target)
			}
		}
		for pointer, env := range item.Resolution.EnvBindings {
			if !strings.HasPrefix(pointer, "/") || !validOnboardEnvName(env) {
				return fmt.Errorf("legacy item %s has invalid environment binding", item.Name)
			}
		}
		requestedExec := []string{}
		for _, reason := range item.ReasonCodes {
			if strings.HasPrefix(reason, "executable:") {
				requestedExec = append(requestedExec, strings.TrimPrefix(reason, "executable:"))
			}
		}
		if len(requestedExec) > 0 {
			sort.Strings(requestedExec)
			approved := sortedUnique(item.Resolution.ApprovedExecutables)
			if !slices.Equal(requestedExec, approved) {
				return fmt.Errorf("legacy item %s requires exact executable approvals", item.Name)
			}
		}
		for _, id := range item.CandidateIDs {
			seen[id] = true
			resolutions[id] = item.Resolution
		}
	}
	for _, candidate := range inventory.Envelope.Candidates {
		if !seen[candidate.ID] {
			return fmt.Errorf("legacy candidate %s has no reviewed disposition", candidate.Name)
		}
		var payload map[string]any
		resolution := resolutions[candidate.ID]
		if json.Unmarshal(candidate.Payload, &payload) == nil && payload["target_resolution_required"] == true && payload["disposition"] != "excluded" && resolution.Decision != "exclude" && !(payload["unsupported_reason"] == "conditional-group-host" && resolution.Decision == "exclude") && len(resolution.ApprovedTargets) == 0 {
			return fmt.Errorf("legacy candidate %s has an unscoped target set and needs an item-specific target resolution", candidate.Name)
		}
	}
	return nil
}

func validOnboardEnvName(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
