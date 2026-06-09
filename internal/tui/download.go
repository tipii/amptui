package tui

import (
	"context"
	"fmt"
	"net/http"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tipii/amptui/internal/downloader"
	"github.com/tipii/amptui/internal/media"
)

// downloadStatusMsg updates the footer's download status line. done=true
// also clears the in-flight gate, so a fresh `d` is accepted.
type downloadStatusMsg struct {
	text string
	err  bool
	done bool
}

// downloadTimeoutPerTrack caps each track's GET; the parent context lives
// for downloadTimeoutPerTrack * (N+1) so large albums don't time out
// halfway through.
const downloadTimeoutPerTrack = 5 * time.Minute

// handleDownload routes the `d` key. It picks tracks based on what's
// under the cursor (single track, "play album" row, or an album row in
// the album list) and kicks off a background download. No-ops with a
// short hint when the folder is unset, another download is in flight, or
// no playable item is highlighted.
func (m Model) handleDownload() (Model, tea.Cmd) {
	if m.cfg.DownloadFolder == "" {
		m.downloadStatus = "set a download folder in settings (,)"
		m.downloadErr = true
		return m, nil
	}
	if m.downloading {
		m.downloadStatus = "download already in progress"
		m.downloadErr = true
		return m, nil
	}
	if m.client == nil {
		return m, nil
	}

	tracks, label := m.downloadSelection()
	if len(tracks) == 0 {
		m.downloadStatus = "press d on a track or album"
		m.downloadErr = true
		return m, nil
	}

	m.downloading = true
	m.downloadErr = false
	if len(tracks) == 1 {
		m.downloadStatus = "downloading " + label + "…"
	} else {
		m.downloadStatus = fmt.Sprintf("downloading %s (%d tracks)…", label, len(tracks))
	}
	return m, downloadCmd(tracks, m.client, m.cfg.DownloadFolder)
}

// downloadSelection resolves what's under the cursor into a track list +
// a human label. albumItem looks tracks up in the library cache (cursor
// is on an album row but the user hasn't drilled in yet).
func (m Model) downloadSelection() (tracks []media.Track, label string) {
	switch it := m.list.SelectedItem().(type) {
	case trackItem:
		return []media.Track{it.track}, it.track.Title
	case albumActionItem:
		t := it.tracks
		if len(t) > 0 {
			return t, t[0].Album
		}
		return nil, ""
	case albumItem:
		if m.library == nil {
			return nil, it.album.Title
		}
		return m.library.Tracks(it.album.RatingKey), it.album.Title
	}
	return nil, ""
}

// downloadCmd downloads tracks sequentially via the backend's StreamURL
// (auth is baked in by the backend) and reports a single summary on
// completion. Per-track errors are accumulated as the first error +
// continue — partial success is preferred over abort.
func downloadCmd(tracks []media.Track, client media.Backend, folder string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(
			context.Background(),
			downloadTimeoutPerTrack*time.Duration(len(tracks)+1),
		)
		defer cancel()
		hc := &http.Client{Timeout: downloadTimeoutPerTrack}

		var written, skipped int
		var firstErr error
		for _, t := range tracks {
			res, err := downloader.Download(ctx, hc, client.StreamURL(t), t, folder)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if res.Skipped {
				skipped++
			} else {
				written++
			}
		}

		out := downloadStatusMsg{done: true}
		switch {
		case firstErr != nil:
			out.text = "download error: " + firstErr.Error()
			out.err = true
		case written == 0 && skipped == len(tracks):
			out.text = "already downloaded"
		case skipped > 0:
			out.text = fmt.Sprintf("downloaded %d (skipped %d)", written, skipped)
		default:
			out.text = fmt.Sprintf("downloaded %d", written)
		}
		return out
	}
}
