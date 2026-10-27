package upgrade

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// releaseServer serves the current platform's release asset and (when
// checksums is non-empty) the release's checksums.txt at the tagged URLs the
// updater builds, pointing downloadBase at itself.
func releaseServer(t *testing.T, version string, asset []byte, checksums string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case fmt.Sprintf("/v%s/%s", version, assetName()):
			_, _ = w.Write(asset)
		case fmt.Sprintf("/v%s/%s", version, checksumsAsset):
			if checksums == "" {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, checksums)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	orig := downloadBase
	downloadBase = srv.URL
	t.Cleanup(func() { downloadBase = orig })
}

// checksumLine formats a sha256sum entry for name.
func checksumLine(digest []byte, name string) string {
	return fmt.Sprintf("%s  %s\n", hex.EncodeToString(digest), name)
}

// updateTempFiles lists the updater's temp files left in dir.
func updateTempFiles(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".agents-sandbox-update-*"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	return matches
}

func TestChecksumForAsset(t *testing.T) {
	// A fixed 64-character digest; its value is irrelevant to parsing.
	const digest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	tests := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{name: "two-space line", body: digest + "  " + assetName() + "\n", want: digest},
		{name: "surrounding blank lines", body: "\n" + digest + "  " + assetName() + "\n\n", want: digest},
		{name: "uppercase digest normalized", body: strings.ToUpper(digest) + "  " + assetName() + "\n", want: digest},
		{
			name: "other assets ignored",
			body: digest + "  agents-sandbox-other-arch\n" + digest + "  " + assetName() + "\n",
			want: digest,
		},
		{name: "missing entry", body: digest + "  agents-sandbox-other-arch\n", wantErr: true},
		{name: "empty file", body: "", wantErr: true},
		{name: "short digest", body: "abcd  " + assetName() + "\n", wantErr: true},
		{name: "non-hex digest", body: strings.Repeat("z", 64) + "  " + assetName() + "\n", wantErr: true},
		{name: "extra field", body: digest + "  " + assetName() + " extra\n", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checksumForAsset([]byte(tc.body), assetName())
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFileSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := fileSHA256(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sum := sha256.Sum256([]byte("payload"))
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFileSHA256OpenError(t *testing.T) {
	if _, err := fileSHA256(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected error for a missing file")
	}
}

func TestFileSHA256ReadError(t *testing.T) {
	// Opening a directory succeeds; copying from it fails, so this exercises
	// the read-error branch.
	if _, err := fileSHA256(t.TempDir()); err == nil {
		t.Fatal("expected error when hashing a directory")
	}
}

func TestFetchChecksumsTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), maxChecksumsBytes+1))
	}))
	t.Cleanup(srv.Close)
	orig := downloadBase
	downloadBase = srv.URL
	t.Cleanup(func() { downloadBase = orig })

	if _, err := fetchChecksums(context.Background(), "2.0.0"); err == nil {
		t.Fatal("expected error for an oversized checksums.txt")
	}
}

func TestFetchChecksumsNetworkError(t *testing.T) {
	orig := downloadBase
	downloadBase = "http://127.0.0.1:1"
	t.Cleanup(func() { downloadBase = orig })

	if _, err := fetchChecksums(context.Background(), "2.0.0"); err == nil {
		t.Fatal("expected error for an unreachable checksums endpoint")
	}
}

func TestFetchChecksumsTruncatedBody(t *testing.T) {
	// A body shorter than its declared Content-Length makes the read fail, so
	// this exercises the read-error branch.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1024")
		_, _ = w.Write([]byte("short"))
	}))
	t.Cleanup(srv.Close)
	orig := downloadBase
	downloadBase = srv.URL
	t.Cleanup(func() { downloadBase = orig })

	if _, err := fetchChecksums(context.Background(), "2.0.0"); err == nil {
		t.Fatal("expected error for a truncated checksums.txt")
	}
}

func TestFetchChecksumsBadURL(t *testing.T) {
	orig := downloadBase
	downloadBase = "://missing-scheme"
	t.Cleanup(func() { downloadBase = orig })

	if _, err := fetchChecksums(context.Background(), "2.0.0"); err == nil {
		t.Fatal("expected error for an unbuildable checksums request")
	}
}

func TestVerifyAssetChecksum(t *testing.T) {
	asset := []byte("release-binary")
	digest := sha256.Sum256(asset)

	path := filepath.Join(t.TempDir(), "downloaded")
	if err := os.WriteFile(path, asset, 0o600); err != nil {
		t.Fatal(err)
	}

	// A prerelease tag proves the checksums fetch is addressed by the same
	// release tag as the asset download.
	const version = "1.9.0-canary.3"
	releaseServer(t, version, nil, checksumLine(digest[:], assetName()))
	if err := verifyAssetChecksum(context.Background(), version, path, assetName()); err != nil {
		t.Fatalf("expected a verified match, got %v", err)
	}
}

func TestVerifyAssetChecksumUnreadableAsset(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	releaseServer(t, "2.0.0", nil, checksumLine(make([]byte, sha256.Size), assetName()))
	if err := verifyAssetChecksum(context.Background(), "2.0.0", missing, assetName()); err == nil {
		t.Fatal("expected error when the downloaded asset cannot be read")
	}
}

func TestVerifyAssetChecksumMismatch(t *testing.T) {
	asset := []byte("release-binary")
	path := filepath.Join(t.TempDir(), "downloaded")
	if err := os.WriteFile(path, asset, 0o600); err != nil {
		t.Fatal(err)
	}

	wrong := sha256.Sum256([]byte("tampered"))
	releaseServer(t, "2.0.0", nil, checksumLine(wrong[:], assetName()))
	if err := verifyAssetChecksum(context.Background(), "2.0.0", path, assetName()); err == nil {
		t.Fatal("expected a checksum mismatch error")
	}
}

// TestInstallReleaseIsFailClosed drives the whole download -> verify -> replace
// path: only a matching published checksum may touch the installed binary.
func TestInstallReleaseIsFailClosed(t *testing.T) {
	asset := []byte("#!/bin/sh\necho installed\n")
	digest := sha256.Sum256(asset)

	tests := []struct {
		name      string
		checksums string
		wantErr   bool
	}{
		{name: "matching checksum installs", checksums: checksumLine(digest[:], assetName())},
		{
			name:      "mismatched checksum aborts",
			checksums: checksumLine(bytes.Repeat([]byte{0}, sha256.Size), assetName()),
			wantErr:   true,
		},
		{name: "missing checksums file aborts", checksums: "", wantErr: true},
		{
			name:      "missing entry aborts",
			checksums: checksumLine(digest[:], "agents-sandbox-other-arch"),
			wantErr:   true,
		},
		{
			name:      "malformed entry aborts",
			checksums: "not-a-hex-digest  " + assetName() + "\n",
			wantErr:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			releaseServer(t, "2.0.0", asset, tc.checksums)
			dir := t.TempDir()
			exe := filepath.Join(dir, "agents-sandbox")
			original := []byte("old-binary")
			if err := os.WriteFile(exe, original, 0o755); err != nil {
				t.Fatal(err)
			}

			err := installRelease(context.Background(), "2.0.0", exe)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected the install to abort")
				}
			} else if err != nil {
				t.Fatalf("expected the install to proceed, got %v", err)
			}

			data, readErr := os.ReadFile(exe)
			if readErr != nil {
				t.Fatalf("read installed binary: %v", readErr)
			}
			if tc.wantErr {
				if !bytes.Equal(data, original) {
					t.Fatalf("an aborted install modified the binary: %q", data)
				}
				if leftovers := updateTempFiles(t, dir); len(leftovers) != 0 {
					t.Fatalf("an aborted install left temp files: %v", leftovers)
				}
				return
			}
			if !bytes.Equal(data, asset) {
				t.Fatalf("installed content = %q, want %q", data, asset)
			}
			if leftovers := updateTempFiles(t, dir); len(leftovers) != 0 {
				t.Fatalf("a successful install left temp files: %v", leftovers)
			}
		})
	}
}

func TestInstallReleaseDownloadFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	orig := downloadBase
	downloadBase = srv.URL
	t.Cleanup(func() { downloadBase = orig })

	dir := t.TempDir()
	exe := filepath.Join(dir, "agents-sandbox")
	if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installRelease(context.Background(), "2.0.0", exe); err == nil {
		t.Fatal("expected error when the asset download fails")
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old-binary" {
		t.Fatalf("a failed download modified the binary: %q", data)
	}
}

func TestInstallReleaseReplaceFailure(t *testing.T) {
	asset := []byte("binary")
	digest := sha256.Sum256(asset)
	releaseServer(t, "2.0.0", asset, checksumLine(digest[:], assetName()))

	dir := t.TempDir()
	exe := filepath.Join(dir, "agents-sandbox")
	if err := os.Mkdir(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installRelease(context.Background(), "2.0.0", exe); err == nil {
		t.Fatal("expected error when the replacement target is a directory")
	}
	if leftovers := updateTempFiles(t, dir); len(leftovers) != 0 {
		t.Fatalf("a failed replace left temp files: %v", leftovers)
	}
}
