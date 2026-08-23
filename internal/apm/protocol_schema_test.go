package apm

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestVendoredImportProtocolHashes(t *testing.T) {
	expected := map[string]string{"testdata/import-candidates-v1.json": "533578ddac74ab2eff8ce215d7a5efbc32594ab0a3056da9f192bf79320b2579", "testdata/import-plan-v1.json": "de220efc5d59f06fd81b413dc62915d3d4bb0bda115e13bfa634936a4e26f759", "testdata/import-result-v1.json": "38d3c17a11c4c7375c67a6983b09bfac634128b21ffbb0eabe10771d63b0dc15", "testdata/import-envelope-v1.json": "0dd40f94af044a537157b9985a97d66e5d5f13e3947783c476436d06b7c7a4e0", "testdata/envelopes-v1.json": "c9f41d6e445835926ede647f5b48942c41b2f2421589aa1dbbc665626d123dbf"}
	for name, want := range expected {
		data, err := protocolSchemas.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Fatalf("%s hash=%s want=%s", name, got, want)
		}
	}
}

//go:embed testdata/*.json
var protocolSchemas embed.FS

func TestVendoredImportSchemasAreStrictV1(t *testing.T) {
	for _, name := range []string{"testdata/import-candidates-v1.json", "testdata/import-plan-v1.json", "testdata/import-result-v1.json"} {
		data, err := protocolSchemas.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatal(err)
		}
		if schema["additionalProperties"] != false {
			t.Fatalf("%s is not strict", name)
		}
		properties, _ := schema["properties"].(map[string]any)
		version, _ := properties["schema_version"].(map[string]any)
		if version["const"] != float64(1) {
			t.Fatalf("%s schema version = %v", name, version["const"])
		}
	}
}

func TestVendoredCandidateSchemaLeavesAPMNamesExtensible(t *testing.T) {
	data, err := protocolSchemas.ReadFile("testdata/import-candidates-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	properties := schema["properties"].(map[string]any)
	sourceItems := properties["sources"].(map[string]any)["items"].(map[string]any)
	candidateItems := properties["candidates"].(map[string]any)["items"].(map[string]any)
	targetItems := candidateItems["properties"].(map[string]any)["source_target"].(map[string]any)["items"].(map[string]any)
	for name, item := range map[string]map[string]any{"source": sourceItems, "target": targetItems} {
		if item["type"] != "string" || item["minLength"] != float64(1) || item["enum"] != nil {
			t.Fatalf("%s names are not extensible: %#v", name, item)
		}
	}
}

func TestVendoredEnvelopeGoldenUsesExactKindsAndWrappers(t *testing.T) {
	data, err := protocolSchemas.ReadFile("testdata/envelopes-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]json.RawMessage
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	var plan importPlanWire
	if err := decodeStrict(string(golden["scan"]), &plan); err != nil || plan.Kind != ImportKindPlan || plan.OK == nil || !*plan.OK {
		t.Fatalf("scan=%#v err=%v", plan, err)
	}
	if err := ValidateImportPlanBinding(plan.Plan); err != nil {
		t.Fatalf("golden identity binding: %v", err)
	}
	for key, kind := range map[string]string{"apply": ImportKindApply, "status": ImportKindStatus} {
		var result importResultWire
		err := decodeStrict(string(golden[key]), &result)
		if err != nil || result.Kind != kind || result.OK == nil || !*result.OK {
			t.Fatalf("%s=%#v err=%v", key, result, err)
		}
	}
	var failure importErrorWire
	if err := decodeStrict(string(golden["error"]), &failure); err != nil || failure.Kind != ImportKindError || failure.OK == nil || *failure.OK {
		t.Fatalf("error=%#v err=%v", failure, err)
	}
}
