package mpris

import (
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// newTestPair starts a Server and a separate client connection to the
// same session bus. Skips when there's no bus to talk to (CI, SSH).
func newTestPair(t *testing.T) (*Server, dbus.BusObject) {
	t.Helper()
	s, err := New(func() time.Duration { return 42 * time.Second })
	if err != nil {
		t.Skipf("no session bus: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	client, err := dbus.SessionBusPrivateNoAutoStartup()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Auth(nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Hello(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return s, client.Object(s.name, objectPath)
}

func recv(t *testing.T, s *Server) Command {
	t.Helper()
	select {
	case c := <-s.Commands():
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("no command received")
		return Command{}
	}
}

func TestMethodsBecomeCommands(t *testing.T) {
	s, obj := newTestPair(t)

	for method, want := range map[string]CommandKind{
		"Play": Play, "Pause": Pause, "PlayPause": PlayPause,
		"Stop": Stop, "Next": Next, "Previous": Previous,
	} {
		if err := obj.Call(ifacePlayer+"."+method, 0).Err; err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		if got := recv(t, s); got.Kind != want {
			t.Errorf("%s: got kind %v, want %v", method, got.Kind, want)
		}
	}

	if err := obj.Call(ifacePlayer+".Seek", 0, int64(-5_000_000)).Err; err != nil {
		t.Fatal(err)
	}
	if got := recv(t, s); got.Kind != Seek || got.Offset != -5*time.Second {
		t.Errorf("Seek: got %+v", got)
	}
}

func TestPropertiesReflectUpdate(t *testing.T) {
	s, obj := newTestPair(t)

	s.Update(Status{
		State:   Paused,
		CanNext: true,
		Track: &Track{
			ID: "abc-123", Title: "Song", Artist: "Band", Album: "Record",
			TrackNumber: 3, Length: 3 * time.Minute, ArtURL: "file:///tmp/x",
		},
	})

	var status string
	if err := obj.Call(ifaceProps+".Get", 0, ifacePlayer, "PlaybackStatus").Store(&status); err != nil {
		t.Fatal(err)
	}
	if status != "Paused" {
		t.Errorf("PlaybackStatus = %q", status)
	}

	var pos int64
	if err := obj.Call(ifaceProps+".Get", 0, ifacePlayer, "Position").Store(&pos); err != nil {
		t.Fatal(err)
	}
	if pos != 42_000_000 {
		t.Errorf("Position = %d", pos)
	}

	var all map[string]dbus.Variant
	if err := obj.Call(ifaceProps+".GetAll", 0, ifacePlayer).Store(&all); err != nil {
		t.Fatal(err)
	}
	meta := all["Metadata"].Value().(map[string]dbus.Variant)
	if got := meta["xesam:title"].Value(); got != "Song" {
		t.Errorf("title = %v", got)
	}
	if got := meta["mpris:length"].Value(); got != int64(180_000_000) {
		t.Errorf("length = %v", got)
	}
	trackID := meta["mpris:trackid"].Value().(dbus.ObjectPath)
	if !trackID.IsValid() {
		t.Errorf("trackid %q is not a valid object path", trackID)
	}
	if all["CanGoNext"].Value() != true || all["CanGoPrevious"].Value() != false {
		t.Errorf("CanGoNext/CanGoPrevious = %v/%v", all["CanGoNext"], all["CanGoPrevious"])
	}

	// SetPosition only lands when it names the current track.
	if err := obj.Call(ifacePlayer+".SetPosition", 0, dbus.ObjectPath("/stale"), int64(1)).Err; err != nil {
		t.Fatal(err)
	}
	if err := obj.Call(ifacePlayer+".SetPosition", 0, trackID, int64(10_000_000)).Err; err != nil {
		t.Fatal(err)
	}
	if got := recv(t, s); got.Kind != SetPosition || got.Position != 10*time.Second {
		t.Errorf("SetPosition: got %+v", got)
	}
	select {
	case c := <-s.Commands():
		t.Errorf("unexpected extra command %+v", c)
	default:
	}
}

func TestTrackPath(t *testing.T) {
	for _, id := range []string{"12345", "a1b2-c3d4", "ünïcode/../x", ""} {
		if p := trackPath(id); !p.IsValid() {
			t.Errorf("trackPath(%q) = %q, not a valid object path", id, p)
		}
	}
}
