package naming

import "testing"

// FuzzRunnerImageRefRoundTrip checks that the launcher can read back the image
// references it writes: ParseImageTag must recover the slug and agent from a
// RunnerImageRef. Naming feeds sandbox identity, so a mis-parse silently
// targets the wrong artifact.
func FuzzRunnerImageRefRoundTrip(f *testing.F) {
	f.Add("myproject", "opencode")
	f.Add("a", "pi")
	f.Add("agents-sandbox", "opencode2")
	f.Add("my-project-2", "claude-code")
	f.Add("x--y", "a-b")

	f.Fuzz(func(t *testing.T, slug, agent string) {
		if !validArtifactPart(slug) || !validArtifactPart(agent) {
			t.Skip()
		}
		ref := RunnerImageRef(slug, agent)
		info := ParseImageTag(ref)
		if info.Slug != slug {
			t.Fatalf(
				"ParseImageTag(RunnerImageRef(%q, %q)) slug = %q, want %q (ref %q)",
				slug,
				agent,
				info.Slug,
				slug,
				ref,
			)
		}
		if info.Agent != agent {
			t.Fatalf(
				"ParseImageTag(RunnerImageRef(%q, %q)) agent = %q, want %q (ref %q)",
				slug,
				agent,
				info.Agent,
				agent,
				ref,
			)
		}
	})
}

// validArtifactPart constrains fuzzed inputs to the shape the launcher itself
// generates (lowercase alphanumerics joined by interior dashes), so the fuzzer
// explores that space rather than producing inputs the naming scheme never
// emits.
func validArtifactPart(s string) bool {
	if s == "" || len(s) > 40 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i > 0 && i < len(s)-1:
		default:
			return false
		}
	}
	return true
}
