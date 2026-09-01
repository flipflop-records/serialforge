package device

import (
	"testing"
	"time"
)

func withFixedClock(t *testing.T, start time.Time) func() {
	t.Helper()
	tick := start
	orig := timeNow
	timeNow = func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}
	return func() { timeNow = orig }
}

func TestRecentStoreTouchAndOrder(t *testing.T) {
	defer withFixedClock(t, time.Unix(1000, 0))()

	dir := t.TempDir()
	s, err := LoadRecent(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Touch("/tmp/a")
	s.Touch("/tmp/b")
	s.Touch("/tmp/c")

	got := s.All()
	want := []string{"/tmp/c", "/tmp/b", "/tmp/a"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Path != w {
			t.Errorf("entry %d = %q, want %q", i, got[i].Path, w)
		}
	}
}

func TestRecentStoreTouchDeduplicatesAndMovesToFront(t *testing.T) {
	defer withFixedClock(t, time.Unix(1000, 0))()

	dir := t.TempDir()
	s, _ := LoadRecent(dir)
	s.Touch("/tmp/a")
	s.Touch("/tmp/b")
	s.Touch("/tmp/a") // re-touch: should move to front, not duplicate

	got := s.All()
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2 (deduplicated): %+v", len(got), got)
	}
	if got[0].Path != "/tmp/a" {
		t.Errorf("most recent = %q, want /tmp/a", got[0].Path)
	}
}

func TestRecentStoreBoundedSize(t *testing.T) {
	defer withFixedClock(t, time.Unix(1000, 0))()

	dir := t.TempDir()
	s, _ := LoadRecent(dir)
	for i := 0; i < maxRecentEndpoints+5; i++ {
		s.Touch(string(rune('a' + i)))
	}
	got := s.All()
	if len(got) != maxRecentEndpoints {
		t.Fatalf("got %d entries, want the bounded max %d", len(got), maxRecentEndpoints)
	}
	// Most-recently-touched entries survive; oldest are evicted.
	if got[0].Path != string(rune('a'+maxRecentEndpoints+4)) {
		t.Errorf("most recent entry = %q, want the last one touched", got[0].Path)
	}
}

func TestRecentStoreRemove(t *testing.T) {
	dir := t.TempDir()
	s, _ := LoadRecent(dir)
	s.Touch("/tmp/a")
	s.Touch("/tmp/b")
	if !s.Remove("/tmp/a") {
		t.Fatal("Remove should report true for an existing entry")
	}
	if s.Remove("/tmp/a") {
		t.Fatal("Remove should report false the second time (already gone)")
	}
	got := s.All()
	if len(got) != 1 || got[0].Path != "/tmp/b" {
		t.Errorf("got %+v, want only /tmp/b remaining", got)
	}
}

func TestRecentStoreSaveAndReload(t *testing.T) {
	defer withFixedClock(t, time.Unix(1000, 0))()

	dir := t.TempDir()
	s, err := LoadRecent(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Touch("/tmp/serialforge-a")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := LoadRecent(dir)
	if err != nil {
		t.Fatalf("LoadRecent (reload): %v", err)
	}
	got := reloaded.All()
	if len(got) != 1 || got[0].Path != "/tmp/serialforge-a" {
		t.Fatalf("reloaded = %+v, want [/tmp/serialforge-a]", got)
	}
	if got[0].LastUsed.IsZero() {
		t.Error("LastUsed should survive the round trip, not be zero")
	}
}

func TestRecentStoreEmptyByDefault(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadRecent(dir)
	if err != nil {
		t.Fatalf("LoadRecent on a fresh dir: %v", err)
	}
	if len(s.All()) != 0 {
		t.Errorf("fresh store should be empty, got %+v", s.All())
	}
}
