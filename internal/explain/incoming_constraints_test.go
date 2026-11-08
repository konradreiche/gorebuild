package explain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIncomingFactoryRejectsLeakedTypesAndCapturedLocals(t *testing.T) {
	for _, test := range []struct{ name, extra, call, local string }{
		{"leaked interface", "type Local interface{Serve()}\nfunc NewLocal() Local{return handler{}}\n", "upstream.NewLocal()", ""},
		{"local capture", "func NewFlag(bool) contract.Handler{return handler{}}\n", "upstream.NewFlag(local)", "local:=enabled;_ = local;"},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, root := factoryFixture(t)
			file := filepath.Join(root, "upstream/upstream.go")
			previous, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			sourceFile(t, root, "upstream/upstream.go", string(previous)+test.extra)
			sourceFile(t, root, "dep/dep.go", "package dep\nimport \"example.org/repo/upstream\"\nimport \"example.org/repo/contract\"\ntype API struct{h contract.Handler}\nfunc NewAPI(enabled bool)*API{"+test.local+"return &API{h:"+test.call+"}}\n")
			report, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			boundary := incomingByName(t, report, upstreamName).Boundaries[1]
			if boundary.Status != "rejected" || boundary.StructurallySupported {
				t.Fatal("unsupported inversion treated as supported", boundary)
			}
		})
	}
}

func TestIncomingCycleDetection(t *testing.T) {
	data, root := factoryFixture(t)
	for index := range data.Graph {
		if data.Graph[index].ImportPath == upstreamName {
			data.Graph[index].Imports = append(data.Graph[index].Imports, consumerName)
		}
	}
	report, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	boundary := incomingByName(t, report, upstreamName).Boundaries[1]
	if boundary.Status != "rejected" || !strings.Contains(strings.Join(boundary.Reasons, " "), "cycle") {
		t.Fatal("new graph cycle not rejected", boundary)
	}
}

func TestIncomingCacheArtifactCompatibility(t *testing.T) {
	data, root := factoryFixture(t)
	file := saveIncomingCache(t, data, true, false)
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		SchemaVersion int             `json:"schema_version"`
		Cache         CacheDiagnostic `json:"cache_diagnostic"`
	}
	if err := json.Unmarshal(content, &saved); err != nil {
		t.Fatal(err)
	}
	saved.Cache.Environment = map[string]string{"GODEBUG": "gocachehash=1,gocacheverify=1"}
	content, err = json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{}, file)
	if err != nil {
		t.Fatal(err)
	}
	if report.CacheEvidence.Complete || !strings.Contains(strings.Join(report.CacheEvidence.Warnings, " "), "GODEBUG") {
		t.Fatal("non-hash diagnostic configuration mismatch ignored")
	}
	saved.Cache.Package = "other/package"
	content, err = json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Suggest(context.Background(), data, "dep", root, "", TypeOptions{}, file); err != nil {
		return
	}
	t.Fatal("cache evidence for different package accepted")
}
