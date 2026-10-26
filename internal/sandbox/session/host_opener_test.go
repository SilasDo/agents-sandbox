package session

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// stubHostOpener swaps the host-opener seams. The watch stub blocks until its
// context is cancelled, like the real watcher, and returns report.
func stubHostOpener(t *testing.T, volumePath string, report opener.Report, watchErr error) *[]string {
	t.Helper()
	var watchedPaths []string
	originalResolve, originalWatch := resolveHomeVolumePath, watchHostOpener
	resolveHomeVolumePath = func(_ context.Context, volumeName string) (string, error) {
		if volumeName != "home-vol" {
			t.Errorf("resolved volume %q, want home-vol", volumeName)
		}
		return volumePath, nil
	}
	watchHostOpener = func(ctx context.Context, homeVolumePath string, _ msb.Sandbox) (opener.Report, error) {
		watchedPaths = append(watchedPaths, homeVolumePath)
		<-ctx.Done()
		return report, watchErr
	}
	t.Cleanup(func() { resolveHomeVolumePath, watchHostOpener = originalResolve, originalWatch })
	return &watchedPaths
}

func TestStartHostOpenerWatchesHomeVolumeAndReportsAfterStop(t *testing.T) {
	report := opener.Report{
		Refusals: []opener.Refusal{{Target: "file:///etc/passwd", Reason: "scheme not allowed"}},
		Omitted:  2,
	}
	watched := stubHostOpener(t, "/host/volumes/home-vol", report, errors.New("boom"))
	ui := &termio.Mock{}
	ses := &fakePrepared{sb: &msb.MockSandbox{}, homeVolume: "home-vol"}

	stop := startHostOpener(context.Background(), ses, true, ui)
	if len(ui.InfoCalls) != 0 {
		t.Fatalf("printed while attached: %v", ui.InfoCalls)
	}
	stop()

	if len(*watched) != 1 || (*watched)[0] != "/host/volumes/home-vol" {
		t.Fatalf("watched %v", *watched)
	}
	joined := strings.Join(ui.InfoCalls, "\n")
	if !strings.Contains(joined, "file:///etc/passwd (scheme not allowed)") || !strings.Contains(joined, "2 more") {
		t.Fatalf("info calls = %v", ui.InfoCalls)
	}
	if !strings.Contains(strings.Join(ui.VerboseCalls, "\n"), "boom") {
		t.Fatalf("watch error not logged: %v", ui.VerboseCalls)
	}
}

func TestStartHostOpenerSkipsWhenDisabledOrWithoutVM(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		enabled bool
		ses     *fakePrepared
	}{
		{"disabled", false, &fakePrepared{sb: &msb.MockSandbox{}, homeVolume: "home-vol"}},
		{"no sandbox", true, &fakePrepared{homeVolume: "home-vol"}},
		{"no home volume", true, &fakePrepared{sb: &msb.MockSandbox{}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			watched := stubHostOpener(t, "/host/volumes/home-vol", opener.Report{}, nil)
			startHostOpener(context.Background(), testCase.ses, testCase.enabled, &termio.Mock{})()
			if len(*watched) != 0 {
				t.Fatalf("watcher started: %v", *watched)
			}
		})
	}
}

func TestStartHostOpenerSkipsWhenVolumePathUnavailable(t *testing.T) {
	watched := stubHostOpener(t, "", opener.Report{}, nil)
	ui := &termio.Mock{}
	startHostOpener(context.Background(), &fakePrepared{sb: &msb.MockSandbox{}, homeVolume: "home-vol"}, true, ui)()
	if len(*watched) != 0 {
		t.Fatalf("watcher started: %v", *watched)
	}
	if !strings.Contains(strings.Join(ui.VerboseCalls, "\n"), "home volume path unavailable") {
		t.Fatalf("verbose calls = %v", ui.VerboseCalls)
	}
}

func TestResolveHomeVolumePathUsesVolumeHandle(t *testing.T) {
	client := &msb.MockMsbClient{}
	client.GetVolumeFn = func(_ context.Context, name string) (msb.VolumeHandle, error) {
		return &msb.MockVolumeHandle{Name_: name, Path_: "/host/" + name}, nil
	}
	msb.WithMsbMock(t, client)
	got, err := resolveHomeVolumePath(context.Background(), "home-vol")
	if err != nil || got != "/host/home-vol" {
		t.Fatalf("got %q, %v", got, err)
	}
}

// TestShellAndRunServeHostOpenerWhileAttached covers the wiring: both session
// kinds start the host opener for the session's home volume when open-links is
// on, and stop it when the session ends.
func TestShellAndRunServeHostOpenerWhileAttached(t *testing.T) {
	for _, session := range []struct {
		name  string
		start func(context.Context, options.RunOptions, termio.UI) error
	}{{"shell", Shell}, {"run", Run}} {
		t.Run(session.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			watched := stubHostOpener(t, "/host/volumes/home-vol", opener.Report{}, nil)
			sb := &msb.MockSandbox{AttachCode: 0, Name_: "test-vm"}
			sb.ShellOut = map[string]msb.ShellResult{
				"curl -sf http://127.0.0.1:4096/session/status": msb.NewTestResult(true, 0, `{}`, "", nil),
			}
			sb.ExecOut = map[string]msb.ShellResult{"sleep 1h": msb.NewTestResult(true, 0, "", "", nil)}
			original := prepareSandbox
			prepareSandbox = func(context.Context, options.RunOptions, termio.UI) (preparedSandbox, error) {
				return &fakePrepared{sb: sb, target: "/workspace", homeVolume: "home-vol"}, nil
			}
			t.Cleanup(func() { prepareSandbox = original })

			opts := options.RunOptions{OpenLinks: true, ReapPolicy: options.NewReapPolicy(true, 5)}
			if err := session.start(context.Background(), opts, &termio.Mock{}); err != nil {
				t.Fatalf("%s: %v", session.name, err)
			}
			if len(*watched) != 1 {
				t.Fatalf("host opener started %d times, want 1", len(*watched))
			}
		})
	}
}
