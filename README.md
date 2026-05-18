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
- [x] Play queue: add track/album, auto-advance, queue modal
- [x] Queue: next/prev skip, reorder, delete, jump-to-play
- [x] In-app keybindings modal (`?`)
- [x] Library cache (`internal/library`) as single source of truth — browse + search read from it; Plex is only touched during sync
- [ ] Scrobble / mark played

## Requirements

- Go 1.26+
- [mpv](https://mpv.io/) on your `PATH` (playback is disabled gracefully if missing)
- A Plex Media Server with a music library, and an `X-Plex-Token`

## Build

```bash
make build   # produces ./amptui
make run     # build-and-run via `go run`
make         # list all targets (build / run / test / vet / tidy / clean)
```

Without `make`: `go build -o amptui ./cmd/amptui && ./amptui`, or `go run ./cmd/amptui`.

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

Press `?` in the app for an in-TUI keybindings modal.

| Key                   | Action                                       |
| --------------------- | -------------------------------------------- |
| `enter` / `→` / `l`   | Open selected item / play track              |
| `esc` / `←` / `h`     | Go back                                      |
| `↑` / `↓` / `j` / `k` | Move selection                               |
| `tab`                 | Toggle list / grid view (Artists, Albums)    |
| `/`                   | Filter the current list                      |
| `space`               | Pause / resume                               |
| `n` / `p`             | Next / previous in queue                     |
| `<` / `>`             | Seek −10s / +10s                             |
| `,`                   | Open the settings screen                     |
| `q` / `Q`             | Add highlighted track / whole album to queue |
| `o`                   | Open / close the queue modal                 |
| `s`                   | Open the fuzzy search modal                  |
| `?`                   | Open / close the keybindings modal           |
| `R`                   | Re-sync the library cache from Plex          |
| `ctrl+c` / `ctrl+q`   | Quit                                         |

**Inside the queue modal:**

| Key         | Action                                     |
| ----------- | ------------------------------------------ |
| `j` / `k`   | Move cursor                                |
| `J` / `K`   | Reorder highlighted track down / up        |
| `d`         | Delete highlighted track                   |
| `enter`     | Jump playback to highlighted track         |
| `o` / `esc` | Close                                      |

**Inside the search modal:**

| Key         | Action                                          |
| ----------- | ----------------------------------------------- |
| (type)      | Fuzzy search across the whole library           |
| `tab`       | Cycle filter: All / Artists / Albums / Songs    |
| `↑` / `↓`   | Move cursor through results                     |
| `enter`     | Play (track) or jump into (artist/album)        |
| `alt+enter` | Append highlighted track to the queue           |
| `esc`       | Close                                           |

The library cache is built on first launch (~8s for a 9k-track library),
persisted to `~/.cache/amptui/<sectionUUID>.json`, and invalidated when
Plex's section `contentChangedAt` counter advances. Every subsequent
browse and search reads from this cache — Plex is only contacted during
sync. While syncing, a small spinner appears on the right of the status
bar; the browser opens into the cache once the sync finishes.

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
