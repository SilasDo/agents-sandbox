package pruning

import (
	"context"
	"sync"
	"time"

	"github.com/inoio/agents-sandbox/internal/termio"
)

var autoPruneOnce sync.Once

// autoPruneDefaultThreshold is the stale-age threshold used when AutoPrune is
// called with a zero threshold.
var autoPruneDefaultThreshold = 30 * 24 * time.Hour

// AutoPrune runs the prune logic once per process with the given threshold.
// Threshold of 0 defaults to autoPruneDefaultThreshold.
func AutoPrune(ctx context.Context, threshold time.Duration, dryRun bool, ui termio.UI) {
	if threshold == 0 {
		threshold = autoPruneDefaultThreshold
	}
	autoPruneOnce.Do(func() {
		report, err := Prune(ctx, threshold, dryRun, ui)
		if report != nil {
			RenderPruneSummary(ui, report, dryRun)
		}
		if err != nil {
			ui.Warnf("auto-prune failed: %s", err)
		}
	})
}
