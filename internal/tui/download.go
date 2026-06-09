package tui

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tipii/amptui/internal/downloader"
	"github.com/tipii/amptui/internal/media"
)

// downloadState is the lifecycle of one downloadJob.
type downloadState int

const (
	downloadQueued downloadState = iota
	downloadRunning
	downloadDone
	downloadErrored
)

// downloadJob is one unit of work — a track or an album — sitting in the
// download list. idx is the next track to process; written/skipped/errs
// are filled in as the worker advances through tracks.
type downloadJob struct {
	id      int
	label   string
	tracks  []media.Track
	idx     int
	written int
	skipped int
	errs    []string
	state   downloadState
}

func (j *downloadJob) total() int { return len(j.tracks) }

// downloadTickMsg reports one finished track within a job. The Update
// handler folds it into the job, then either ticks the next track or
// moves on to the next queued job.
type downloadTickMsg struct {
	jobID int
	res   downloader.Result
	err   error
}

// downloadTimeoutPerTrack caps each track's GET. The per-job parent
// context lives for downloadTimeoutPerTrack * (N+1) so a long album
// doesn't get cut off halfway through.
const downloadTimeoutPerTrack = 5 * time.Minute

// handleDownload routes the `d` key. It enqueues a job for whatever's
// under the cursor and, if no other job is already running, kicks off
// the worker. Empty folder / unsupported selection flash a footer hint.
func (m Model) handleDownload() (Model, tea.Cmd) {
	if m.cfg.DownloadFolder == "" {
		m.downloadStatus = "set a download folder in settings (,)"
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

	m.nextDownloadJobID++
	job := &downloadJob{
		id:     m.nextDownloadJobID,
		label:  label,
		tracks: tracks,
		state:  downloadQueued,
	}
	m.downloadJobs = append(m.downloadJobs, job)
	m.downloadErr = false
	m.downloadStatus = fmt.Sprintf("queued %s (%d)", label, len(tracks))
	// If nothing's running, start the worker now. If something is, the
	// existing worker's downloadTickMsg handler will pick this job up
	// when it falls idle.
	if !m.anyDownloadRunning() {
		return m.startNextDownloadJob()
	}
	return m, nil
}

// downloadSelection resolves what's under the cursor into a track list
// + a human label.
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

// anyDownloadRunning reports whether the worker is busy on some job.
func (m Model) anyDownloadRunning() bool {
	for _, j := range m.downloadJobs {
		if j.state == downloadRunning {
			return true
		}
	}
	return false
}

// startNextDownloadJob promotes the next queued job to running and
// returns a tick cmd for its first track. No-op when nothing is queued.
func (m Model) startNextDownloadJob() (Model, tea.Cmd) {
	for _, j := range m.downloadJobs {
		if j.state == downloadQueued {
			j.state = downloadRunning
			m.downloadStatus = fmt.Sprintf("downloading %s (1/%d)", j.label, j.total())
			m.downloadErr = false
			return m, downloadTickCmd(j.id, j.tracks[0], m.client, m.cfg.DownloadFolder)
		}
	}
	return m, nil
}

// downloadTickCmd downloads one track and returns the result as a
// downloadTickMsg tagged with the owning job's id.
func downloadTickCmd(jobID int, t media.Track, client media.Backend, folder string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), downloadTimeoutPerTrack)
		defer cancel()
		hc := &http.Client{Timeout: downloadTimeoutPerTrack}
		res, err := downloader.Download(ctx, hc, client.StreamURL(t), t, folder)
		return downloadTickMsg{jobID: jobID, res: res, err: err}
	}
}

// applyDownloadTick folds one finished track into its job, then either
// ticks the next track of the same job, advances to the next queued
// job, or settles with a summary footer status.
func (m Model) applyDownloadTick(msg downloadTickMsg) (Model, tea.Cmd) {
	job := m.downloadJob(msg.jobID)
	if job == nil {
		return m, nil // job vanished — shouldn't happen, fail safe
	}
	if msg.err != nil {
		job.errs = append(job.errs, msg.err.Error())
	} else if msg.res.Skipped {
		job.skipped++
	} else {
		job.written++
	}
	job.idx++

	// More tracks in this job? Tick again.
	if job.idx < job.total() {
		m.downloadStatus = fmt.Sprintf("downloading %s (%d/%d)", job.label, job.idx+1, job.total())
		return m, downloadTickCmd(job.id, job.tracks[job.idx], m.client, m.cfg.DownloadFolder)
	}

	// Job complete — finalize state.
	if len(job.errs) > 0 {
		job.state = downloadErrored
	} else {
		job.state = downloadDone
	}

	// Promote the next queued job if there is one.
	if upd, cmd := m.startNextDownloadJob(); cmd != nil {
		return upd, cmd
	}

	// Nothing else queued — settle the footer with a brief summary.
	m.downloadStatus = downloadFooterSummary(m.downloadJobs)
	m.downloadErr = anyDownloadErrors(m.downloadJobs)
	return m, nil
}

// downloadJob looks a job up by id. Returns nil if it was already
// reaped (we don't reap today, kept as a safety hatch).
func (m Model) downloadJob(id int) *downloadJob {
	for _, j := range m.downloadJobs {
		if j.id == id {
			return j
		}
	}
	return nil
}

func anyDownloadErrors(jobs []*downloadJob) bool {
	for _, j := range jobs {
		if j.state == downloadErrored {
			return true
		}
	}
	return false
}

// downloadFooterSummary renders a one-line digest across all jobs. Used
// when the worker is idle so the footer still surfaces the most recent
// outcome at a glance.
func downloadFooterSummary(jobs []*downloadJob) string {
	if len(jobs) == 0 {
		return ""
	}
	var w, s, e int
	for _, j := range jobs {
		w += j.written
		s += j.skipped
		e += len(j.errs)
	}
	switch {
	case e > 0:
		return fmt.Sprintf("downloads: %d ok · %d skipped · %d errors", w, s, e)
	case s > 0:
		return fmt.Sprintf("downloaded %d (skipped %d)", w, s)
	default:
		return fmt.Sprintf("downloaded %d", w)
	}
}

// downloadsModalBox renders the D-modal: one line per job with a state
// icon, a label, and a progress fraction.
func (m Model) downloadsModalBox() string {
	mw, _ := m.modalSize()
	innerW := mw - 4 // border (2) + horiz padding (2)
	title := headerStyle.Render(fmt.Sprintf("Downloads · %d", len(m.downloadJobs)))
	return m.modalFrame(title + "\n" + m.downloadsModalBody(innerW))
}

func (m Model) downloadsModalBody(innerW int) string {
	if len(m.downloadJobs) == 0 {
		return helpStyle.Render("no downloads yet — press d on a track or album")
	}
	var b strings.Builder
	for i, j := range m.downloadJobs {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(renderDownloadRow(j, innerW))
	}
	return b.String()
}

func renderDownloadRow(j *downloadJob, width int) string {
	icon, style := downloadIcon(j.state)
	frac := fmt.Sprintf("  %d/%d", j.idx, j.total())
	// Label gets whatever cells the icon (2) + fraction don't.
	labelW := width - lipgloss.Width(icon) - lipgloss.Width(frac) - 1
	if labelW < 4 {
		labelW = 4
	}
	label := j.label
	if lipgloss.Width(label) > labelW {
		// Trim by rune to fit the available cells.
		label = trimToWidth(label, labelW)
	}
	left := style.Render(icon) + " " + label
	pad := width - lipgloss.Width(left) - lipgloss.Width(frac)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + helpStyle.Render(frac)
}

func downloadIcon(s downloadState) (string, lipgloss.Style) {
	switch s {
	case downloadQueued:
		return "◌", helpStyle
	case downloadRunning:
		return "⟳", npStyle
	case downloadDone:
		return "✓", npStyle
	case downloadErrored:
		return "✗", errStyle
	}
	return " ", helpStyle
}

// trimToWidth crops s to fit cellsMax visible cells, appending an ellipsis
// when truncated. Width-aware (not byte-based) to keep multibyte titles
// readable.
func trimToWidth(s string, cellsMax int) string {
	if lipgloss.Width(s) <= cellsMax {
		return s
	}
	if cellsMax <= 1 {
		return "…"
	}
	out := ""
	for _, r := range s {
		next := out + string(r)
		if lipgloss.Width(next)+1 > cellsMax { // reserve a cell for the ellipsis
			break
		}
		out = next
	}
	return out + "…"
}
