package upgrade

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// serveReleases serves the release-list API from a JSON body.
func serveReleases(t *testing.T, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	orig := releasesURL
	releasesURL = srv.URL + "/releases"
	t.Cleanup(func() { releasesURL = orig })
}

func TestParseChannel(t *testing.T) {
	tests := []struct {
		in      string
		want    Channel
		wantErr bool
	}{
		{in: "stable", want: ChannelStable},
		{in: "canary", want: ChannelCanary},
		{in: "CANARY", want: ChannelCanary},
		{in: " canary ", want: ChannelCanary},
		{in: "bogus", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range tests {
		got, err := ParseChannel(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseChannel(%q): expected error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseChannel(%q): unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseChannel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCanaryEligible(t *testing.T) {
	tests := map[string]bool{
		"v1.9.0":           true,
		"1.9.0":            true,
		"v1.9.0-canary.1":  true,
		"v1.9.0-canary.10": true,
		"v2.0.0-alpha.1":   false,
		"v1.9.0-rc.1":      false,
		"v1.9.0-beta":      false,
		"nightly":          false,
		"v":                false,
	}
	for tag, want := range tests {
		if got := canaryEligible(tag); got != want {
			t.Errorf("canaryEligible(%q) = %v, want %v", tag, got, want)
		}
	}
}

func TestLatestForChannelCanaryPicksNewest(t *testing.T) {
	serveReleases(t, `[
		{"tag_name":"v2.0.0-alpha.1","draft":false},
		{"tag_name":"v1.9.0-canary.2","draft":false},
		{"tag_name":"v1.9.0-canary.10","draft":false},
		{"tag_name":"v1.8.0","draft":false},
		{"tag_name":"v1.9.0-canary.11","draft":true}
	]`)
	got, err := latestForChannel(context.Background(), ChannelCanary)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.9.0-canary.10" {
		t.Fatalf("latestForChannel(canary) = %q, want 1.9.0-canary.10", got)
	}
}

func TestLatestForChannelCanarySkipsUnusable(t *testing.T) {
	serveReleases(t, `[
		{"tag_name":"not-a-version","draft":false},
		{"tag_name":"v1.9.0-canary.1","draft":false}
	]`)
	got, err := latestForChannel(context.Background(), ChannelCanary)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.9.0-canary.1" {
		t.Fatalf("got %q, want 1.9.0-canary.1", got)
	}
}

func TestLatestForChannelCanaryNoEligible(t *testing.T) {
	serveReleases(t, `[{"tag_name":"v2.0.0-alpha.1","draft":false}]`)
	if _, err := latestForChannel(context.Background(), ChannelCanary); err == nil {
		t.Fatal("expected an error when no release is canary-eligible")
	}
}

func TestLatestForChannelCanaryRollsForwardToRelease(t *testing.T) {
	// Once the real release ships it outranks its own canaries, so the canary
	// pool moves onto it with no special rule.
	serveReleases(t, `[
		{"tag_name":"v1.9.0-canary.3","draft":false},
		{"tag_name":"v1.9.0","draft":false}
	]`)
	got, err := latestForChannel(context.Background(), ChannelCanary)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.9.0" {
		t.Fatalf("got %q, want 1.9.0", got)
	}
}

func TestLatestForChannelStableUsesLatest(t *testing.T) {
	serveLatest(t, "1.8.0", nil)
	serveReleases(t, `[{"tag_name":"v1.9.0-canary.1","draft":false}]`)
	for _, ch := range []Channel{ChannelStable, ""} {
		got, err := latestForChannel(context.Background(), ch)
		if err != nil {
			t.Fatalf("channel %q: unexpected error: %v", ch, err)
		}
		if got != "1.8.0" {
			t.Fatalf("channel %q = %q, want 1.8.0 (the stable release)", ch, got)
		}
	}
}
