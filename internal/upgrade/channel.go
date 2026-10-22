package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
)

// Channel selects which releases the launcher is willing to offer itself.
type Channel string

const (
	// ChannelStable follows GitHub's "latest" release, which never serves a
	// prerelease.
	ChannelStable Channel = "stable"
	// ChannelCanary follows the newest release eligible for the canary pool.
	ChannelCanary Channel = "canary"
)

// canaryIdentifier is the semver prerelease identifier a canary tag carries.
const canaryIdentifier = "canary"

// ParseChannel maps a channel name to its Channel, returning an error for
// unknown values. It is case-insensitive and trims surrounding whitespace.
func ParseChannel(s string) (Channel, error) {
	ch := Channel(strings.ToLower(strings.TrimSpace(s)))
	switch ch {
	case ChannelStable, ChannelCanary:
		return ch, nil
	default:
		return "", fmt.Errorf("invalid upgrade channel %q (want stable or canary)", s)
	}
}

// latestForChannel resolves the newest release version (leading "v" stripped)
// for the channel. The zero value and ChannelStable both mean stable.
func latestForChannel(ctx context.Context, ch Channel) (string, error) {
	if ch == ChannelCanary {
		return latestCanaryRelease(ctx)
	}
	return latestRelease(ctx)
}

// canaryEligible reports whether a release tag may be offered on the canary
// channel: a final release, or a prerelease whose first semver prerelease
// identifier is "canary". Anything else (a plain -alpha/-rc prerelease, an
// unparsable tag) is offered to no channel.
func canaryEligible(tag string) bool {
	v, err := semver.NewVersion(strings.TrimPrefix(tag, "v"))
	if err != nil {
		return false
	}
	prerelease := v.Prerelease()
	if prerelease == "" {
		return true
	}
	identifier, _, _ := strings.Cut(prerelease, ".")
	return identifier == canaryIdentifier
}

// githubRelease is the slice of a GitHub release object that channel
// resolution needs.
type githubRelease struct {
	TagName string `json:"tag_name"`
	Draft   bool   `json:"draft"`
}

// latestCanaryRelease returns the newest canary-eligible release version
// (leading "v" stripped).
//
// The newest eligible release is expected within the first page: the API
// returns releases newest-creation-first and a canary number only ever
// increases, so every later canary is created after the one before it.
func latestCanaryRelease(ctx context.Context) (string, error) {
	url := releasesURL + "?per_page=100"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build releases request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "agents-sandbox")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("list agents-sandbox releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("list agents-sandbox releases: unexpected status %d", resp.StatusCode)
	}
	var releases []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", fmt.Errorf("decode agents-sandbox releases: %w", err)
	}
	var newest *semver.Version
	newestTag := ""
	for _, release := range releases {
		if release.Draft || !canaryEligible(release.TagName) {
			continue
		}
		v, err := semver.NewVersion(strings.TrimPrefix(release.TagName, "v"))
		if err != nil {
			continue
		}
		if newest == nil || v.GreaterThan(newest) {
			newest = v
			newestTag = release.TagName
		}
	}
	if newest == nil {
		return "", errors.New("no canary release found")
	}
	return strings.TrimPrefix(newestTag, "v"), nil
}
