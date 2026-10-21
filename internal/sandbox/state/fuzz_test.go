package state

import (
	"bytes"
	"errors"
	"testing"

	"gopkg.in/yaml.v3"
)

// FuzzParseState drives arbitrary bytes through both persisted-state parsers:
// the per-project HomeState and the launcher-global RootState. A state file is
// hand-editable and an interrupted write can corrupt either one, so neither may
// panic, a malformed document must be classified as ErrStateUnparsable (never a
// bare error), and a document the parser accepts must be one the writer
// reproduces — otherwise the reader and writer disagree about what is on disk.
//
// One target for both documents: `make fuzz` runs `go test -fuzz='^Fuzz'`, and
// Go requires that regexp to match exactly one fuzz test per package.
func FuzzParseState(f *testing.F) {
	for _, seed := range []string{
		"",
		"{}",
		"~",
		"[]",
		"home_volume: vol\nimage_digest: sha256:abc\n",
		"home_volume: vol\nagent_version: 1.2.3\noffered_versions: [1.2.3]\n",
		"env_state:\n  hash: abc\n  names: [FOO, BAR]\n",
		"migrations:\n  runtime_home:\n    status: completed\n    copied: [vol-a]\n",
		"agents:\n  opencode:\n    last_checked: 2026-01-02T03:04:05Z\n    latest_version: 1.2.3\n",
		"migrations: [unterminated",
		"home_volume: [not, a, string]\n",
		"home:\n\tvolume: tab\n",
		"migrations:\n  a: {status: x}\n  a: {status: y}\n",
		"a: &anchor [1]\nb: *anchor\n",
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		home, err := parseHomeState(data)
		if err != nil {
			if !errors.Is(err, ErrStateUnparsable) {
				t.Fatalf("parseHomeState(%q) error = %v, want ErrStateUnparsable", data, err)
			}
		} else {
			assertWriterReproduces(t, "parseHomeState", home, parseHomeState)
		}

		root, err := parseRootState(data)
		if err != nil {
			if !errors.Is(err, ErrStateUnparsable) {
				t.Fatalf("parseRootState(%q) error = %v, want ErrStateUnparsable", data, err)
			}
		} else {
			assertWriterReproduces(t, "parseRootState", root, parseRootState)
		}
	})
}

// assertWriterReproduces checks that a document the parser accepted is stable
// under the writer: marshaling it and parsing the result yields a document that
// marshals to the same bytes. The parser must never accept a document the
// writer canonicalizes into something else.
func assertWriterReproduces[T any](t *testing.T, name string, doc T, parse func([]byte) (T, error)) {
	t.Helper()
	first, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("%s: marshaling an accepted document failed: %v", name, err)
	}
	reparsed, err := parse(first)
	if err != nil {
		t.Fatalf("%s: re-parsing a marshaled document failed: %v (bytes %q)", name, err, first)
	}
	second, err := yaml.Marshal(reparsed)
	if err != nil {
		t.Fatalf("%s: re-marshaling an accepted document failed: %v", name, err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("%s: marshaled form is not stable across a re-parse:\nfirst:  %q\nsecond: %q", name, first, second)
	}
}
