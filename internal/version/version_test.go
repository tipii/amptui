package version

import "testing"

func TestStampedVersionWins(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	for stamped, want := range map[string]string{"v0.4.0": "0.4.0", "0.4.0": "0.4.0", "v1.0.0-rc.1": "1.0.0-rc.1"} {
		Version = stamped
		if got := String(); got != want {
			t.Errorf("Version %q: String() = %q, want %q", stamped, got, want)
		}
	}
}

func TestUnstampedVersionIsNeverEmpty(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = ""
	if String() == "" {
		t.Error("String() is empty without a stamp")
	}
}
