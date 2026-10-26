package cliout

import (
	"encoding/json"

	"github.com/inoio/agents-sandbox/internal/sandbox/pruning"
	"github.com/inoio/agents-sandbox/internal/termio"
)

type pruneDetailJSON struct {
	Type            string `json:"type"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	Digest          string `json:"digest"`
	StaleForSeconds int64  `json:"stale_for_seconds"`
}

// PruneReportJSON is the --format json shape of a full prune report.
type PruneReportJSON struct {
	DryRun       bool              `json:"dry_run"`
	Sandboxes    int               `json:"sandboxes"`
	Volumes      int               `json:"volumes"`
	Images       int               `json:"images"`
	DockerImages int               `json:"docker_images"`
	Details      []pruneDetailJSON `json:"details"`
}

// ImageReportJSON is the --format json shape of an image prune report.
type ImageReportJSON struct {
	DryRun       bool              `json:"dry_run"`
	Images       int               `json:"images"`
	DockerImages int               `json:"docker_images"`
	Details      []pruneDetailJSON `json:"details"`
}

// VolumeReportJSON is the --format json shape of a volume prune report.
type VolumeReportJSON struct {
	DryRun  bool              `json:"dry_run"`
	Volumes int               `json:"volumes"`
	Details []pruneDetailJSON `json:"details"`
}

// SandboxReportJSON is the --format json shape of a sandbox prune report.
type SandboxReportJSON struct {
	DryRun    bool              `json:"dry_run"`
	Sandboxes int               `json:"sandboxes"`
	Details   []pruneDetailJSON `json:"details"`
}

func NewPruneReportJSON(report *pruning.StaleReport, dryRun bool) PruneReportJSON {
	return PruneReportJSON{
		DryRun:       dryRun,
		Sandboxes:    report.PrunedSandboxes,
		Volumes:      report.PrunedVolumes,
		Images:       report.PrunedSandboxImages,
		DockerImages: report.PrunedDockerImages,
		Details:      newPruneDetailsJSON(report.Details),
	}
}

func NewImageReportJSON(report pruning.ImageReport, dryRun bool) ImageReportJSON {
	return ImageReportJSON{
		DryRun:       dryRun,
		Images:       report.MSBImagesPruned,
		DockerImages: report.DockerImagesPruned,
		Details:      newPruneDetailsJSON(report.Details),
	}
}

func NewVolumeReportJSON(report pruning.VolumeReport, dryRun bool) VolumeReportJSON {
	return VolumeReportJSON{
		DryRun:  dryRun,
		Volumes: report.VolumesPruned,
		Details: newPruneDetailsJSON(report.Details),
	}
}

func NewSandboxReportJSON(report pruning.SandboxReport, dryRun bool) SandboxReportJSON {
	return SandboxReportJSON{
		DryRun:    dryRun,
		Sandboxes: report.VMsPruned,
		Details:   newPruneDetailsJSON(report.Details),
	}
}

func newPruneDetailsJSON(entries []pruning.StaleEntry) []pruneDetailJSON {
	details := make([]pruneDetailJSON, 0, len(entries))
	for _, entry := range entries {
		details = append(details, pruneDetailJSON{
			Type:            entry.Type.String(),
			Name:            entry.Name,
			Slug:            entry.Slug,
			Digest:          entry.Digest,
			StaleForSeconds: int64(entry.StaleFor.Seconds()),
		})
	}
	return details
}

// RenderPruneReport writes the JSON value under --format json, or calls the
// human renderer by default. Quiet suppresses stdout entirely (prune has no
// identifiers-only mode). The values passed by callers are concrete report
// DTOs, so the JSON marshal cannot fail.
func RenderPruneReport(ui termio.UI, mode Mode, human func(), jsonValue any) {
	if mode == ModeJSON {
		data, _ := json.MarshalIndent(jsonValue, "", "  ")
		ui.Out(string(data))
		return
	}
	if mode == ModeTable {
		human()
	}
}
