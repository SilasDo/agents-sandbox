package reconfig

import (
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
)

func TestPlanReconfigServeOnly(t *testing.T) {
	desired := options.ServeOnlyBindings(options.ServeOnlyBasePort)
	tests := []struct {
		name         string
		serveOnly    bool
		cfgPorts     []msbSdk.PortBinding
		wantRecreate bool
		wantLabel    string
	}{
		{"serve on, vm not published", true, nil, true, "published port(s)"},
		{"serve on, vm already published", true, desired, false, ""},
		{"serve off, vm published", false, desired, true, "published port(s)"},
		{"serve off, vm not published", false, nil, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg *msbSdk.SandboxConfig
			if tt.cfgPorts == nil {
				// A running VM always has a non-nil SandboxConfig; PortBindings
				// is simply nil/empty when nothing is published.
				cfg = &msbSdk.SandboxConfig{PortBindings: nil}
			} else {
				cfg = &msbSdk.SandboxConfig{PortBindings: tt.cfgPorts}
			}
			plan := PlanReconfig(
				cfg,
				"img",
				options.RunOptions{ServeOnly: tt.serveOnly},
				ChangeFlags{},
				"",
			)
			if plan.Recreate != tt.wantRecreate {
				t.Errorf("Recreate = %v, want %v", plan.Recreate, tt.wantRecreate)
			}
			if tt.wantLabel != "" {
				found := false
				for _, c := range plan.Changes {
					if c.Label == tt.wantLabel {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected change label %q, got %+v", tt.wantLabel, plan.Changes)
				}
			}
		})
	}
}

func TestPlanReconfigTriggersRecreateOnImageChange(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "agents-sandbox/runner-proj:oldhash"}

	planSame := PlanReconfig(
		cfg,
		"agents-sandbox/runner-proj:oldhash",
		options.RunOptions{},
		ChangeFlags{},
		"",
	)
	if planSame.Recreate {
		t.Error("expected no recreate when image reference is unchanged")
	}

	planNew := PlanReconfig(
		cfg,
		"agents-sandbox/runner-proj:newhash",
		options.RunOptions{},
		ChangeFlags{},
		"",
	)
	if !planNew.Recreate {
		t.Error("expected recreate when image reference changes after a rebuild")
	}
	if len(planNew.Changes) == 0 || planNew.Changes[0].Label != "image" {
		t.Errorf("expected an 'image' change label, got %+v", planNew.Changes)
	}
}

// TestPlanReconfigToleratesLegacyImageRef covers Review Focus 1's "VM not
// recreated": a VM booted from the legacy prefixed microsandbox reference must
// not be recreated just because the launcher now computes the prefix-free one.
func TestPlanReconfigToleratesLegacyImageRef(t *testing.T) {
	const legacyRef = "agents-sandbox/runner-proj-1mjusbm3wikhb0:opencode-latest"
	cfg := &msbSdk.SandboxConfig{Image: legacyRef}

	cases := []struct {
		ref          string
		wantRecreate bool
	}{
		{"proj-1mjusbm3wikhb0:opencode-latest", false}, // prefix-free spelling of the same image
		{legacyRef, false},                                                 // identical
		{"proj-1mjusbm3wikhb0:pi-latest", true},                            // different agent
		{"agents-sandbox/runner-proj-1mjusbm3wikhb0:4e8oeqohbmalok", true}, // pinned digest ref
		{"proj-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31", false},           // content ref, same image
		// prefixed content ref, same image
		{"agents-sandbox/runner-proj-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31", false},
		{"proj-1mjusbm3wikhb0:pi.9f2c1a7b4d6e0f31", true}, // content ref, different agent
	}
	for _, tc := range cases {
		plan := PlanReconfig(cfg, tc.ref, options.RunOptions{}, ChangeFlags{}, "")
		if plan.Recreate != tc.wantRecreate {
			t.Errorf("PlanReconfig(%q) recreate = %v, want %v", tc.ref, plan.Recreate, tc.wantRecreate)
		}
	}
}

// TestPlanReconfigToleratesContentImageRef covers the upgrade from a VM that was
// booted from a content ref: it is not recreated when the launcher resolves the
// same slug+agent under the name or legacy prefixed spelling, but IS recreated
// for a different content id (a genuine image switch whose persisted digest may
// already match — the home action records the new digest before the VM is
// replaced), a different agent, or a digest-pinned ref.
func TestPlanReconfigToleratesContentImageRef(t *testing.T) {
	const contentRef = "proj-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31"
	cfg := &msbSdk.SandboxConfig{Image: contentRef}

	cases := []struct {
		ref          string
		wantRecreate bool
	}{
		{contentRef, false},                            // identical content ref
		{"proj-1mjusbm3wikhb0:opencode-latest", false}, // its prefix-free name ref
		// its legacy prefixed name ref
		{"agents-sandbox/runner-proj-1mjusbm3wikhb0:opencode-latest", false},
		{"proj-1mjusbm3wikhb0:pi.9f2c1a7b4d6e0f31", true}, // different agent
		{"proj-1mjusbm3wikhb0:4e8oeqohbmalok", true},      // digest-pinned ref
		// different content id: a real image switch, not a spelling change
		{"proj-1mjusbm3wikhb0:opencode.4b8d0e6f2a1c3b57", true},
		// prefixed spelling of a different content id
		{"agents-sandbox/runner-proj-1mjusbm3wikhb0:opencode.4b8d0e6f2a1c3b57", true},
	}
	for _, tc := range cases {
		plan := PlanReconfig(cfg, tc.ref, options.RunOptions{}, ChangeFlags{}, "")
		if plan.Recreate != tc.wantRecreate {
			t.Errorf("PlanReconfig(%q) recreate = %v, want %v", tc.ref, plan.Recreate, tc.wantRecreate)
		}
	}
}

func TestDesiredPublishBindingsDelegatesToOptions(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{PortBindings: []msbSdk.PortBinding{
		{Bind: "127.0.0.1", HostPort: 4097, GuestPort: 4096, Protocol: msbSdk.PortProtocolTCP},
	}}
	optsBindings := options.ServeOnlyBindings(4097)
	planBindings := desiredPublishBindings(true, cfg)
	if len(planBindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(planBindings))
	}
	if planBindings[0].Bind != optsBindings[0].Bind {
		t.Errorf("Bind = %q, want %q", planBindings[0].Bind, optsBindings[0].Bind)
	}
	if planBindings[0].HostPort != optsBindings[0].HostPort {
		t.Errorf("HostPort = %d, want %d", planBindings[0].HostPort, optsBindings[0].HostPort)
	}
	if planBindings[0].GuestPort != optsBindings[0].GuestPort {
		t.Errorf("GuestPort = %d, want %d", planBindings[0].GuestPort, optsBindings[0].GuestPort)
	}
	if planBindings[0].Protocol != optsBindings[0].Protocol {
		t.Errorf("Protocol = %v, want %v", planBindings[0].Protocol, optsBindings[0].Protocol)
	}
}

func TestDesiredPublishBindingsNilWhenNotServeOnly(t *testing.T) {
	got := desiredPublishBindings(false, nil)
	if got != nil {
		t.Errorf("expected nil when serveOnly=false, got %+v", got)
	}
}

func TestPlanReconfigNetworkChangeTriggersRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{}
	opts := options.RunOptions{}
	plan := PlanReconfig(cfg, "img", opts, ChangeFlags{Network: true}, "")
	if !plan.Recreate {
		t.Fatal("expected Recreate when network policy changes")
	}
}

func TestPlanReconfigNetworkSameNoRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{}
	opts := options.RunOptions{}
	plan := PlanReconfig(cfg, "img", opts, ChangeFlags{}, "")
	if plan.Recreate {
		t.Fatal("expected no Recreate when network policy is unchanged")
	}
}

func TestPlanReconfigHomeVolumeChangeTriggersRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image: "img",
		Volumes: map[string]msbSdk.MountConfig{
			mounts.VMHomeDir: {Named: "agents-sandbox-home-proj-old"},
		},
	}
	plan := PlanReconfig(cfg, "img", options.RunOptions{}, ChangeFlags{},
		"agents-sandbox-home-proj-new")
	if !plan.Recreate {
		t.Fatal("expected Recreate when the desired home volume differs from the mounted one")
	}
	found := false
	for _, c := range plan.Changes {
		if c.Label == "home volume" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a 'home volume' change label, got %+v", plan.Changes)
	}
}

func TestPlanReconfigHomeVolumeSameNoRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image: "img",
		Volumes: map[string]msbSdk.MountConfig{
			mounts.VMHomeDir: {Named: "agents-sandbox-home-proj-vol"},
		},
	}
	plan := PlanReconfig(cfg, "img", options.RunOptions{}, ChangeFlags{},
		"agents-sandbox-home-proj-vol")
	if plan.Recreate {
		t.Fatal("expected no Recreate when the mounted home volume matches the desired one")
	}
}

func TestPlanReconfigNilCfgHomeVolumeSafe(t *testing.T) {
	plan := PlanReconfig(nil, "img", options.RunOptions{}, ChangeFlags{},
		"agents-sandbox-home-proj-vol")
	if plan.Recreate {
		t.Fatal("expected no Recreate for a nil config (fresh VM creation)")
	}
}

func TestPlanReconfigMountsChangeTriggersRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{}
	plan := PlanReconfig(cfg, "img", options.RunOptions{}, ChangeFlags{Mounts: true}, "")
	if !plan.Recreate {
		t.Fatal("expected Recreate when host bind mounts change")
	}
	found := false
	for _, c := range plan.Changes {
		if c.Label == changeLabelBindMounts {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a %q change label, got %+v", changeLabelBindMounts, plan.Changes)
	}
}

// TestPlanReconfigMountsUnchangedNoRecreate guards the regression where the
// plan was derived from cfg.Volumes, which the SDK never populates when
// reading an existing VM back: every run would then rebuild the VM.
func TestPlanReconfigMountsUnchangedNoRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{}
	opts := options.RunOptions{Mounts: mounts.Mounts{
		"/home/dev/.m2": {Source: "/host/.m2"},
	}}
	plan := PlanReconfig(cfg, "img", opts, ChangeFlags{}, "")
	if plan.Recreate {
		t.Fatalf("expected no Recreate for unchanged mounts, got %+v", plan.Changes)
	}
}

func TestFormatSizeSpecEmptyRaw(t *testing.T) {
	cases := []struct {
		valueMiB uint32
		raw      string
		want     string
	}{
		{0, "", "0M"},
		{1024, "", "1024M"},
		{4096, "", "4096M"},
		{2048, "2G", "2G"},
		{2048, "3G", "2048M"},
	}
	for _, tc := range cases {
		if got := FormatSizeSpec(tc.valueMiB, tc.raw); got != tc.want {
			t.Errorf("FormatSizeSpec(%d, %q) = %q, want %q", tc.valueMiB, tc.raw, got, tc.want)
		}
	}
}

// TestConfigChangeList pins both display forms: the "label: old → new" line for
// a change carrying both values, and the bare label for anything else. A
// one-sided change must take the bare form.
func TestConfigChangeList(t *testing.T) {
	got := configChangeList([]Change{
		{Label: "image", Old: "a", New: "b"},
		{Label: "new-only", New: "b"},
		{Label: "old-only", Old: "a"},
		{Label: "label-only"},
	})
	want := "Project VM config changed:\n" +
		"  - image: a → b\n" +
		"  - new-only\n" +
		"  - old-only\n" +
		"  - label-only"
	if got != want {
		t.Errorf("configChangeList() = %q, want %q", got, want)
	}
	if got := configChangeList(nil); got != "Project VM config changed:" {
		t.Errorf("configChangeList(nil) = %q", got)
	}
}

func TestImageRecreateEmptyImageRef(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "old:1"}
	if imageRecreate(cfg, "", false) {
		t.Error("an empty desired imageRef must not trigger a recreate")
	}
	if !imageRecreate(cfg, "new:2", false) {
		t.Error("a differing imageRef must trigger a recreate")
	}
	if !imageRecreate(cfg, "", true) {
		t.Error("the Image flag alone must trigger a recreate")
	}
}

func TestImageRecreateForeignRefs(t *testing.T) {
	const foreign = "docker.io/library/foreign:1"
	cfg := &msbSdk.SandboxConfig{Image: foreign}
	if imageRecreate(cfg, foreign, false) {
		t.Error("an identical foreign image reference must not trigger a recreate")
	}
	if !imageRecreate(cfg, "docker.io/library/other:1", false) {
		t.Error("two different foreign image references must trigger a recreate")
	}
}

func TestTmpSizeChangeMissingVolume(t *testing.T) {
	if _, changed := tmpSizeChange(&msbSdk.SandboxConfig{}, options.RunOptions{TmpSize: "4G"}); changed {
		t.Error("a requested tmp size with no mounted /tmp volume must not be a change")
	}
}

func TestTmpSizeChangeInvalidSpec(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Volumes: map[string]msbSdk.MountConfig{mounts.TmpMountPath: {SizeMiB: 2048}},
	}
	if _, changed := tmpSizeChange(cfg, options.RunOptions{TmpSize: "not-a-size"}); changed {
		t.Error("an unparseable tmp size must not be a change")
	}
	if _, changed := tmpSizeChange(cfg, options.RunOptions{}); changed {
		t.Error("an unset tmp size must not be a change")
	}
}

func TestWorkspaceQuotaChangeReturnValue(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Volumes: map[string]msbSdk.MountConfig{mounts.WorkspaceMountPath: {QuotaMiB: 1024}},
	}
	change, changed := workspaceQuotaChange(cfg, options.RunOptions{WorkspaceQuota: "32G"})
	if !changed {
		t.Fatal("expected a change for a different quota")
	}
	if change.Label != "/workspace write quota" || change.Old != "1024M" || change.New != "32G" {
		t.Errorf("workspaceQuotaChange() = %+v", change)
	}
}

func TestHomeVolumeChangeEmptyRequested(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Volumes: map[string]msbSdk.MountConfig{mounts.VMHomeDir: {Named: "vol"}},
	}
	if homeVolumeChange(cfg, "") {
		t.Error("an empty desired home volume must not be a change")
	}
	if homeVolumeChange(&msbSdk.SandboxConfig{}, "vol") {
		t.Error("a missing mounted home volume must not be a change")
	}
	if !homeVolumeChange(cfg, "other") {
		t.Error("a differing home volume must be a change")
	}
}

func TestRootDiskChangeOldValue(t *testing.T) {
	disk := msbSdk.RootDisk.Managed(8192)
	cfg := &msbSdk.SandboxConfig{RootDisk: &disk}
	change, changed := rootDiskChange(cfg, options.RunOptions{DiskSize: "16G"})
	if !changed {
		t.Fatal("expected a change for a different root disk")
	}
	if change.Label != "root disk size" || change.Old != "8192M" || change.New != "16G" {
		t.Errorf("rootDiskChange() = %+v", change)
	}
	if _, changed := rootDiskChange(&msbSdk.SandboxConfig{}, options.RunOptions{DiskSize: "16G"}); !changed {
		t.Error("a nil root disk with a requested size must be a change")
	}
}

func TestPublishReconfigHostPort(t *testing.T) {
	if hostPort, changed := publishReconfig(options.RunOptions{}, &msbSdk.SandboxConfig{}); hostPort != 0 || changed {
		t.Errorf("not serve-only: hostPort=%d changed=%v, want 0 false", hostPort, changed)
	}
	cfg := &msbSdk.SandboxConfig{PortBindings: options.ServeOnlyBindings(4097)}
	if hostPort, changed := publishReconfig(options.RunOptions{ServeOnly: true}, cfg); hostPort != 4097 || changed {
		t.Errorf("serve-only reuse: hostPort=%d changed=%v, want 4097 false", hostPort, changed)
	}
}

func TestResourceModificationStagesSingleCPU(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{CPUs: 4, MaxCPUs: 8}
	mo := resourceModification(cfg, options.RunOptions{CPUs: 1})
	if mo == nil || mo.CPUs != 1 {
		t.Fatalf("expected a staged CPU=1 modification, got %+v", mo)
	}
}

func TestResourceModificationMaxCPUsOne(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{CPUs: 0, MaxCPUs: 1}
	mo := resourceModification(cfg, options.RunOptions{CPUs: 2})
	if mo == nil || mo.CPUs != 1 {
		t.Fatalf("expected CPU clamped to 1, got %+v", mo)
	}
}

func TestPlanReconfigAgentConfigSuppressedByRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "old:1", CPUs: 4, MemoryMiB: 4096}
	plan := PlanReconfig(cfg, "new:2", options.RunOptions{}, ChangeFlags{AgentConfig: true}, "")
	if !plan.Recreate {
		t.Fatal("expected a recreate on the image change")
	}
	if plan.RestartDaemons {
		t.Error("agent config must not restart daemons when the VM is recreated anyway")
	}
	for _, c := range plan.Changes {
		if c.Label == "agent config" {
			t.Errorf("agent config should not be a change line when recreating: %+v", plan.Changes)
		}
	}
}

func TestResourceModificationMemoryNoMax(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{MemoryMiB: 1024, MaxMemoryMiB: 0}
	mo := resourceModification(cfg, options.RunOptions{Memory: "2G"})
	if mo == nil {
		t.Fatal("expected a staged memory modification when the boot max is unknown")
	}
	if mo.MemoryMiB != 2048 {
		t.Errorf("MemoryMiB = %d, want 2048", mo.MemoryMiB)
	}
	if mo.Policy != msbSdk.ModificationPolicyNoRestart {
		t.Errorf("Policy = %v, want no-restart", mo.Policy)
	}
}

func TestResourceModificationMemoryBelowMax(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{MemoryMiB: 1024, MaxMemoryMiB: 8192}
	mo := resourceModification(cfg, options.RunOptions{Memory: "2G"})
	if mo == nil || mo.MemoryMiB != 2048 {
		t.Fatalf("expected memory 2048 (below the max), got %+v", mo)
	}
}

func TestResourceModificationMemoryClampAtOneMiB(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{MemoryMiB: 0, MaxMemoryMiB: 1}
	mo := resourceModification(cfg, options.RunOptions{Memory: "2G"})
	if mo == nil || mo.MemoryMiB != 1 {
		t.Fatalf("expected memory clamped to 1, got %+v", mo)
	}
}

func TestResourceModificationNilWhenUnchanged(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{CPUs: 4, MemoryMiB: 4096, MaxCPUs: 8, MaxMemoryMiB: 8192}
	if mo := resourceModification(cfg, options.RunOptions{CPUs: 4, Memory: "4G"}); mo != nil {
		t.Errorf("expected nil when nothing changes, got %+v", mo)
	}
}

// TestPlanReconfigRecreateSuppressesEnvChange pins that env/secret changes fold
// into an already-decided recreate: they must not be appended as extra change
// lines, which would misreport why the VM is rebuilt.
func TestPlanReconfigRecreateSuppressesEnvChange(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "old:1", CPUs: 4, MemoryMiB: 4096}
	plan := PlanReconfig(cfg, "new:2", options.RunOptions{}, ChangeFlags{Env: true, Secrets: true}, "")
	if !plan.Recreate {
		t.Fatal("expected a recreate on the image change")
	}
	for _, c := range plan.Changes {
		if c.Label == "environment variables" || c.Label == "secrets" {
			t.Errorf("env/secret change should not be listed when recreating anyway: %+v", plan.Changes)
		}
	}
}

func TestPlanReconfigAgentConfigAppendsChange(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "a", CPUs: 4, MemoryMiB: 4096}
	plan := PlanReconfig(cfg, "a", options.RunOptions{}, ChangeFlags{AgentConfig: true}, "")
	if !plan.RestartDaemons {
		t.Fatal("expected a daemon restart for an agent config change")
	}
	found := false
	for _, c := range plan.Changes {
		if c.Label == "agent config" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an 'agent config' change line, got %+v", plan.Changes)
	}
}
