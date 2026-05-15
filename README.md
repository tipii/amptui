# amptui

A terminal UI Plex client focused exclusively on music — browse your library,
queue tracks, and play them, all from the keyboard.

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), the
[plexgo](https://github.com/LukeHagar/plexgo) SDK, and [mpv](https://mpv.io/)
for audio playback.

## Status

Early development. Working today:

- [x] Connect to a Plex server (manual token auth)
- [x] Browse: libraries → artists → albums → tracks
- [x] Play a track via mpv (pause/resume, seek)
- [x] Play queue: add track/album, auto-advance, queue view
- [ ] Queue: next/prev skip, reorder, remove
- [ ] Search
- [ ] Scrobble / mark played

## Requirements

- Go 1.26+
- [mpv](https://mpv.io/) on your `PATH` (playback is disabled gracefully if missing)
- A Plex Media Server with a music library, and an `X-Plex-Token`

## Build

```bash
go build -o amptui ./cmd/amptui
./amptui
```

Or run directly: `go run ./cmd/amptui`

## Configuration

Create `~/.config/amptui/config.toml` (see `config.example.toml`):

```toml
server_url = "http://192.168.1.10:32400"
token = "your-X-Plex-Token-here"

# Optional: skip the library picker and open straight into this library.
# Matched against a library's section key or title (case-insensitive).
default_library = "Music"
```

All values can also be supplied via environment variables, which override the
config file:

```bash
export AMPTUI_SERVER_URL="http://192.168.1.10:32400"
export AMPTUI_TOKEN="your-X-Plex-Token-here"
export AMPTUI_DEFAULT_LIBRARY="Music"
```

Finding your token: see Plex's guide,
[Finding an authentication token](https://support.plex.tv/articles/204059436-finding-an-authentication-token-x-plex-token/).

## Keybindings

| Key                   | Action                                    |
| --------------------- | ----------------------------------------- |
| `enter` / `→` / `l`   | Open selected item / play track            |
| `esc` / `←` / `h`     | Go back                                    |
| `↑` / `↓` / `j` / `k` | Move selection                             |
| `space`               | Pause / resume                             |
| `,` / `.`             | Seek −10s / +10s                           |
| `q`                   | Add highlighted track to the queue         |
| `Q`                   | Add the whole album to the queue           |
| `o`                   | Open / close the queue view                |
| `/`                   | Filter the current list                    |
| `ctrl+c` / `ctrl+q`   | Quit                                       |

## Project layout

```
cmd/amptui/        entrypoint: config → connect → launch UI
internal/config/   TOML + env config loading
internal/plex/     Plex API client (plexgo SDK + raw-HTTP fallback)
internal/player/   mpv subprocess driven over its JSON IPC socket
internal/tui/      Bubble Tea drill-down browser
```

## License

MIT
