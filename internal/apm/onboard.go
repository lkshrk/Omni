package apm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

const ImportSchemaVersion = 1

const (
	ImportKindPlan     = "import-plan"
	ImportKindApply    = "import-apply-result"
	ImportKindStatus   = "import-status-result"
	ImportKindResume   = "import-resume-result"
	ImportKindFinalize = "import-finalize-result"
	ImportKindCleanup  = "import-cleanup-result"
	ImportKindRollback = "import-rollback-result"
	ImportKindError    = "import-error"
)

type SourcePreimage struct {
	ID                 string `json:"id"`
	AbsolutePath       string `json:"absolute_path"`
	Kind               string `json:"kind"`
	Size               int64  `json:"size"`
	Mode               uint32 `json:"mode"`
	ContentFingerprint string `json:"content_fingerprint"`
}

type ImportCandidate struct {
	ID                 string          `json:"id"`
	Kind               string          `json:"kind"`
	Name               string          `json:"name"`
	RootID             string          `json:"root_id"`
	SourceHandle       string          `json:"source_handle"`
	SourceTarget       []string        `json:"source_target"`
	Provenance         string          `json:"provenance"`
	Payload            json.RawMessage `json:"payload"`
	ContentFingerprint string          `json:"content_fingerprint"`
	SourcePreimageIDs  []string        `json:"source_preimage_ids"`
	ExecutablePaths    []string        `json:"executable_paths"`
	SecretBlocked      bool            `json:"secret_blocked,omitempty"`
}

type CandidateEnvelope struct {
	SchemaVersion   int               `json:"schema_version"`
	Coordinator     string            `json:"coordinator"`
	Scope           string            `json:"scope,omitempty"`
	ProjectRoot     string            `json:"project_root,omitempty"`
	Sources         []string          `json:"sources"`
	CandidateSetID  string            `json:"candidate_set_id"`
	SourcePreimages []SourcePreimage  `json:"source_preimages"`
	Candidates      []ImportCandidate `json:"candidates"`
}

type ImportResolution struct {
	Decision            string            `json:"decision"`
	SelectedOriginID    string            `json:"selected_origin_id"`
	ApprovedTargets     []string          `json:"approved_targets"`
	EnvBindings         map[string]string `json:"env_bindings"`
	ApprovedExecutables []string          `json:"approved_executables"`
}

type ImportItem struct {
	ID                  string           `json:"id"`
	CandidateIDs        []string         `json:"candidate_ids"`
	Kind                string           `json:"kind"`
	Name                string           `json:"name"`
	Classification      string           `json:"classification"`
	ProposedAction      string           `json:"proposed_action"`
	CurrentTargets      []string         `json:"current_targets"`
	ProposedTargets     []string         `json:"proposed_targets"`
	ProposedDestination string           `json:"proposed_destination"`
	ReasonCodes         []string         `json:"reason_codes"`
	Resolution          ImportResolution `json:"resolution"`
}

func (i ImportItem) TargetOptions() []string {
	options := make([]string, 0, len(i.CurrentTargets)+len(i.ProposedTargets))
	for _, target := range append(append([]string(nil), i.CurrentTargets...), i.ProposedTargets...) {
		if ValidImportName(target) {
			options = append(options, target)
		}
	}
	sort.Strings(options)
	return slices.Compact(options)
}

type ImportPlan struct {
	SchemaVersion        int               `json:"schema_version"`
	Coordinator          string            `json:"coordinator"`
	PlanID               string            `json:"plan_id"`
	ResolutionID         string            `json:"resolution_id"`
	OperationID          string            `json:"operation_id"`
	Scope                string            `json:"scope,omitempty"`
	ProjectRoot          string            `json:"project_root,omitempty"`
	Sources              []string          `json:"sources"`
	CandidateSetID       string            `json:"candidate_set_id"`
	InventoryFingerprint string            `json:"inventory_fingerprint"`
	Items                []ImportItem      `json:"items"`
	Summary              map[string]int    `json:"summary"`
	Warnings             []json.RawMessage `json:"warnings"`
	Blockers             []json.RawMessage `json:"blockers"`
}

func BindImportPlanResolution(plan *ImportPlan) error {
	if plan == nil {
		return errors.New("import plan is required")
	}
	immutable, err := planCanonicalMap(*plan)
	if err != nil {
		return err
	}
	delete(immutable, "plan_id")
	delete(immutable, "resolution_id")
	delete(immutable, "operation_id")
	if items, ok := immutable["items"].([]any); ok {
		for _, raw := range items {
			if item, ok := raw.(map[string]any); ok {
				item["resolution"] = emptyResolutionMap()
			}
		}
	}
	computedPlanID := canonicalHash(immutable)
	if plan.PlanID != "" && plan.PlanID != computedPlanID {
		return errors.New("import plan immutable fields do not match plan_id")
	}
	plan.PlanID = computedPlanID
	resolutions := make([]map[string]any, 0, len(plan.Items))
	for i := range plan.Items {
		item := &plan.Items[i]
		if item.Resolution.ApprovedTargets == nil {
			item.Resolution.ApprovedTargets = []string{}
		}
		if item.Resolution.EnvBindings == nil {
			item.Resolution.EnvBindings = map[string]string{}
		}
		if item.Resolution.ApprovedExecutables == nil {
			item.Resolution.ApprovedExecutables = []string{}
		}
		resolutions = append(resolutions, map[string]any{"item_id": item.ID, "resolution": resolutionMap(item.Resolution)})
	}
	sort.Slice(resolutions, func(i, j int) bool { return resolutions[i]["item_id"].(string) < resolutions[j]["item_id"].(string) })
	plan.ResolutionID = canonicalHash(resolutions)
	plan.OperationID = canonicalHash(map[string]any{"candidate_set_id": plan.CandidateSetID, "plan_id": plan.PlanID, "resolution_id": plan.ResolutionID})[:32]
	return nil
}

func ValidateImportPlanBinding(plan ImportPlan) error {
	if err := validateImportScope(plan.Scope, plan.ProjectRoot); err != nil {
		return err
	}
	originalPlan, originalResolution, originalOperation := plan.PlanID, plan.ResolutionID, plan.OperationID
	if err := BindImportPlanResolution(&plan); err != nil {
		return err
	}
	if plan.PlanID != originalPlan || plan.ResolutionID != originalResolution || plan.OperationID != originalOperation {
		return errors.New("import plan identity binding mismatch")
	}
	return nil
}

func ValidateImportPlanProtocol(plan ImportPlan) error {
	if plan.SchemaVersion != ImportSchemaVersion || plan.Coordinator != "omni-v24" {
		return errors.New("reviewed plan protocol mismatch")
	}
	return validateImportScope(plan.Scope, plan.ProjectRoot)
}

func planCanonicalMap(plan ImportPlan) (map[string]any, error) {
	data, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}
func emptyResolutionMap() map[string]any {
	return map[string]any{"decision": "", "selected_origin_id": "", "approved_targets": []any{}, "env_bindings": map[string]any{}, "approved_executables": []any{}}
}
func resolutionMap(r ImportResolution) map[string]any {
	targets := r.ApprovedTargets
	if targets == nil {
		targets = []string{}
	}
	execs := r.ApprovedExecutables
	if execs == nil {
		execs = []string{}
	}
	env := r.EnvBindings
	if env == nil {
		env = map[string]string{}
	}
	return map[string]any{"decision": r.Decision, "selected_origin_id": r.SelectedOriginID, "approved_targets": targets, "env_bindings": env, "approved_executables": execs}
}
func canonicalHash(value any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(value)
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}))
	return hex.EncodeToString(sum[:])
}

type ImportEnvelope struct {
	OK                    bool        `json:"ok"`
	Kind                  string      `json:"kind"`
	SchemaVersion         int         `json:"schema_version"`
	Plan                  *ImportPlan `json:"plan,omitempty"`
	OperationID           string      `json:"operation_id,omitempty"`
	Coordinator           string      `json:"coordinator,omitempty"`
	State                 string      `json:"state,omitempty"`
	NextAction            string      `json:"next_action,omitempty"`
	FinalizeTokenRequired bool        `json:"finalize_token_required,omitempty"`
	Applied               []string    `json:"applied,omitempty"`
	Retained              []string    `json:"retained,omitempty"`
	Excluded              []string    `json:"excluded,omitempty"`
	Unsupported           []string    `json:"unsupported,omitempty"`
	BackupPath            string      `json:"backup_path,omitempty"`
	Code                  string      `json:"code,omitempty"`
	Message               string      `json:"message,omitempty"`
	Blockers              []string    `json:"blockers,omitempty"`
}

type importResult struct {
	SchemaVersion         int    `json:"schema_version"`
	OperationID           string `json:"operation_id"`
	Coordinator           string `json:"coordinator"`
	State                 string `json:"state"`
	NextAction            string `json:"next_action"`
	FinalizeTokenRequired *bool  `json:"finalize_token_required"`
}

type importPlanWire struct {
	OK   *bool      `json:"ok"`
	Kind string     `json:"kind"`
	Plan ImportPlan `json:"plan"`
}
type importResultWire struct {
	OK     *bool        `json:"ok"`
	Kind   string       `json:"kind"`
	Result importResult `json:"result"`
}
type importErrorWire struct {
	OK    *bool  `json:"ok"`
	Kind  string `json:"kind"`
	Error struct {
		Code        string `json:"code"`
		Message     string `json:"message"`
		OperationID string `json:"operation_id,omitempty"`
	} `json:"error"`
}

type ImportRequest struct {
	CandidateFile string
	PlanFile      string
	PreimageSet   string
	Sources       []string
	FinalizeToken []byte
	OperationID   string
	PersistPlan   string
	ProjectRoot   string
}

func (c *Client) ImportPlan(ctx context.Context, req ImportRequest) (ImportEnvelope, Result, error) {
	if err := validateImportRequest(req, false); err != nil {
		return ImportEnvelope{}, Result{}, err
	}
	args := []string{"import"}
	if req.ProjectRoot == "" {
		args = append(args, "--global")
	}
	for _, source := range req.Sources {
		args = append(args, "--from", source)
	}
	args = append(args, "--candidate-file", req.CandidateFile, "--coordinator", "omni-v24", "--omni-preimage-set", req.PreimageSet, "--format", "json")
	if req.PersistPlan != "" {
		args = append(args, "--plan-json", req.PersistPlan)
	}
	return c.AtProjectRoot(req.ProjectRoot).runImport(ctx, ImportKindPlan, nil, args...)
}

func (c *Client) ImportApply(ctx context.Context, req ImportRequest) (ImportEnvelope, Result, error) {
	if err := validateImportRequest(req, true); err != nil {
		return ImportEnvelope{}, Result{}, err
	}
	args := []string{"import"}
	if req.ProjectRoot == "" {
		args = append(args, "--global")
	}
	args = append(args, "--candidate-file", req.CandidateFile, "--apply-plan", req.PlanFile, "--coordinator", "omni-v24", "--omni-preimage-set", req.PreimageSet, "--token-stdin", "--format", "json")
	envelope, result, err := c.AtProjectRoot(req.ProjectRoot).runImport(ctx, ImportKindApply, req.FinalizeToken, args...)
	if err == nil && (envelope.State != "awaiting-external-commit" || envelope.NextAction != "external-commit-then-finalize" || !envelope.FinalizeTokenRequired) {
		err = errors.New("APM apply did not enter the Omni external-commit fence")
	}
	return envelope, result, err
}

func (c *Client) ImportStatus(ctx context.Context, operationID string) (ImportEnvelope, Result, error) {
	return c.ImportStatusAt(ctx, operationID, "")
}

func (c *Client) ImportStatusAt(ctx context.Context, operationID, projectRoot string) (ImportEnvelope, Result, error) {
	if !validOperationID(operationID) {
		return ImportEnvelope{}, Result{}, errors.New("invalid operation ID")
	}
	if err := validateRequestProjectRoot(projectRoot); err != nil {
		return ImportEnvelope{}, Result{}, err
	}
	return c.AtProjectRoot(projectRoot).runImport(ctx, ImportKindStatus, nil, "import", "status", "--operation", operationID, "--format", "json")
}

func (c *Client) ImportResume(ctx context.Context, req ImportRequest) (ImportEnvelope, Result, error) {
	if err := validateImportRequest(req, true); err != nil {
		return ImportEnvelope{}, Result{}, err
	}
	if !validOperationID(req.OperationID) {
		return ImportEnvelope{}, Result{}, errors.New("invalid operation ID")
	}
	return c.AtProjectRoot(req.ProjectRoot).runImport(ctx, ImportKindResume, req.FinalizeToken, "import", "resume", "--operation", req.OperationID, "--candidate-file", req.CandidateFile, "--apply-plan", req.PlanFile, "--coordinator", "omni-v24", "--omni-preimage-set", req.PreimageSet, "--token-stdin", "--format", "json")
}

func (c *Client) ImportFinalize(ctx context.Context, req ImportRequest) (ImportEnvelope, Result, error) {
	if !validOperationID(req.OperationID) || strings.TrimSpace(req.PreimageSet) == "" || len(req.FinalizeToken) < 32 {
		return ImportEnvelope{}, Result{}, errors.New("invalid finalize request")
	}
	if err := validateRequestProjectRoot(req.ProjectRoot); err != nil {
		return ImportEnvelope{}, Result{}, err
	}
	return c.AtProjectRoot(req.ProjectRoot).runImport(ctx, ImportKindFinalize, req.FinalizeToken, "import", "finalize", "--operation", req.OperationID, "--omni-preimage-set", req.PreimageSet, "--token-stdin", "--format", "json")
}

func (c *Client) ImportCleanup(ctx context.Context, operationID string) (ImportEnvelope, Result, error) {
	return c.ImportCleanupAt(ctx, operationID, "")
}

func (c *Client) ImportCleanupAt(ctx context.Context, operationID, projectRoot string) (ImportEnvelope, Result, error) {
	if !validOperationID(operationID) {
		return ImportEnvelope{}, Result{}, errors.New("invalid operation ID")
	}
	if err := validateRequestProjectRoot(projectRoot); err != nil {
		return ImportEnvelope{}, Result{}, err
	}
	return c.AtProjectRoot(projectRoot).runImport(ctx, ImportKindCleanup, nil, "import", "cleanup", "--operation", operationID, "--confirm", "--format", "json")
}

func (c *Client) ImportRollback(ctx context.Context, operationID, projectRoot string) (ImportEnvelope, Result, error) {
	if !validOperationID(operationID) {
		return ImportEnvelope{}, Result{}, errors.New("invalid operation ID")
	}
	if err := validateRequestProjectRoot(projectRoot); err != nil {
		return ImportEnvelope{}, Result{}, err
	}
	return c.AtProjectRoot(projectRoot).runImport(ctx, ImportKindRollback, nil, "import", "rollback", "--operation", operationID, "--format", "json")
}

func (c *Client) runImport(ctx context.Context, expectedKind string, stdin []byte, args ...string) (ImportEnvelope, Result, error) {
	var result Result
	var err error
	if stdin == nil {
		result, err = c.Run(ctx, args...)
	} else {
		result, err = c.RunPrivate(ctx, stdin, args...)
	}
	if err != nil {
		return decodeImportFailure(result, err)
	}
	var envelope ImportEnvelope
	if expectedKind == ImportKindPlan {
		var wire importPlanWire
		if decodeErr := decodeStrict(result.Stdout, &wire); decodeErr != nil {
			return envelope, result, fmt.Errorf("decode APM import plan envelope: %w", decodeErr)
		}
		if wire.OK == nil || !*wire.OK || wire.Kind != expectedKind {
			return envelope, result, errors.New("APM import plan envelope mismatch")
		}
		envelope = ImportEnvelope{OK: true, Kind: wire.Kind, SchemaVersion: wire.Plan.SchemaVersion, Coordinator: wire.Plan.Coordinator, OperationID: wire.Plan.OperationID, Plan: &wire.Plan}
	} else {
		var wire importResultWire
		if decodeErr := decodeStrict(result.Stdout, &wire); decodeErr != nil {
			return envelope, result, fmt.Errorf("decode APM import result envelope: %w", decodeErr)
		}
		if wire.OK == nil || !*wire.OK || wire.Kind != expectedKind {
			return envelope, result, errors.New("APM import result envelope mismatch")
		}
		r := wire.Result
		if r.FinalizeTokenRequired == nil {
			return envelope, result, errors.New("APM import result envelope missing finalize_token_required")
		}
		envelope = ImportEnvelope{OK: true, Kind: wire.Kind, SchemaVersion: r.SchemaVersion, OperationID: r.OperationID, Coordinator: r.Coordinator, State: r.State, NextAction: r.NextAction, FinalizeTokenRequired: *r.FinalizeTokenRequired}
	}
	if envelope.SchemaVersion != ImportSchemaVersion {
		return envelope, result, errors.New("APM import schema mismatch")
	}
	if envelope.Coordinator != "" && envelope.Coordinator != "omni-v24" {
		return envelope, result, errors.New("APM import coordinator mismatch")
	}
	if envelope.Plan == nil && (!validOperationID(envelope.OperationID) || !knownResultState(envelope.State) || !knownNextAction(envelope.NextAction) || envelope.FinalizeTokenRequired != (envelope.State == "awaiting-external-commit")) {
		return envelope, result, errors.New("APM import result protocol mismatch")
	}
	if envelope.Plan != nil {
		if envelope.Plan.SchemaVersion != ImportSchemaVersion || envelope.Plan.Coordinator != "omni-v24" || validateImportScope(envelope.Plan.Scope, envelope.Plan.ProjectRoot) != nil || !validOperationID(envelope.Plan.OperationID) || !validHexID(envelope.Plan.PlanID, 64) || !validHexID(envelope.Plan.ResolutionID, 64) {
			return envelope, result, errors.New("APM import plan protocol mismatch")
		}
		for _, item := range envelope.Plan.Items {
			if !knownClassification(item.Classification) {
				return envelope, result, fmt.Errorf("unknown APM import classification %q", item.Classification)
			}
		}
	}
	return envelope, result, nil
}

func decodeStrict(data string, dst any) error {
	decoder := json.NewDecoder(bytes.NewBufferString(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func decodeImportFailure(result Result, runErr error) (ImportEnvelope, Result, error) {
	var wire importErrorWire
	if err := decodeStrict(result.Stdout, &wire); err != nil {
		return ImportEnvelope{}, result, fmt.Errorf("APM import failed with invalid error envelope: %w", err)
	}
	if wire.OK == nil || *wire.OK || wire.Kind != ImportKindError || !knownErrorCode(wire.Error.Code) || wire.Error.Message == "" {
		return ImportEnvelope{}, result, errors.New("APM import failed with mismatched error envelope")
	}
	envelope := ImportEnvelope{OK: false, Kind: wire.Kind, OperationID: wire.Error.OperationID, Code: wire.Error.Code, Message: wire.Error.Message}
	return envelope, result, fmt.Errorf("APM import %s: %s: %w", wire.Error.Code, wire.Error.Message, runErr)
}

func validateImportRequest(req ImportRequest, apply bool) error {
	scope := "global"
	if req.ProjectRoot != "" {
		scope = "project"
	}
	if err := validateImportScope(scope, req.ProjectRoot); err != nil {
		return err
	}
	for _, source := range req.Sources {
		if !ValidImportName(source) {
			return fmt.Errorf("invalid import source %q", source)
		}
	}
	if !filepath.IsAbs(req.CandidateFile) {
		return errors.New("candidate file must be absolute")
	}
	if apply && !filepath.IsAbs(req.PlanFile) {
		return errors.New("plan file must be absolute")
	}
	if req.PersistPlan != "" && !filepath.IsAbs(req.PersistPlan) {
		return errors.New("plan output must be absolute")
	}
	if strings.TrimSpace(req.PreimageSet) == "" {
		return errors.New("preimage set is required")
	}
	if apply && len(req.FinalizeToken) < 32 {
		return errors.New("256-bit finalize token is required")
	}
	return nil
}

// ValidImportName validates an opaque APM source/target name without copying APM's registry.
func ValidImportName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for i, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || i > 0 && (r == '-' || r == '_' || r == '.') {
			continue
		}
		return false
	}
	return true
}

func validateImportScope(scope, projectRoot string) error {
	if scope == "" || scope == "global" {
		if projectRoot != "" {
			return errors.New("global import cannot declare a project root")
		}
		return nil
	}
	if scope != "project" {
		return fmt.Errorf("unsupported import scope %q", scope)
	}
	if projectRoot == "" || !filepath.IsAbs(projectRoot) || filepath.Clean(projectRoot) != projectRoot {
		return errors.New("project import requires a canonical absolute project_root")
	}
	resolved, err := filepath.EvalSymlinks(projectRoot)
	if err != nil || resolved != projectRoot {
		return errors.New("project import requires a canonical absolute project_root")
	}
	info, err := os.Stat(projectRoot)
	if err != nil || !info.IsDir() {
		return errors.New("project import requires a canonical absolute project_root")
	}
	return nil
}

func validateRequestProjectRoot(projectRoot string) error {
	if projectRoot == "" {
		return nil
	}
	return validateImportScope("project", projectRoot)
}

func validOperationID(id string) bool {
	return validHexID(id, 32)
}

func validHexID(id string, length int) bool {
	if len(id) != length {
		return false
	}
	for _, r := range id {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

func knownResultState(v string) bool {
	return slices.Contains([]string{"complete", "awaiting-external-commit", "recoverable-partial", "rolled-back"}, v)
}
func knownNextAction(v string) bool {
	return slices.Contains([]string{"none", "external-commit-then-finalize", "resume", "rollback"}, v)
}
func knownErrorCode(v string) bool { return v == "protocol-incompatible" || v == "recoverable-partial" }

func knownClassification(v string) bool {
	known := []string{"already-managed", "duplicate", "importable", "local-package", "needs-choice", "conflict", "secret-blocked", "unsupported", "excluded", "excluded-changed"}
	sort.Strings(known)
	i := sort.SearchStrings(known, v)
	return i < len(known) && known[i] == v
}
