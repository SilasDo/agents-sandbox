package state

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"

	"go.yaml.in/yaml/v3"
)

// homeStateSchemaFields and rootStateSchemaFields pin the persisted field set of
// the current schema versions. A field change that does not bump the schema lets
// an older binary read the new document as current and silently erase the new
// field on its next write (#337). When this test fails, bump HomeStateSchema or
// RootStateSchema and update the pinned list together.
var (
	homeStateSchemaFields = []string{
		"agent_source",
		"agent_version",
		"docker_source",
		"env_state",
		"env_state.hash",
		"env_state.names",
		"home_volume",
		"image_digest",
		"mount_state",
		"mount_state.hash",
		"mount_state.names",
		"network_state",
		"network_state.hash",
		"network_state.names",
		"offered_versions",
		"recovered_home_volume",
		"schema",
		"secret_state",
		"secret_state.hash",
		"secret_state.names",
	}
	rootStateSchemaFields = []string{
		"adopted_home_volumes",
		"adopted_home_volumes[].checkout_slug",
		"adopted_home_volumes[].volume",
		"migrations",
		"migrations[].copied",
		"migrations[].excluded",
		"migrations[].kept",
		"migrations[].status",
		"schema",
		"updates",
		"updates.agents",
		"updates.agents[].last_checked",
		"updates.agents[].latest_version",
		"updates.launcher",
		"updates.launcher.dismissed_versions",
		"updates.launcher.last_checked",
	}
)

func TestHomeStateSchemaFieldsArePinned(t *testing.T) {
	assertSchemaFields(t, "HomeStateSchema", reflect.TypeFor[HomeState](), homeStateSchemaFields)
}

func TestRootStateSchemaFieldsArePinned(t *testing.T) {
	assertSchemaFields(t, "RootStateSchema", reflect.TypeFor[RootState](), rootStateSchemaFields)
}

func assertSchemaFields(t *testing.T, schemaConstant string, documentType reflect.Type, pinned []string) {
	t.Helper()
	got := append(yamlFieldPaths(documentType, ""), "schema")
	slices.Sort(got)
	if !slices.Equal(got, pinned) {
		t.Fatalf("persisted fields changed; bump %s and update the pinned list.\ngot:    %v\npinned: %v",
			schemaConstant, got, pinned)
	}
}

// yamlFieldPaths lists the yaml key paths of a persisted struct, descending into
// nested structs and map values ("[]" marks a map value), so a nested field
// change is caught as well as a top-level one.
func yamlFieldPaths(structType reflect.Type, prefix string) []string {
	var paths []string
	for field := range structType.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		path := prefix + name
		paths = append(paths, path)
		paths = append(paths, nestedYAMLFieldPaths(field.Type, path)...)
	}
	return paths
}

func nestedYAMLFieldPaths(fieldType reflect.Type, path string) []string {
	switch {
	case fieldType.Kind() == reflect.Map:
		return nestedYAMLFieldPaths(fieldType.Elem(), path+"[]")
	case fieldType.Kind() == reflect.Struct && fieldType != reflect.TypeFor[time.Time]():
		return yamlFieldPaths(fieldType, path+".")
	default:
		return nil
	}
}

func TestParseRejectsNewerSchema(t *testing.T) {
	newer := []byte("schema: 99\nhome_volume: vol\n")
	if _, err := parseHomeState(newer); !errors.Is(err, ErrStateSchemaUnsupported) {
		t.Errorf("parseHomeState error = %v, want ErrStateSchemaUnsupported", err)
	}
	if _, err := parseRootState(newer); !errors.Is(err, ErrStateSchemaUnsupported) {
		t.Errorf("parseRootState error = %v, want ErrStateSchemaUnsupported", err)
	}
}

func TestParseRejectsNewerSchemaIsNotUnparsable(t *testing.T) {
	_, err := parseHomeState([]byte("schema: 99\n"))
	if errors.Is(err, ErrStateUnparsable) {
		t.Fatalf("a newer-schema document must not be classified unparsable (recovery would discard it): %v", err)
	}
}

func TestParseAcceptsUnversionedAndCurrentSchema(t *testing.T) {
	for _, document := range []string{"home_volume: vol\n", "schema: 1\nhome_volume: vol\n"} {
		if _, err := parseHomeState([]byte(document)); err != nil {
			t.Errorf("parseHomeState(%q) = %v", document, err)
		}
		if _, err := parseRootState([]byte(document)); err != nil {
			t.Errorf("parseRootState(%q) = %v", document, err)
		}
	}
}

func TestParseRejectsNegativeSchemaAsUnparsable(t *testing.T) {
	if _, err := parseHomeState([]byte("schema: -1\n")); !errors.Is(err, ErrStateUnparsable) {
		t.Errorf("parseHomeState error = %v, want ErrStateUnparsable", err)
	}
	if _, err := parseRootState([]byte("schema: -1\n")); !errors.Is(err, ErrStateUnparsable) {
		t.Errorf("parseRootState error = %v, want ErrStateUnparsable", err)
	}
}

func TestWriteStampsCurrentSchema(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}
	if err := writeState(k, HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	if err := writeRootState(RootState{}); err != nil {
		t.Fatalf("writeRootState: %v", err)
	}
	assertFileSchema(t, stateFile(k), HomeStateSchema)
	assertFileSchema(t, rootStateFile(), RootStateSchema)
}

func assertFileSchema(t *testing.T, path string, want int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var header struct {
		Schema int `yaml:"schema"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil || header.Schema != want {
		t.Errorf("%s schema = %d (err %v), want %d; document:\n%s", path, header.Schema, err, want, data)
	}
}

func TestTransactionsRefuseNewerSchemaWithoutWriting(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}
	newer := []byte("schema: 99\nhome_volume: vol\nfuture_field: keep\n")
	seedFile(t, stateFile(k), newer)
	seedFile(t, rootStateFile(), newer)

	for name, run := range map[string]func() error{
		"WithHomeState": func() error {
			return WithHomeState(k, func(*HomeState, bool) (bool, error) { return true, nil })
		},
		"RecoverHomeState": func() error {
			return RecoverHomeState(k, func(*HomeState, bool) (bool, error) { return true, nil })
		},
		"WithRootState": func() error {
			_, err := WithRootState(true, func(txn *RootTxn) error { return txn.Commit() })
			return err
		},
	} {
		if err := run(); !errors.Is(err, ErrStateSchemaUnsupported) {
			t.Errorf("%s error = %v, want ErrStateSchemaUnsupported", name, err)
		}
	}
	for _, path := range []string{stateFile(k), rootStateFile()} {
		if got, _ := os.ReadFile(path); string(got) != string(newer) {
			t.Errorf("%s was rewritten: %q", path, got)
		}
	}
}

func seedFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
}
