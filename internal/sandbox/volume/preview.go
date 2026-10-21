package volume

import (
	"context"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// PreviewHomeVolume resolves, read-only, the home volume a run would use and
// whether it would have to be created. It mirrors ResolveHomeVolume's
// reuse → recover → create decision without any write: no volume is created,
// no state is repaired, and nothing is prefilled.
//
// The create branch reports an empty name: a fresh volume's name embeds a
// timestamp (HomeVolumeName), so a preview can only name a volume it would
// reuse.
func PreviewHomeVolume(ctx context.Context, client msb.Client, k state.Key, recoveredVolume string) (string, bool) {
	if st, err := state.ReadState(k); err == nil && st.HomeVolume != "" {
		if _, err := client.GetVolume(ctx, st.HomeVolume); err == nil {
			return st.HomeVolume, false
		}
	}
	if recovered, ok := confirmedRecoveredVolume(ctx, client, recoveredVolume); ok {
		return recovered, false
	}
	return "", true
}
