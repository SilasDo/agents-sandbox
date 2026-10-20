package testutil

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestInstalledSeamFamilies(t *testing.T) {
	const modulePath = "example.com/mod"
	const pkgDir = "internal/example"

	writeMainTest := func(t *testing.T, source string) string {
		t.Helper()
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, pkgDir), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if source != "" {
			path := filepath.Join(root, pkgDir, "main_test.go")
			if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
				t.Fatalf("write main_test.go: %v", err)
			}
		}
		return root
	}

	tests := []struct {
		name    string
		source  string
		want    []string
		wantErr bool
	}{
		{
			name: "full set via the testutil wrapper",
			source: `package example

import (
	"testing"

	mocks "example.com/mod/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	mocks.InitFailFastMocks(m)
}
`,
			want: []string{"configpaths", "docker", "doctor", "msb"},
		},
		{
			name: "direct installers with an aliased import",
			source: `package example

import (
	"testing"

	"example.com/mod/internal/configpaths"
	"example.com/mod/internal/sandbox/docker"
	sandboxmsb "example.com/mod/internal/sandbox/msb"
)

func TestMain(m *testing.M) {
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	sandboxmsb.InstallFailFastGet()
	m.Run()
}
`,
			want: []string{"configpaths", "docker", "msb"},
		},
		{
			name: "same-package installer via an unqualified call",
			source: `package doctor

import (
	"testing"

	"example.com/mod/internal/configpaths"
	"example.com/mod/internal/sandbox/docker"
	"example.com/mod/internal/sandbox/msb"
)

func TestMain(m *testing.M) {
	configpaths.InstallFailFastConfigPaths()
	docker.InstallFailFastGet()
	InstallFailFast()
	msb.InstallFailFastGet()
	m.Run()
}
`,
			want: []string{"configpaths", "docker", "doctor", "msb"},
		},
		{
			name: "a comment mentioning an installer is not a call",
			source: `package example

import "testing"

func TestMain(m *testing.M) {
	// cannot call mocks.InitFailFastMocks(m) because of a cycle
	m.Run()
}
`,
			want: []string{},
		},
		{
			name: "an installer from another module is ignored",
			source: `package example

import (
	"testing"

	"example.com/other/thing"
)

func TestMain(m *testing.M) {
	thing.InstallFailFastGet()
	m.Run()
}
`,
			want: []string{},
		},
		{
			name:    "missing main_test.go",
			source:  "",
			wantErr: true,
		},
		{
			name: "main_test.go without a TestMain",
			source: `package example

import "testing"

func TestSomething(t *testing.T) {}
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeMainTest(t, tt.source)
			got, err := installedSeamFamilies(root, pkgDir, modulePath)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("installedSeamFamilies() error = nil, want error; got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("installedSeamFamilies() error = %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("installedSeamFamilies() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAcyclicSeamFamilies(t *testing.T) {
	// The real module graph's shape among the seam packages: docker imports
	// nothing module-local, msb imports configpaths, and doctor imports all of
	// docker, configpaths, and msb.
	transitive := map[string]map[string]bool{
		"internal/configpaths":    {},
		"internal/sandbox/docker": {},
		"internal/sandbox/msb": {
			"internal/configpaths": true,
			"internal/agent":       true,
			"internal/filewalk":    true,
		},
		"internal/sandbox/doctor": {
			"internal/sandbox/docker":  true,
			"internal/configpaths":     true,
			"internal/sandbox/msb":     true,
			"internal/termio":          true,
			"internal/sandbox/runtime": true,
		},
	}

	tests := []struct {
		name   string
		pkgDir string
		want   []string
	}{
		{"a seam imported by others drops those importers", "internal/configpaths", []string{"configpaths", "docker"}},
		{
			"a seam imported by doctor drops only doctor",
			"internal/sandbox/docker",
			[]string{"configpaths", "docker", "msb"},
		},
		{"another seam imported by doctor", "internal/sandbox/msb", []string{"configpaths", "docker", "msb"}},
		{
			"the top of the graph may install every family",
			"internal/sandbox/doctor",
			[]string{"configpaths", "docker", "doctor", "msb"},
		},
		{
			"a package imported by doctor still drops doctor",
			"internal/termio",
			[]string{"configpaths", "docker", "msb"},
		},
		{
			"an unrelated package may install every family",
			"internal/sandbox/vm",
			[]string{"configpaths", "docker", "doctor", "msb"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := acyclicSeamFamilies(tt.pkgDir, transitive)
			if !slices.Equal(got, tt.want) {
				t.Errorf("acyclicSeamFamilies(%q) = %v, want %v", tt.pkgDir, got, tt.want)
			}
		})
	}
}
