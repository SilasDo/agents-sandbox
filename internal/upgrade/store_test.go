package upgrade

import "testing"

func TestMemoryStoreRoundTrips(t *testing.T) {
	var s memoryStore
	got, err := s.Load()
	if err != nil || !got.LastChecked.IsZero() || len(got.DismissedVersions) != 0 {
		t.Fatalf("empty load = %+v, %v", got, err)
	}
	want := LauncherState{DismissedVersions: []string{"2.0.0"}}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err = s.Load()
	if err != nil || len(got.DismissedVersions) != 1 || got.DismissedVersions[0] != "2.0.0" {
		t.Fatalf("round-trip = %+v, %v", got, err)
	}
}
