package vm

import "testing"

// FuzzParseLegacyUpdaterState drives arbitrary bytes through the retired
// updater.yaml parser. The file is hand-editable and may be corrupted by an
// interrupted write, so a tolerant reader must never panic, and when it does
// read a flat last_checked it must fold it into the opencode agent entry that
// the migration consumes.
func FuzzParseLegacyUpdaterState(f *testing.F) {
	for _, seed := range []string{
		"",
		"last_checked: 2026-01-02T03:04:05Z\n",
		"agents:\n  opencode:\n    last_checked: 2026-01-02T03:04:05Z\n",
		"agents:\n  pi:\n    last_checked: 2026-01-02T03:04:05Z\n",
		"last_checked: 2026-01-02T03:04:05+02:00\n",
		"last_checked: not-a-time\n",
		"last_checked: [unterminated",
		"agents: []\n",
		"agents:\n  a: {last_checked: x}\n  a: {last_checked: y}\n",
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		legacy := parseLegacyUpdaterState(data)
		if legacy.LastChecked.IsZero() {
			return
		}
		opencode, ok := legacy.Agents["opencode"]
		if !ok || opencode.LastChecked.IsZero() {
			t.Fatalf("parseLegacyUpdaterState(%q) left the flat last_checked unfolded: %+v", data, legacy)
		}
	})
}
