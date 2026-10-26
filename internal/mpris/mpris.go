// Package mpris exposes amptui on the D-Bus session bus as an MPRIS
// media player (org.mpris.MediaPlayer2), so the desktop's media keys,
// playerctl, status bars and notification widgets can see what's
// playing and drive play / pause / next / previous / seek.
//
// The server doesn't touch the player itself. Requests from the bus are
// queued on Commands() for the UI loop to apply — the queue lives in
// the TUI model, so that's the only place next/prev can be resolved
// correctly — and the UI pushes what it's showing back with Update.
// Position is read live through the callback passed to New, since MPRIS
// clients poll it rather than receive change signals for it.
package mpris

import (
	"cmp"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

const (
	busName     = "org.mpris.MediaPlayer2.amptui"
	objectPath  = dbus.ObjectPath("/org/mpris/MediaPlayer2")
	ifaceRoot   = "org.mpris.MediaPlayer2"
	ifacePlayer = "org.mpris.MediaPlayer2.Player"
	ifaceProps  = "org.freedesktop.DBus.Properties"

	// noTrack is the spec's sentinel trackid for "nothing loaded".
	noTrack = dbus.ObjectPath("/org/mpris/MediaPlayer2/TrackList/NoTrack")
)

// CommandKind is the action a bus client asked for.
type CommandKind int

const (
	Play CommandKind = iota
	Pause
	PlayPause
	Stop
	Next
	Previous
	// Seek moves by Command.Offset relative to the current position.
	Seek
	// SetPosition moves to the absolute Command.Position. The server
	// has already checked that the request targets the current track.
	SetPosition
	// SetVolume sets the volume to Command.Volume (0–1).
	SetVolume
	// SetShuffle turns shuffle on or off (Command.Shuffle).
	SetShuffle
	// SetLoop sets the repeat mode (Command.Loop).
	SetLoop
	Quit
)

// LoopStatus mirrors MPRIS's LoopStatus values.
type LoopStatus string

const (
	LoopNone     LoopStatus = "None"
	LoopTrack    LoopStatus = "Track"
	LoopPlaylist LoopStatus = "Playlist"
)

// Command is one request from a bus client.
type Command struct {
	Kind     CommandKind
	Offset   time.Duration
	Position time.Duration
	Volume   float64
	Shuffle  bool
	Loop     LoopStatus
}

// PlaybackState mirrors MPRIS's PlaybackStatus values.
type PlaybackState string

const (
	Playing PlaybackState = "Playing"
	Paused  PlaybackState = "Paused"
	Stopped PlaybackState = "Stopped"
)

// Track is the metadata published for the current track.
type Track struct {
	// ID is any stable identifier for the track (the backend's
	// RatingKey); it's sanitized into an object path for mpris:trackid.
	ID          string
	Title       string
	Artist      string
	Album       string
	TrackNumber int
	Length      time.Duration
	// ArtURL is a file:// or http(s):// URL for the cover, or empty.
	ArtURL string
}

// Status is everything the UI publishes. A nil Track means stopped.
type Status struct {
	State   PlaybackState
	Track   *Track
	CanNext bool
	CanPrev bool
	// Volume is 0–1 (MPRIS reports a muted player as 0).
	Volume  float64
	Shuffle bool
	Loop    LoopStatus
}

// Server is a registered MPRIS player on the session bus.
type Server struct {
	conn     *dbus.Conn
	name     string // the bus name claimed in New
	cmds     chan Command
	position func() time.Duration

	mu    sync.Mutex
	props map[string]map[string]dbus.Variant // iface -> name -> value
}

// New connects to the session bus and claims an MPRIS name. position is
// called whenever a client reads the Position property. It fails when
// there's no session bus (headless / SSH), which callers should treat
// as "no media controls" rather than fatal.
func New(position func() time.Duration) (*Server, error) {
	// NoAutoStartup: with no running bus (SSH, a bare TTY) godbus would
	// otherwise dbus-launch a fresh daemon nothing else is listening on.
	conn, err := dbus.SessionBusPrivateNoAutoStartup()
	if err != nil {
		return nil, fmt.Errorf("connecting to session bus: %w", err)
	}
	if err := conn.Auth(nil); err != nil {
		conn.Close()
		return nil, fmt.Errorf("authenticating to session bus: %w", err)
	}
	if err := conn.Hello(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("registering on session bus: %w", err)
	}
	s := &Server{
		conn:     conn,
		cmds:     make(chan Command, 16),
		position: position,
	}
	s.props = map[string]map[string]dbus.Variant{
		ifaceRoot: {
			"CanQuit":             dbus.MakeVariant(true),
			"CanRaise":            dbus.MakeVariant(false),
			"HasTrackList":        dbus.MakeVariant(false),
			"Identity":            dbus.MakeVariant("amptui"),
			"SupportedUriSchemes": dbus.MakeVariant([]string{}),
			"SupportedMimeTypes":  dbus.MakeVariant([]string{}),
		},
		ifacePlayer: s.playerProps(Status{State: Stopped}),
	}

	exports := []struct {
		v       any
		iface   string
		renames map[string]string
	}{
		{rootObj{s}, ifaceRoot, nil},
		{playerObj{s}, ifacePlayer, map[string]string{"SeekBy": "Seek"}},
		{propsObj{s}, ifaceProps, nil},
		{introspect.Introspectable(introspectXML), "org.freedesktop.DBus.Introspectable", nil},
	}
	for _, e := range exports {
		if err := conn.ExportWithMap(e.v, e.renames, objectPath, e.iface); err != nil {
			conn.Close()
			return nil, fmt.Errorf("exporting %s: %w", e.iface, err)
		}
	}

	// The spec's convention for a second instance is a ".instance<pid>"
	// suffix, so two amptuis both stay controllable.
	for _, name := range []string{busName, fmt.Sprintf("%s.instance%d", busName, os.Getpid())} {
		reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("requesting %s: %w", name, err)
		}
		if reply == dbus.RequestNameReplyPrimaryOwner {
			s.name = name
			return s, nil
		}
	}
	conn.Close()
	return nil, fmt.Errorf("could not claim an MPRIS bus name")
}

// Commands delivers requests from bus clients.
func (s *Server) Commands() <-chan Command { return s.cmds }

// Close releases the bus name and disconnects.
func (s *Server) Close() error { return s.conn.Close() }

// Update publishes st, emitting PropertiesChanged for whatever differs
// from the last published status. Cheap to call on every tick.
func (s *Server) Update(st Status) {
	next := s.playerProps(st)

	s.mu.Lock()
	cur := s.props[ifacePlayer]
	changed := map[string]dbus.Variant{}
	for k, v := range next {
		if old, ok := cur[k]; !ok || !reflect.DeepEqual(old.Value(), v.Value()) {
			changed[k] = v
			cur[k] = v
		}
	}
	s.mu.Unlock()

	if len(changed) > 0 {
		_ = s.conn.Emit(objectPath, ifaceProps+".PropertiesChanged",
			ifacePlayer, changed, []string{})
	}
}

// Seeked tells clients the position jumped (MPRIS doesn't signal
// ordinary position progress, only discontinuities).
func (s *Server) Seeked(pos time.Duration) {
	_ = s.conn.Emit(objectPath, ifacePlayer+".Seeked", pos.Microseconds())
}

// send queues a command without ever blocking the bus goroutine; a
// burst beyond the buffer means the UI is wedged, and dropping is
// better than stalling D-Bus dispatch.
func (s *Server) send(c Command) {
	select {
	case s.cmds <- c:
	default:
	}
}

// playerProps renders st as the Player interface's property set
// (minus Position, which is read live).
func (s *Server) playerProps(st Status) map[string]dbus.Variant {
	meta := map[string]dbus.Variant{"mpris:trackid": dbus.MakeVariant(noTrack)}
	if t := st.Track; t != nil {
		meta = map[string]dbus.Variant{
			"mpris:trackid": dbus.MakeVariant(trackPath(t.ID)),
			"mpris:length":  dbus.MakeVariant(t.Length.Microseconds()),
			"xesam:title":   dbus.MakeVariant(t.Title),
			"xesam:album":   dbus.MakeVariant(t.Album),
		}
		if t.Artist != "" {
			meta["xesam:artist"] = dbus.MakeVariant([]string{t.Artist})
		}
		if t.TrackNumber > 0 {
			meta["xesam:trackNumber"] = dbus.MakeVariant(int32(t.TrackNumber))
		}
		if t.ArtURL != "" {
			meta["mpris:artUrl"] = dbus.MakeVariant(t.ArtURL)
		}
	}
	loaded := st.Track != nil
	state := st.State
	if state == "" || !loaded {
		state = Stopped
	}
	return map[string]dbus.Variant{
		"PlaybackStatus": dbus.MakeVariant(string(state)),
		"Metadata":       dbus.MakeVariant(meta),
		"Rate":           dbus.MakeVariant(1.0),
		"MinimumRate":    dbus.MakeVariant(1.0),
		"MaximumRate":    dbus.MakeVariant(1.0),
		"Volume":         dbus.MakeVariant(st.Volume),
		"Shuffle":        dbus.MakeVariant(st.Shuffle),
		"LoopStatus":     dbus.MakeVariant(string(cmp.Or(st.Loop, LoopNone))),
		"CanGoNext":      dbus.MakeVariant(st.CanNext),
		"CanGoPrevious":  dbus.MakeVariant(st.CanPrev),
		"CanPlay":        dbus.MakeVariant(loaded),
		"CanPause":       dbus.MakeVariant(loaded),
		"CanSeek":        dbus.MakeVariant(loaded),
		"CanControl":     dbus.MakeVariant(true),
	}
}

// trackPath turns an arbitrary ID into a valid object path segment
// (only [A-Za-z0-9_] are allowed).
func trackPath(id string) dbus.ObjectPath {
	var b strings.Builder
	for _, r := range id {
		if r < 128 && (r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return noTrack
	}
	return dbus.ObjectPath("/org/amptui/track/t" + b.String())
}

// rootObj implements org.mpris.MediaPlayer2.
type rootObj struct{ s *Server }

func (o rootObj) Raise() *dbus.Error { return nil }
func (o rootObj) Quit() *dbus.Error  { o.s.send(Command{Kind: Quit}); return nil }

// playerObj implements org.mpris.MediaPlayer2.Player.
type playerObj struct{ s *Server }

func (o playerObj) Play() *dbus.Error      { o.s.send(Command{Kind: Play}); return nil }
func (o playerObj) Pause() *dbus.Error     { o.s.send(Command{Kind: Pause}); return nil }
func (o playerObj) PlayPause() *dbus.Error { o.s.send(Command{Kind: PlayPause}); return nil }
func (o playerObj) Stop() *dbus.Error      { o.s.send(Command{Kind: Stop}); return nil }
func (o playerObj) Next() *dbus.Error      { o.s.send(Command{Kind: Next}); return nil }
func (o playerObj) Previous() *dbus.Error  { o.s.send(Command{Kind: Previous}); return nil }

// SeekBy is exported to D-Bus as Seek (see New); the Go name avoids
// clashing with io.Seeker's signature.
func (o playerObj) SeekBy(offsetUs int64) *dbus.Error {
	o.s.send(Command{Kind: Seek, Offset: time.Duration(offsetUs) * time.Microsecond})
	return nil
}

// SetPosition is ignored unless trackID names the current track, per
// the spec — it guards against a stale request landing on a new track.
func (o playerObj) SetPosition(trackID dbus.ObjectPath, posUs int64) *dbus.Error {
	o.s.mu.Lock()
	meta, _ := o.s.props[ifacePlayer]["Metadata"].Value().(map[string]dbus.Variant)
	o.s.mu.Unlock()
	if cur, _ := meta["mpris:trackid"].Value().(dbus.ObjectPath); cur != trackID || cur == noTrack || posUs < 0 {
		return nil
	}
	o.s.send(Command{Kind: SetPosition, Position: time.Duration(posUs) * time.Microsecond})
	return nil
}

func (o playerObj) OpenUri(string) *dbus.Error {
	return dbus.MakeFailedError(fmt.Errorf("amptui does not open URIs"))
}

// propsObj implements org.freedesktop.DBus.Properties over s.props.
type propsObj struct{ s *Server }

func (o propsObj) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	if iface == ifacePlayer && name == "Position" {
		return dbus.MakeVariant(o.s.position().Microseconds()), nil
	}
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	v, ok := o.s.props[iface][name]
	if !ok {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty",
			[]any{fmt.Sprintf("unknown property %s.%s", iface, name)})
	}
	return v, nil
}

func (o propsObj) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	o.s.mu.Lock()
	out := make(map[string]dbus.Variant, len(o.s.props[iface])+1)
	for k, v := range o.s.props[iface] {
		out[k] = v
	}
	o.s.mu.Unlock()
	if iface == ifacePlayer {
		out["Position"] = dbus.MakeVariant(o.s.position().Microseconds())
	}
	return out, nil
}

// Set handles the writable Player properties by queueing a command; the
// new value is published back through Update once it's applied.
func (o propsObj) Set(iface, name string, v dbus.Variant) *dbus.Error {
	if iface == ifacePlayer {
		switch name {
		case "Volume":
			vol, ok := v.Value().(float64)
			if !ok {
				return dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", []any{"Volume must be a double"})
			}
			o.s.send(Command{Kind: SetVolume, Volume: min(max(vol, 0), 1)})
			return nil
		case "Shuffle":
			on, ok := v.Value().(bool)
			if !ok {
				return dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", []any{"Shuffle must be a boolean"})
			}
			o.s.send(Command{Kind: SetShuffle, Shuffle: on})
			return nil
		case "LoopStatus":
			ls, _ := v.Value().(string)
			switch LoopStatus(ls) {
			case LoopNone, LoopTrack, LoopPlaylist:
				o.s.send(Command{Kind: SetLoop, Loop: LoopStatus(ls)})
				return nil
			}
			return dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", []any{"LoopStatus must be None, Track or Playlist"})
		}
	}
	return dbus.NewError("org.freedesktop.DBus.Error.PropertyReadOnly",
		[]any{fmt.Sprintf("%s.%s is read-only", iface, name)})
}

const introspectXML = `<node>
  <interface name="org.mpris.MediaPlayer2">
    <method name="Raise"/>
    <method name="Quit"/>
    <property name="CanQuit" type="b" access="read"/>
    <property name="CanRaise" type="b" access="read"/>
    <property name="HasTrackList" type="b" access="read"/>
    <property name="Identity" type="s" access="read"/>
    <property name="SupportedUriSchemes" type="as" access="read"/>
    <property name="SupportedMimeTypes" type="as" access="read"/>
  </interface>
  <interface name="org.mpris.MediaPlayer2.Player">
    <method name="Next"/>
    <method name="Previous"/>
    <method name="Pause"/>
    <method name="PlayPause"/>
    <method name="Stop"/>
    <method name="Play"/>
    <method name="Seek"><arg name="Offset" type="x" direction="in"/></method>
    <method name="SetPosition">
      <arg name="TrackId" type="o" direction="in"/>
      <arg name="Position" type="x" direction="in"/>
    </method>
    <method name="OpenUri"><arg name="Uri" type="s" direction="in"/></method>
    <signal name="Seeked"><arg name="Position" type="x"/></signal>
    <property name="PlaybackStatus" type="s" access="read"/>
    <property name="Rate" type="d" access="read"/>
    <property name="Metadata" type="a{sv}" access="read"/>
    <property name="Volume" type="d" access="readwrite"/>
    <property name="Shuffle" type="b" access="readwrite"/>
    <property name="LoopStatus" type="s" access="readwrite"/>
    <property name="Position" type="x" access="read"/>
    <property name="MinimumRate" type="d" access="read"/>
    <property name="MaximumRate" type="d" access="read"/>
    <property name="CanGoNext" type="b" access="read"/>
    <property name="CanGoPrevious" type="b" access="read"/>
    <property name="CanPlay" type="b" access="read"/>
    <property name="CanPause" type="b" access="read"/>
    <property name="CanSeek" type="b" access="read"/>
    <property name="CanControl" type="b" access="read"/>
  </interface>
  <interface name="org.freedesktop.DBus.Properties">
    <method name="Get">
      <arg name="interface" type="s" direction="in"/>
      <arg name="property" type="s" direction="in"/>
      <arg name="value" type="v" direction="out"/>
    </method>
    <method name="GetAll">
      <arg name="interface" type="s" direction="in"/>
      <arg name="properties" type="a{sv}" direction="out"/>
    </method>
    <method name="Set">
      <arg name="interface" type="s" direction="in"/>
      <arg name="property" type="s" direction="in"/>
      <arg name="value" type="v" direction="in"/>
    </method>
    <signal name="PropertiesChanged">
      <arg name="interface" type="s"/>
      <arg name="changed_properties" type="a{sv}"/>
      <arg name="invalidated_properties" type="as"/>
    </signal>
  </interface>
  <interface name="org.freedesktop.DBus.Introspectable">
    <method name="Introspect"><arg name="data" type="s" direction="out"/></method>
  </interface>
</node>`
