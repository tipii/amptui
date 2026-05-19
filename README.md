# amptui

A terminal UI Plex client focused exclusively on music — browse your library,
queue tracks, and play them, all from the keyboard.

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), the
[plexgo](https://github.com/LukeHagar/plexgo) SDK, and [mpv](https://mpv.io/)
for audio playback.

## Status

Working today:

- [x] Connect to a Plex server (manual token auth)
- [x] Browse: libraries → artists → albums → tracks (list or grid)
- [x] Dashboard home with recent plays, recently added, recent playlists
- [x] Play via mpv: pause/resume, seek, next/prev, queue with auto-advance
- [x] Queue modal: reorder, delete, jump-to-play, track progress bar
- [x] Fuzzy search across the whole library
- [x] Artist / album info: bio, genres, similar artists (`i` modal)
- [x] Inline artwork — Kitty graphics on supported terminals, half-block fallback everywhere else
- [x] Editable settings screen, in-app keybindings modal (`?`)
- [x] Library cache (`internal/library`) as single source of truth — browse + search read from it; Plex is only touched during sync or info fetches
- [ ] Scrobble / mark played

## Requirements

- Go 1.26+
- [mpv](https://mpv.io/) on your `PATH` (playback is disabled gracefully if missing)
- A Plex Media Server with a music library, and an `X-Plex-Token`

## Build

```bash
make build     # produces ./amptui
make run       # build-and-run via `go run`
make install   # `go install` to $GOBIN / $GOPATH/bin so `amptui` is on PATH
make uninstall # remove the installed binary
make           # list all targets
```

Without `make`: `go build -o amptui ./cmd/amptui && ./amptui`, or `go run ./cmd/amptui`,
or `go install ./cmd/amptui` for a system-wide install.

## Configuration

Create `~/.config/amptui/config.toml` (see `config.example.toml`):

```toml
server_url = "http://192.168.1.10:32400"
token = "your-X-Plex-Token-here"

# Optional: skip the library picker and open straight into this library.
# Matched against a library's section key or title (case-insensitive).
default_library = "Music"

# Optional: initial render mode at each browser level — "list" or "grid".
# Editable from the settings screen (`,`).
default_view_artist = "grid"
default_view_album = "list"

# Optional: which screen amptui opens on — "library" (default) or "dashboard".
# Tab swaps between them at runtime.
home = "library"

# Optional: render inline artwork (artist / album thumbnails) in the header,
# info modal, grid cards and album list rows. Off by default; flip on to use
# the Kitty graphics protocol on supported terminals or the half-block ANSI
# fallback elsewhere. Cached to ~/.cache/amptui/img/.
images = false
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
| `tab`                 | Switch between Dashboard and Library         |
| `/`                   | Filter the current list                      |
| `i`                   | Artist / album info (bio, genres, similar)   |
| `space`               | Pause / resume                               |
| `n` / `p`             | Next / previous in queue                     |
| `<` / `>`             | Seek −10s / +10s                             |
| `,`                   | Open / close the settings screen             |
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

**Inside the settings screen:**

| Key       | Action                                                  |
| --------- | ------------------------------------------------------- |
| `j` / `k` | Move cursor between editable fields                     |
| `enter`   | Edit the highlighted field                              |
| `enter` (while editing) | Save the new value to `config.toml`       |
| `esc`     | Cancel an edit, or close the settings screen            |
| `R`       | Re-sync the library cache from Plex                     |
| `C`       | Clear the image cache (disk + terminal Kitty registry)  |

First-time setup (no credentials yet): save your server URL + token in
settings and the app builds its Plex client and kicks off the library
sync immediately — no restart needed. Re-editing those fields after a
successful startup still requires a relaunch; the running app keeps
its existing Plex client.

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
internal/library/  on-disk cache for a music section (browse + search read from here)
internal/imgcache/ on-disk thumbnail bytes + terminal-protocol detection
internal/tui/      Bubble Tea drill-down browser
```

## License

MIT
