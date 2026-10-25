package volume

import (
	"context"
	"fmt"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// Mount point constants used by volume operations (prefill, copy, edit).
const (
	homeMount = "/mnt/home"
	srcMount  = "/src"
	dstMount  = "/dst"
)

// HomeVolumeName generates a prefix-free volume name for the given
// project/agent key with a UTC timestamp.
func HomeVolumeName(k state.Key) string {
	ts := time.Now().UTC().Format("20060102T150405")
	return naming.HomeVolumeName(k.Slug, k.Agent, ts)
}

// Manager manages home-volume lifecycle for projects.
type Manager struct {
	ui termio.UI
}

// NewManager returns a volume manager backed by the given UI.
func NewManager(ui termio.UI) *Manager {
	return &Manager{ui: ui}
}

// PrefillVolume builds a throwaway sandbox from the given image, mounts the
// target home volume at /mnt/home, and copies the image's home directory onto it.
// The throwaway sandbox is stopped and removed afterwards.
func (vm *Manager) PrefillVolume(
	ctx context.Context,
	client msb.Client,
	k state.Key, volumeName string,
	info image.ImageInfo,
	ui termio.UI,
) error {
	return withThrowawaySandbox(ctx, client, k, info, throwawaySandbox{
		purpose: "prefill",
		mounts: map[string]msbSdk.MountConfig{
			homeMount: msbSdk.Mount.Named(volumeName, msbSdk.MountOptions{}),
		},
		spinnerLabel: "Preparing home volume",
	}, ui, func(sb msb.Sandbox, spin termio.Spinner) error {
		return runShell(ctx, sb, spin, shellStep{
			execErrPrefix: "prefill cp",
			failurePrefix: "prefill cp failed",
			script:        "cp -a /home/dev/. " + homeMount + "/ && chown -R dev:dev " + homeMount,
		})
	})
}

// ApplyHomeAction executes the migrate/reset action the user chose, always
// keeping the old volume. It returns the home volume to mount for this run.
//
// State is only written when the action actually executes successfully; a real
// run creates the new volume, prefills it (spawning a VM), copies files for
// migrate (spawning another VM), and then records the new volume and current
// digest. Both --dry-run and --dry-run-vm simulate the action without changing
// state: --dry-run performs no writes at all, and --dry-run-vm additionally
// never spawns a VM, so the chosen action is left uncommitted.
func (vm *Manager) ApplyHomeAction(
	ctx context.Context,
	client msb.Client,
	k state.Key, oldVolume string,
	info image.ImageInfo,
	action VolumeAction,
	opts options.RunOptions,
	ui termio.UI,
) (string, error) {
	if action == ActionKeep {
		if opts.DryRun {
			return oldVolume, nil
		}
		if err := vm.RecordHomeImage(k, info.Digest, ui); err != nil {
			ui.Warnf("failed to record image digest: %v", err)
		}
		return oldVolume, nil
	}

	label := actionLabel(action)
	if opts.DryRun {
		ui.Infof("dry-run: Would %s home volume (old %q kept)", label, oldVolume)
		return oldVolume, nil
	}
	if opts.DryRunVM {
		ui.Infof("dry-run-vm: %s deferred; not applied without running a VM (old %q kept)", label, oldVolume)
		return oldVolume, nil
	}

	newVol, err := client.CreateVolume(ctx, HomeVolumeName(k),
		msbSdk.WithVolumeKind(msbSdk.VolumeKindDir),
	)
	if err != nil {
		return "", fmt.Errorf("create volume: %w", err)
	}
	newName := newVol.Name()

	if err := vm.PrefillVolume(ctx, client, k, newName, info, ui); err != nil {
		vm.cleanupVolume(ctx, client, newName, ui)
		return "", err
	}

	if action == ActionMigrate {
		if err := vm.CopyVolume(ctx, client, k, oldVolume, newName, info, ui); err != nil {
			vm.cleanupVolume(ctx, client, newName, ui)
			return "", err
		}
	}

	// Change only the home volume identity and digest, keeping every other field
	// (env/secret/network/mount fingerprints and the upgrade state) that a home
	// action does not affect: dropping them would trigger a false VM rebuild or
	// re-fire the upgrade prompt on the next startup (see
	// EnvChanged/SecretsChanged/NetworkChanged/MountsChanged treating empty
	// state as "changed"). The write runs under the home lock but is guarded by a
	// compare-and-set on the resolved home volume, since the msb I/O above ran
	// without the lock: if another writer changed home_volume in the meantime,
	// abort and drop the new volume rather than clobber it.
	if err := state.WithHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		if cur.HomeVolume != oldVolume {
			return false, fmt.Errorf(
				"home volume changed concurrently (want %q, have %q)",
				oldVolume,
				cur.HomeVolume,
			)
		}
		cur.HomeVolume = newName
		cur.ImageDigest = info.Digest
		return true, nil
	}); err != nil {
		vm.cleanupVolume(ctx, client, newName, ui)
		return "", err
	}
	ui.Infof("%s to new home volume %q (old %q kept)", label, newName, oldVolume)
	return newName, nil
}

// cleanupVolume removes the given volume best-effort after a failed operation
// that created it, so an uncommitted volume does not linger as an orphan.
func (vm *Manager) cleanupVolume(ctx context.Context, client msb.Client, name string, ui termio.UI) {
	if err := client.RemoveVolume(ctx, name); err != nil {
		ui.Warnf("failed to remove new volume %q after error: %v", name, err)
	}
}

// CopyVolume copies the contents of the old home volume on top of the newly
// created one via a throwaway sandbox that mounts both.
func (vm *Manager) CopyVolume(
	ctx context.Context,
	client msb.Client,
	k state.Key, oldVolume, newVolume string,
	info image.ImageInfo,
	ui termio.UI,
) error {
	return withThrowawaySandbox(ctx, client, k, info, throwawaySandbox{
		purpose: "copy",
		mounts: map[string]msbSdk.MountConfig{
			srcMount: msbSdk.Mount.Named(oldVolume, msbSdk.MountOptions{}),
			dstMount: msbSdk.Mount.Named(newVolume, msbSdk.MountOptions{}),
		},
		// The copy spinner starts only once the sandbox is up.
		spinnerLabel: "",
	}, ui, func(sb msb.Sandbox, _ termio.Spinner) error {
		spin := ui.Spinner("Copying files from existing home volume")
		return runShell(ctx, sb, spin, shellStep{
			execErrPrefix: "copy files",
			failurePrefix: "copy failed",
			script:        "cp -a " + srcMount + "/. " + dstMount + "/ && chown -R dev:dev " + dstMount,
		})
	})
}
