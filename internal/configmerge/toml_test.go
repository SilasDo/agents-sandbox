package configmerge

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestBuildMergedForTOMLDeepMergesTOMLSnippets(t *testing.T) {
	dir := t.TempDir()
	writeSnippet(t, dir, "config-a.toml", "model = \"gpt-5\"\n[sandbox_workspace_write]\nnetwork_access = false\n")
	writeSnippet(t, dir, "config-b.toml", "[sandbox_workspace_write]\nnetwork_access = true\nretries = 3\n")

	data, sources, has, err := BuildMergedFor("/home/dev/.codex/config.toml", "config*.toml", dir, "")
	if err != nil || !has {
		t.Fatalf("BuildMergedFor: has=%v err=%v", has, err)
	}
	if len(sources) != 2 {
		t.Errorf("sources = %v, want both snippets", sources)
	}
	var merged struct {
		Model   string `toml:"model"`
		Sandbox struct {
			NetworkAccess bool `toml:"network_access"`
			Retries       int  `toml:"retries"`
		} `toml:"sandbox_workspace_write"`
	}
	if err := toml.Unmarshal(data, &merged); err != nil {
		t.Fatalf("output is not TOML: %v\n%s", err, data)
	}
	if merged.Model != "gpt-5" || !merged.Sandbox.NetworkAccess || merged.Sandbox.Retries != 3 {
		t.Errorf("merged = %+v, want model, network_access=true, retries=3", merged)
	}
	if !strings.Contains(string(data), "retries = 3\n") {
		t.Errorf("integers must stay integers in TOML output; got:\n%s", data)
	}
}

func TestBuildMergedForJSONPathKeepsJSONOutput(t *testing.T) {
	dir := t.TempDir()
	writeSnippet(t, dir, "settings-a.json", `{"a": 1}`)

	data, _, has, err := BuildMergedFor("/home/dev/.claude/settings.json", "settings*.json*", dir, "")
	if err != nil || !has {
		t.Fatalf("BuildMergedFor: has=%v err=%v", has, err)
	}
	if string(data) != "{\n  \"a\": 1\n}\n" {
		t.Errorf("data = %q, want indented JSON", data)
	}
}

func TestBuildMergedForSkipsInvalidTOMLSnippet(t *testing.T) {
	dir := t.TempDir()
	writeSnippet(t, dir, "config-bad.toml", "not = = toml")
	writeSnippet(t, dir, "config-good.toml", "ok = true\n")

	data, sources, has, err := BuildMergedFor("config.toml", "config*.toml", dir, "")
	if err != nil || !has {
		t.Fatalf("BuildMergedFor: has=%v err=%v", has, err)
	}
	if len(sources) != 1 || filepath.Base(sources[0]) != "config-good.toml" {
		t.Errorf("sources = %v, want only config-good.toml", sources)
	}
	if string(data) != "ok = true\n" {
		t.Errorf("data = %q, want ok = true", data)
	}
}

func TestDocumentsEqual(t *testing.T) {
	tests := []struct {
		name, path, a, b string
		want             bool
	}{
		{"json reordered", "settings.json", `{"a":1,"b":2}`, `{"b":2, "a":1}`, true},
		{"json differs", "settings.json", `{"a":1}`, `{"a":2}`, false},
		{"json unparsable", "settings.json", `{"a":1}`, `nope`, false},
		{"json unparsable first", "settings.json", `nope`, `{"a":1}`, false},
		{"toml reformatted", "config.toml", "a = 1\n[t]\nx = \"y\"\n", "t = { x = \"y\" }\na = 1\n", true},
		{"toml differs", "config.toml", "a = 1\n", "a = 2\n", false},
		{"toml unparsable", "config.toml", "a = 1\n", "a = = 1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DocumentsEqual(tt.path, []byte(tt.a), []byte(tt.b)); got != tt.want {
				t.Errorf("DocumentsEqual = %v, want %v", got, tt.want)
			}
		})
	}
}
