package view

import (
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/pruning"
)

func TestNewPruneReportJSONMapsCountsAndDetails(t *testing.T) {
	report := &pruning.StaleReport{
		PrunedSandboxes: 1, PrunedVolumes: 2, PrunedSandboxImages: 3, PrunedDockerImages: 4,
		Details: []pruning.StaleEntry{{Name: "vm", Slug: "s", Digest: "d", StaleFor: 90 * time.Second}},
	}
	got := NewPruneReportJSON(report, true)
	if !got.DryRun || got.Sandboxes != 1 || got.Volumes != 2 || got.Images != 3 || got.DockerImages != 4 {
		t.Errorf("counts = %+v", got)
	}
	if len(got.Details) != 1 || got.Details[0].StaleForSeconds != 90 || got.Details[0].Name != "vm" {
		t.Errorf("details = %+v", got.Details)
	}
}

func TestGroupReportJSONsKeepEmptyDetailsAsArray(t *testing.T) {
	values := []any{
		NewImageReportJSON(pruning.ImageReport{MSBImagesPruned: 1}, false),
		NewVolumeReportJSON(pruning.VolumeReport{VolumesPruned: 1}, false),
		NewSandboxReportJSON(pruning.SandboxReport{VMsPruned: 1}, false),
	}
	for _, value := range values {
		if got := marshal(t, value); !strings.Contains(got, `"details":[]`) {
			t.Errorf("empty details must marshal as [], got %s", got)
		}
	}
}
