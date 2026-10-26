package session

import (
	"context"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// resolveHomeVolumePath returns the host directory backing a home volume. It is
// a test seam; production asks microsandbox for the volume's path.
var resolveHomeVolumePath = func(ctx context.Context, volumeName string) (string, error) {
	handle, err := msb.Get().GetVolume(ctx, volumeName)
	if err != nil {
		return "", fmt.Errorf("look up home volume %q: %w", volumeName, err)
	}
	return handle.Path(), nil
}

// watchHostOpener serves the in-VM xdg-open shim's spool until ctx is done. It
// is a test seam; production runs an opener.Watcher with the host handler.
var watchHostOpener = func(ctx context.Context, homeVolumePath string, _ msb.Sandbox) (opener.Report, error) {
	watcher := &opener.Watcher{
		HomeVolumePath: homeVolumePath,
		Handle:         opener.NewHostHandler(opener.OpenOnHost),
	}
	return watcher.Watch(ctx)
}

// startHostOpener runs the host side of the in-VM xdg-open shim for the
// session and returns a stop function. Like the notify watcher, it runs only
// while this launcher is attached; requests made while no launcher is attached
// time out in the shim. Nothing is printed while the TUI is attached: stop
// reports refused targets afterwards, when the terminal is the user's again.
func startHostOpener(ctx context.Context, ses preparedSandbox, enabled bool, ui termio.UI) func() {
	sb := ses.Sandbox()
	if !enabled || sb == nil || ses.HomeVolume() == "" {
		ui.Verbosef("not starting host opener, enabled %v, sandbox nil %v", enabled, sb == nil)
		return func() {}
	}
	homeVolumePath, err := resolveHomeVolumePath(ctx, ses.HomeVolume())
	if err != nil || homeVolumePath == "" {
		ui.Verbosef("not starting host opener: home volume path unavailable: %v", err)
		return func() {}
	}
	watchContext, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var report opener.Report
	var watchErr error
	go func() {
		defer close(done)
		report, watchErr = watchHostOpener(watchContext, homeVolumePath, sb)
	}()
	return func() {
		cancel()
		<-done
		reportHostOpener(report, watchErr, ui)
	}
}

// reportHostOpener lists what the host opener refused during the session, so
// the user can open those targets by hand.
func reportHostOpener(report opener.Report, watchErr error, ui termio.UI) {
	if watchErr != nil {
		ui.Verbosef("host opener: %v", watchErr)
	}
	for _, refusal := range report.Refusals {
		ui.Infof("not opened on the host: %s (%s)", refusal.Target, refusal.Reason)
	}
	if report.Omitted > 0 {
		ui.Infof("not opened on the host: %d more", report.Omitted)
	}
}
