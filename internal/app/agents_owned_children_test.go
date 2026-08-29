package app

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOwnedMCPFingerprintNormalizesPackageRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bundle-a")
	owner := agentBundleOwner{Name: "bundle-a", Original: root, Root: root}
	standalone := apmMCPDep{
		Name:      "owned-mcp",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{filepath.Join(root, "start.mjs")},
		Cwd:       root,
		Env:       map[string]string{"CONFIG": filepath.Join(root, "config.json")},
		Headers:   map[string]string{"x-config": filepath.Join(root, "headers.json")},
	}
	provided := standalone
	provided.Args = []string{"${PLUGIN_ROOT}/start.mjs"}
	provided.Cwd = "${PLUGIN_ROOT}"
	provided.Env = map[string]string{"CONFIG": "${PLUGIN_ROOT}/config.json"}
	provided.Headers = map[string]string{"x-config": "${PLUGIN_ROOT}/headers.json"}

	if agentsMCPFingerprint(standalone, owner.Root) != agentsMCPFingerprint(provided, owner.Root) {
		t.Fatal("equivalent package-root MCP definitions have different fingerprints")
	}
}

func TestOwnedMCPFingerprintIncludesEverySemanticField(t *testing.T) {
	base := apmMCPDep{
		Name:      "owned-mcp",
		Registry:  true,
		Transport: "stdio",
		Version:   "1.0.0",
		Package:   "@acme/server",
		URL:       "https://example.invalid/mcp",
		Command:   "node",
		Args:      []string{"start.mjs", "--safe"},
		Cwd:       "/work",
		Headers:   map[string]string{"Authorization": "${TOKEN}"},
		Env:       map[string]string{"TOKEN": "${TOKEN}"},
		Tools:     []string{"read"},
	}
	tests := map[string]func(*apmMCPDep){
		"name":        func(dep *apmMCPDep) { dep.Name = "other" },
		"registry":    func(dep *apmMCPDep) { dep.Registry = false },
		"transport":   func(dep *apmMCPDep) { dep.Transport = "http" },
		"version":     func(dep *apmMCPDep) { dep.Version = "2.0.0" },
		"package":     func(dep *apmMCPDep) { dep.Package = "@acme/other" },
		"url":         func(dep *apmMCPDep) { dep.URL = "https://other.invalid/mcp" },
		"command":     func(dep *apmMCPDep) { dep.Command = "deno" },
		"args":        func(dep *apmMCPDep) { dep.Args = []string{"start.mjs", "--unsafe"} },
		"cwd":         func(dep *apmMCPDep) { dep.Cwd = "/other" },
		"header name": func(dep *apmMCPDep) { dep.Headers = map[string]string{"X-Token": "${TOKEN}"} },
		"header value": func(dep *apmMCPDep) {
			dep.Headers = map[string]string{"Authorization": "${OTHER_TOKEN}"}
		},
		"env name":  func(dep *apmMCPDep) { dep.Env = map[string]string{"OTHER_TOKEN": "${TOKEN}"} },
		"env value": func(dep *apmMCPDep) { dep.Env = map[string]string{"TOKEN": "${OTHER_TOKEN}"} },
		"tools":     func(dep *apmMCPDep) { dep.Tools = []string{"write"} },
	}
	want := agentsMCPFingerprint(base, "")
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if got := agentsMCPFingerprint(changed, ""); got == want {
				t.Fatalf("%s did not affect MCP fingerprint", name)
			}
		})
	}
}

func TestOwnedLSPFingerprintIncludesEverySemanticField(t *testing.T) {
	base := apmLSPDep{
		Name:                "owned-lsp",
		Transport:           "stdio",
		Command:             "server",
		Args:                []string{"--stdio"},
		Env:                 map[string]string{"MODE": "safe"},
		Cwd:                 "/work",
		ExtensionToLanguage: map[string]string{".go": "go"},
		Initialization:      map[string]any{"format": true},
		WorkspaceFolder:     "/workspace",
		StartupTimeout:      10,
		ShutdownTimeout:     20,
		RestartOnCrash:      true,
		MaxRestarts:         3,
	}
	tests := map[string]func(*apmLSPDep){
		"name":                  func(dep *apmLSPDep) { dep.Name = "other" },
		"transport":             func(dep *apmLSPDep) { dep.Transport = "socket" },
		"command":               func(dep *apmLSPDep) { dep.Command = "other" },
		"args":                  func(dep *apmLSPDep) { dep.Args = []string{"--node-ipc"} },
		"env name":              func(dep *apmLSPDep) { dep.Env = map[string]string{"OTHER": "safe"} },
		"env value":             func(dep *apmLSPDep) { dep.Env = map[string]string{"MODE": "unsafe"} },
		"cwd":                   func(dep *apmLSPDep) { dep.Cwd = "/other" },
		"extension language":    func(dep *apmLSPDep) { dep.ExtensionToLanguage = map[string]string{".go": "golang"} },
		"initialization":        func(dep *apmLSPDep) { dep.Initialization = map[string]any{"format": false} },
		"workspace folder":      func(dep *apmLSPDep) { dep.WorkspaceFolder = "/other" },
		"startup timeout":       func(dep *apmLSPDep) { dep.StartupTimeout++ },
		"shutdown timeout":      func(dep *apmLSPDep) { dep.ShutdownTimeout++ },
		"restart on crash":      func(dep *apmLSPDep) { dep.RestartOnCrash = false },
		"maximum restart count": func(dep *apmLSPDep) { dep.MaxRestarts++ },
	}
	want := agentsLSPFingerprint(base, "")
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if got := agentsLSPFingerprint(changed, ""); got == want {
				t.Fatalf("%s did not affect LSP fingerprint", name)
			}
		})
	}
}

func TestOwnedMCPFingerprintDoesNotResolveRelativePaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "context-mode")
	owner := agentBundleOwner{Name: "context-mode", Original: root, Root: root}
	standalone := apmMCPDep{Name: "context-mode", Transport: "stdio", Command: "node", Args: []string{"./start.mjs"}}
	provided := standalone
	provided.Args = []string{filepath.Join(root, "start.mjs")}

	if agentsMCPFingerprint(standalone, owner.Root) == agentsMCPFingerprint(provided, owner.Root) {
		t.Fatal("relative standalone context-mode path was treated as package-owned absolute path")
	}
}

func TestClassifyAgentsOwnedChildrenMarksExactStandalone(t *testing.T) {
	dep := apmMCPDep{Name: "owned-mcp", Transport: "stdio", Command: "sh", Args: []string{"server.sh"}}
	owned := agentsOwnedChild{
		Kind:        agentsChildMCP,
		Name:        dep.Name,
		Owner:       "bundle-a",
		Fingerprint: agentsMCPFingerprint(dep, ""),
		MCP:         &dep,
	}
	manifest := apmManifest{Dependencies: apmDependencies{MCP: []apmMCPDep{dep}}}

	collisions := classifyAgentsOwnedChildren(manifest, []agentsOwnedChild{owned})
	if len(collisions) != 1 || !collisions[0].Standalone || !collisions[0].Exact {
		t.Fatalf("collisions = %#v", collisions)
	}
}

func TestClassifyAgentsOwnedChildrenMarksDifferingStandalone(t *testing.T) {
	provided := apmMCPDep{Name: "owned-mcp", Transport: "stdio", Command: "sh", Args: []string{"server.sh"}}
	standalone := provided
	standalone.Args = []string{"other.sh"}
	owned := agentsOwnedChild{
		Kind:        agentsChildMCP,
		Name:        provided.Name,
		Owner:       "bundle-a",
		Fingerprint: agentsMCPFingerprint(provided, ""),
		MCP:         &provided,
	}
	manifest := apmManifest{Dependencies: apmDependencies{MCP: []apmMCPDep{standalone}}}

	collisions := classifyAgentsOwnedChildren(manifest, []agentsOwnedChild{owned})
	if len(collisions) != 1 || !collisions[0].Standalone || collisions[0].Exact || !strings.Contains(collisions[0].Message, "args") {
		t.Fatalf("collisions = %#v", collisions)
	}
}

func TestOwnedFingerprintsRetainRawAPMSemantics(t *testing.T) {
	for name, tc := range map[string]struct {
		kind         agentsChildKind
		provided     string
		standalone   string
		changedField string
	}{
		"mcp settings": {
			kind:         agentsChildMCP,
			provided:     "name: service\nregistry: false\ntransport: stdio\nsettings: {mode: safe}\n",
			standalone:   "name: service\nregistry: false\ntransport: stdio\nsettings: {mode: unsafe}\n",
			changedField: "settings",
		},
		"mcp unknown legacy type": {
			kind:         agentsChildMCP,
			provided:     "name: service\nregistry: false\ntransport: stdio\ntype: legacy\n",
			standalone:   "name: service\nregistry: false\ntransport: stdio\ntype: modern\n",
			changedField: "type",
		},
		"lsp settings": {
			kind:         agentsChildLSP,
			provided:     "name: service\ncommand: server\nsettings: {format: true}\n",
			standalone:   "name: service\ncommand: server\nsettings: {format: false}\n",
			changedField: "settings",
		},
		"lsp explicit false presence": {
			kind:         agentsChildLSP,
			provided:     "name: service\ncommand: server\nrestartOnCrash: false\n",
			standalone:   "name: service\ncommand: server\n",
			changedField: "restartOnCrash",
		},
	} {
		t.Run(name, func(t *testing.T) {
			manifest := apmManifest{}
			child := agentsOwnedChild{Kind: tc.kind, Name: "service", Owner: "bundle"}
			if tc.kind == agentsChildMCP {
				var provided, standalone apmMCPDep
				if err := yaml.Unmarshal([]byte(tc.provided), &provided); err != nil {
					t.Fatal(err)
				}
				if err := yaml.Unmarshal([]byte(tc.standalone), &standalone); err != nil {
					t.Fatal(err)
				}
				child.MCP, child.Fingerprint = &provided, agentsMCPFingerprint(provided, "")
				manifest.Dependencies.MCP = []apmMCPDep{standalone}
			} else {
				var provided, standalone apmLSPDep
				if err := yaml.Unmarshal([]byte(tc.provided), &provided); err != nil {
					t.Fatal(err)
				}
				if err := yaml.Unmarshal([]byte(tc.standalone), &standalone); err != nil {
					t.Fatal(err)
				}
				child.LSP, child.Fingerprint = &provided, agentsLSPFingerprint(provided, "")
				manifest.Dependencies.LSP = []apmLSPDep{standalone}
			}
			collisions := classifyAgentsOwnedChildren(manifest, []agentsOwnedChild{child})
			if len(collisions) != 1 || collisions[0].Exact || !strings.Contains(collisions[0].Message, tc.changedField) {
				t.Fatalf("collision = %#v", collisions)
			}
		})
	}
}

func TestSameOwnerDifferingDefinitionsAreNeverExact(t *testing.T) {
	standalone := apmMCPDep{Name: "shared", Registry: false, Transport: "stdio", Command: "one"}
	other := standalone
	other.Command = "two"
	children := []agentsOwnedChild{
		{Kind: agentsChildMCP, Name: "shared", Owner: "bundle", MCP: &standalone, Fingerprint: agentsMCPFingerprint(standalone, "")},
		{Kind: agentsChildMCP, Name: "shared", Owner: "bundle", MCP: &other, Fingerprint: agentsMCPFingerprint(other, "")},
	}
	collisions := classifyAgentsOwnedChildren(apmManifest{Dependencies: apmDependencies{MCP: []apmMCPDep{standalone}}}, children)
	if len(collisions) != 2 || collisions[0].Exact || collisions[1].Exact {
		t.Fatalf("same-owner variants classified exact: %#v", collisions)
	}
}
