package apm

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestVendoredImportProtocolHashes(t *testing.T) {
	expected := map[string]string{"testdata/import-candidates-v1.json": "c624ff8586da7effa458c2a07d0433fc222bacf5c2801da82070cfff13811038", "testdata/import-plan-v1.json": "72cb19bb870db9f824f1f558104d31a440f78827b6a0829d89009d973d649901", "testdata/import-result-v1.json": "38d3c17a11c4c7375c67a6983b09bfac634128b21ffbb0eabe10771d63b0dc15", "testdata/import-envelope-v1.json": "0dd40f94af044a537157b9985a97d66e5d5f13e3947783c476436d06b7c7a4e0", "testdata/envelopes-v1.json": "fe58d241874086251c724424c3f11a3334ef45fd3fdb506bd3d0ca1769ac2961"}
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
