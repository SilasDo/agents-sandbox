package mounts

import (
	"path/filepath"
	"testing"
)

// FuzzResolveMountTarget drives arbitrary guest paths from a user's `mounts:`
// config through the validation that decides which host directories land where.
// A target the function accepts must never equal or shadow a managed mount
// (/workspace, /tmp, the sandbox home): a regression there lets a configured
// mount hide the workspace or the tmpfs. Accepted targets are also cleaned, so
// re-validating the result must be a no-op.
func FuzzResolveMountTarget(f *testing.F) {
	for _, seed := range []string{
		"", "/", "/workspace", "/workspace/x", "/workspace/..", "/tmp", "/tmp/x",
		"/home/dev", "/home/dev/.m2", "/home", "/mnt/data", "relative", "//workspace",
		"/workspace/../etc", "/tmp/../workspace", "./x", "/home/devx", "/foo/./bar",
		"/foo/bar/", "/foo//bar", "/foo/bar/../baz",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, target string) {
		clean, err := resolveMountTarget(target)
		if err != nil {
			return
		}
		if !filepath.IsAbs(clean) || clean == "/" {
			t.Fatalf("resolveMountTarget(%q) = %q, want an absolute non-root path", target, clean)
		}
		if filepath.Clean(clean) != clean {
			t.Fatalf("resolveMountTarget(%q) = %q, want a cleaned target", target, clean)
		}
		for _, managed := range managedMountTargets {
			if clean == managed || isWithin(managed, clean) {
				t.Fatalf("resolveMountTarget(%q) = %q collides with managed mount %q", target, clean, managed)
			}
		}
		for _, exclusive := range exclusiveMountTargets {
			if isWithin(clean, exclusive) {
				t.Fatalf("resolveMountTarget(%q) = %q would shadow managed mount %q", target, clean, exclusive)
			}
		}
		if again, err := resolveMountTarget(clean); err != nil || again != clean {
			t.Fatalf("resolveMountTarget(%q) = %q is not a fixed point: got %q, %v", target, clean, again, err)
		}
	})
}
