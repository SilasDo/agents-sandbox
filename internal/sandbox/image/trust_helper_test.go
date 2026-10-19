package image

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// vendoredTrustHelperPath is the on-disk source of the trust helper asset. The
// test reads it directly so the assertion fails when the build context ships
// any other bytes, rather than comparing the asset against itself.
const vendoredTrustHelperPath = "data/msb-trust.sh"

// TestWriteDockerfileTarShipsTrustHelperExecutable covers the synthetic build
// context the image build consumes. The final stage COPYs the helper into the
// image and takes the executable bit from the context, so the file has to
// travel as a regular file with mode 755; a context entry the COPY cannot
// resolve fails the whole build.
func TestWriteDockerfileTarShipsTrustHelperExecutable(t *testing.T) {
	var context bytes.Buffer
	if err := writeDockerfileTar(&context, []byte("FROM scratch\n")); err != nil {
		t.Fatalf("writeDockerfileTar: %v", err)
	}

	entries, modes := readTarEntries(t, &context)

	want, err := os.ReadFile(vendoredTrustHelperPath)
	if err != nil {
		t.Fatalf("read vendored trust helper: %v", err)
	}
	if got := entries[trustHelperAsset]; got != string(want) {
		t.Errorf("tar entry %q carries %d bytes, want the vendored script's %d", trustHelperAsset, len(got), len(want))
	}
	if got := modes[trustHelperAsset]; got != 0o755 {
		t.Errorf("tar entry %q mode = %o, want 755 so the installed command is executable", trustHelperAsset, got)
	}
	if got := modes[pathMergeAsset]; got != 0o644 {
		t.Errorf("tar entry %q mode = %o, want 644 (only the helper is a command)", pathMergeAsset, got)
	}
}

// TestRenderDockerfileInstallsTrustHelper covers the runner-stage wiring: a
// build-context asset changes nothing until the final stage copies it into the
// image, and a path outside the composed PATH would not be runnable by name.
func TestRenderDockerfileInstallsTrustHelper(t *testing.T) {
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	final := finalStage(t, mustRender(t, a, nil, true))
	if !strings.Contains(final, "COPY "+trustHelperAsset+" "+trustHelperDest) {
		t.Errorf("final stage must COPY %s to %s; got:\n%s", trustHelperAsset, trustHelperDest, final)
	}
}

// readTarEntries returns the tar's entry contents and modes keyed by name.
func readTarEntries(t *testing.T, r io.Reader) (map[string]string, map[string]int64) {
	t.Helper()
	entries := map[string]string{}
	modes := map[string]int64{}
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return entries, modes
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read tar entry %q: %v", header.Name, err)
		}
		entries[header.Name] = string(content)
		modes[header.Name] = header.Mode
	}
}
