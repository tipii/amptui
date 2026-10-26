package tui

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tipii/amptui/internal/downloader"
	"github.com/tipii/amptui/internal/media"
	"github.com/tipii/amptui/internal/textutil"
)

// downloadStatusLinger is how long a download hint or summary stays in
// the footer once nothing is downloading. It used to stay forever, and
// since it takes the footer's right slot it hid the sync spinner and
// library errors for the rest of the session.
const downloadStatusLinger = 8 * time.Second

// setDownloadStatus sets the footer's download line and restarts its
// linger timer.
func (m *Model) setDownloadStatus(s string) {
	m.downloadStatus = s
	m.downloadStatusAt = time.Now()
}

// downloadStatusVisible reports whether the footer should still show
// the download line: always while a job is queued or running, then for
// downloadStatusLinger after the last update.
func (m Model) downloadStatusVisible() bool {
	if m.downloadStatus == "" {
		return false
	}
	for _, j := range m.downloadJobs {
		if j.state == downloadQueued || j.state == downloadRunning {
			return true
		}
	}
	return time.Since(m.downloadStatusAt) < downloadStatusLinger
}

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

	// Live view of the file currently in flight (tracks[idx]), folded in
	// from the worker's snapshot on every poll. curSize is 0 when the
	// server streams without a Content-Length — a transcode is produced
	// on the fly, so there's no total to measure a percentage against.
	// speed is a smoothed bytes-per-second estimate.
	curFile  string
	curBytes int64
	curSize  int64
	speed    float64
	// sampleBytes / sampleAt are the previous poll's reading, which is
	// what the raw rate is derived from before smoothing.
	sampleBytes int64
	sampleAt    time.Time
}

func (j *downloadJob) total() int { return len(j.tracks) }

// speedSmoothing weights the newest rate sample in the moving average.
// Samples are one poll apart, so a raw rate jumps around with every
// chunk boundary; this keeps the displayed number readable without
// lagging so far behind that a real stall goes unnoticed.
const speedSmoothing = 0.3

// observe folds one progress snapshot into the job and updates the
// smoothed rate. A new file — or a counter that went backwards, which
// happens when the snapshot is read between two tracks — restarts the
// average instead of reporting the nonsense spike that differencing
// across files would produce.
func (j *downloadJob) observe(p downloader.Progress, now time.Time) {
	j.curSize = p.Total
	switch {
	case p.Name != j.curFile || p.Written < j.curBytes:
		j.curFile = p.Name
		j.speed = 0
	case !j.sampleAt.IsZero():
		if dt := now.Sub(j.sampleAt).Seconds(); dt > 0 {
			rate := float64(p.Written-j.sampleBytes) / dt
			if j.speed == 0 {
				j.speed = rate
			} else {
				j.speed = speedSmoothing*rate + (1-speedSmoothing)*j.speed
			}
		}
	}
	j.curBytes = p.Written
	j.sampleBytes, j.sampleAt = p.Written, now
}

// clearProgress drops the in-flight view once a job stops running, so a
// finished row doesn't keep advertising the last file at its last rate.
func (j *downloadJob) clearProgress() {
	j.curFile, j.curBytes, j.curSize, j.speed = "", 0, 0, 0
	j.sampleBytes, j.sampleAt = 0, time.Time{}
}

// statusLine is the footer digest for a running job: which track of how
// many, and how fast it's moving.
func (j *downloadJob) statusLine() string {
	s := fmt.Sprintf("downloading %s (%d/%d)", j.label, j.idx+1, j.total())
	if rate := textutil.HumanRate(j.speed); rate != "" {
		s += " · " + rate
	}
	return s
}

// liveDownload is the hand-off between the goroutine running a download
// (which counts bytes as it copies) and the UI goroutine (which reads a
// snapshot on every poll tick). Bubble Tea's Update loop owns the Model,
// so the worker can't write to it directly: it publishes here instead,
// and applyDownloadPoll folds the latest reading into the job.
//
// A nil *liveDownload is usable and inert, which keeps Models built
// without one (tests, mainly) from having to care.
type liveDownload struct {
	mu    sync.Mutex
	jobID int
	p     downloader.Progress
}

func (l *liveDownload) publish(jobID int, p downloader.Progress) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.jobID, l.p = jobID, p
}

func (l *liveDownload) snapshot() (int, downloader.Progress) {
	if l == nil {
		return 0, downloader.Progress{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.jobID, l.p
}

// downloadTickMsg reports one finished track within a job. The Update
// handler folds it into the job, then either ticks the next track or
// moves on to the next queued job.
type downloadTickMsg struct {
	jobID int
	res   downloader.Result
	err   error
}

// downloadPollMsg asks the Update loop to re-read the worker's progress
// snapshot. Unlike downloadTickMsg it carries nothing: the reading lives
// in Model.dl, and the message only exists to schedule the read on the
// goroutine that owns the Model.
type downloadPollMsg struct{}

// downloadPollInterval is how often the in-flight byte count is re-read
// while something is downloading. Fast enough that the bar moves and the
// rate feels live, slow enough to stay off the render path's back — and
// it costs nothing when idle, since the poll stops rearming itself as
// soon as the last job finishes.
const downloadPollInterval = 250 * time.Millisecond

func downloadPollCmd() tea.Cmd {
	return tea.Tick(downloadPollInterval, func(time.Time) tea.Msg { return downloadPollMsg{} })
}

// handleDownload routes the `d` key. It enqueues a job for whatever's
// under the cursor and, if no other job is already running, kicks off
// the worker. Empty folder / unsupported selection flash a footer hint.
func (m Model) handleDownload() (Model, tea.Cmd) {
	if m.cfg.DownloadFolder == "" {
		m.setDownloadStatus("set a download folder in settings (,)")
		m.downloadErr = true
		return m, nil
	}
	if m.client == nil {
		return m, nil
	}
	tracks, label := m.downloadSelection()
	if len(tracks) == 0 {
		m.setDownloadStatus("press d on a track or album")
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
	m.setDownloadStatus(fmt.Sprintf("queued %s (%d)", label, len(tracks)))
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
	switch it := m.selectedItem().(type) {
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
	case artistItem:
		return m.artistTracks(it.artist.RatingKey), it.artist.Title
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
// returns a tick cmd for its first track, plus a progress poll if one
// isn't already in flight. No-op when nothing is queued.
func (m Model) startNextDownloadJob() (Model, tea.Cmd) {
	for _, j := range m.downloadJobs {
		if j.state == downloadQueued {
			j.state = downloadRunning
			m.setDownloadStatus(fmt.Sprintf("downloading %s (1/%d)", j.label, j.total()))
			m.downloadErr = false
			cmds := []tea.Cmd{m.downloadTickCmd(j.id, j.tracks[0])}
			// One poll loop serves every job — a second would double the
			// sample rate and halve each computed speed.
			if !m.downloadPolling {
				m.downloadPolling = true
				cmds = append(cmds, downloadPollCmd())
			}
			return m, tea.Batch(cmds...)
		}
	}
	return m, nil
}

// downloadQuality is the configured preset. Parsed on each use rather
// than cached so changing it in settings applies to the next track,
// without a relaunch and without disturbing a download already running.
func (m Model) downloadQuality() media.DownloadQuality {
	return media.ParseDownloadQuality(m.cfg.DownloadQuality)
}

// downloadTickCmd downloads one track and returns the result as a
// downloadTickMsg tagged with the owning job's id, publishing byte
// counts to m.dl as it copies. Everything the worker needs is captured
// by value here — the goroutine must not read the Model, which the
// Update loop is free to mutate while the download runs.
func (m Model) downloadTickCmd(jobID int, t media.Track) tea.Cmd {
	client, folder, q, live := m.client, m.cfg.DownloadFolder, m.downloadQuality(), m.dl
	multiDisc := m.albumIsMultiDisc(t)
	return func() tea.Msg {
		// No overall timeout: the downloader abandons a transfer that
		// stops receiving data, and a big hi-res file on a slow link
		// legitimately takes long. (A fixed 5-minute cap made those
		// fail every time, restarting from zero.)
		res, err := downloader.Download(context.Background(), http.DefaultClient, downloader.Request{
			URL:       client.DownloadURL(t, q),
			Track:     t,
			Root:      folder,
			Ext:       q.Ext(),
			MultiDisc: multiDisc,
			Progress:  func(p downloader.Progress) { live.publish(jobID, p) },
		})
		return downloadTickMsg{jobID: jobID, res: res, err: err}
	}
}

// albumIsMultiDisc reports whether t's album spans more than one disc,
// in which case each disc downloads into its own "Disc N" folder. Asked
// of the whole album, not t, so disc 1 lands in "Disc 1" too.
func (m Model) albumIsMultiDisc(t media.Track) bool {
	if t.Disc > 1 {
		return true
	}
	if m.library == nil || t.AlbumRatingKey == "" {
		return false
	}
	for _, at := range m.library.Tracks(t.AlbumRatingKey) {
		if at.Disc > 1 {
			return true
		}
	}
	return false
}

// applyDownloadPoll folds the worker's latest byte count into its job and
// rearms itself for as long as something is still downloading.
func (m Model) applyDownloadPoll() (Model, tea.Cmd) {
	jobID, p := m.dl.snapshot()
	if job := m.downloadJob(jobID); job != nil && job.state == downloadRunning {
		job.observe(p, time.Now())
		m.setDownloadStatus(job.statusLine())
		m.downloadErr = false
	}
	if !m.anyDownloadRunning() {
		m.downloadPolling = false
		return m, nil
	}
	return m, downloadPollCmd()
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
		job.errs = append(job.errs, job.tracks[job.idx].Title+": "+msg.err.Error())
	} else if msg.res.Skipped {
		job.skipped++
	} else {
		job.written++
	}
	job.idx++

	// More tracks in this job? Tick again.
	if job.idx < job.total() {
		m.setDownloadStatus(job.statusLine())
		return m, m.downloadTickCmd(job.id, job.tracks[job.idx])
	}

	// Job complete — finalize state.
	if len(job.errs) > 0 {
		job.state = downloadErrored
	} else {
		job.state = downloadDone
	}
	job.clearProgress()

	// Promote the next queued job if there is one.
	if upd, cmd := m.startNextDownloadJob(); cmd != nil {
		return upd, cmd
	}

	// Nothing else queued — settle the footer with a brief summary.
	m.setDownloadStatus(downloadFooterSummary(m.downloadJobs))
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
// icon, a label, and a progress fraction, plus a detail line for the
// file currently in flight. The active quality preset rides along in the
// title — it's what decides the file you end up with, and it would
// otherwise be invisible from the screen where you watch downloads land.
func (m Model) downloadsModalBox() string {
	mw, _ := m.modalSize()
	innerW := mw - 4 // border (2) + horiz padding (2)
	left := headerStyle.Render(fmt.Sprintf("Downloads · %d", len(m.downloadJobs)))
	right := helpStyle.Render(m.downloadQuality().Label())
	pad := innerW - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		pad = 1
	}
	title := left + strings.Repeat(" ", pad) + right
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

// renderDownloadRow renders one job: a header line with the state icon,
// the label, and how many of its tracks are done — plus, while it runs, a
// detail line for the file being written.
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
	head := left + strings.Repeat(" ", pad) + helpStyle.Render(frac)
	if j.state == downloadRunning && j.curFile != "" {
		head += "\n" + renderDownloadDetail(j, width)
	}
	return head + renderDownloadErrors(j, width)
}

// maxDownloadErrorLines is how many of a job's failures the modal lists
// before summarizing the rest.
const maxDownloadErrorLines = 3

// renderDownloadErrors lists why a job's tracks failed, one per line
// under the job. Only the count used to be shown anywhere.
func renderDownloadErrors(j *downloadJob, width int) string {
	var b strings.Builder
	for i, e := range j.errs {
		if i == maxDownloadErrorLines {
			b.WriteString("\n" + downloadDetailIndent + helpStyle.Render(fmt.Sprintf("…and %d more", len(j.errs)-i)))
			break
		}
		b.WriteString("\n" + downloadDetailIndent + errStyle.Render(trimToWidth(e, width-len(downloadDetailIndent))))
	}
	return b.String()
}

// downloadDetailIndent aligns the detail line under the job label, past
// the state icon and its space.
const downloadDetailIndent = "   "

// downloadBarWidth is the detail line's progress bar, in cells. Fixed so
// the numbers to its right don't shift as the bar fills.
const downloadBarWidth = 14

// renderDownloadDetail is the running job's second line: the file being
// written, then its progress, then the current rate.
//
// Progress is a bar with a percentage when the server sent a
// Content-Length, and a plain byte count when it didn't — a transcode is
// generated as it's sent, so its size isn't known until it's done, and
// an invented percentage would be a worse answer than none. The rate is
// shown either way, and is the only thing that moves during a transcode.
func renderDownloadDetail(j *downloadJob, width int) string {
	// Each piece is styled as it's appended: wrapping the whole string
	// in one style at the end would fight the bar's own colors, whose
	// reset sequences cut the outer style off mid-line.
	var right string
	if j.curSize > 0 {
		pct := float64(j.curBytes) / float64(j.curSize)
		right = downloadBar(pct, downloadBarWidth) +
			helpStyle.Render(fmt.Sprintf(" %3.0f%%", 100*clampFraction(pct)))
	} else {
		right = helpStyle.Render(textutil.HumanBytes(j.curBytes))
	}
	if rate := textutil.HumanRate(j.speed); rate != "" {
		right += helpStyle.Render("  " + rate)
	}
	nameW := width - len(downloadDetailIndent) - lipgloss.Width(right) - 2
	if nameW < 4 {
		nameW = 4
	}
	left := downloadDetailIndent + crumbStyle.Render(trimToWidth(j.curFile, nameW))
	pad := width - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + right
}

// downloadBar draws a fixed-width fill bar. The now-playing bar is a
// bubbles progress.Model sized for the footer; this one has to sit
// inside a modal row, so it's drawn here rather than resizing a shared
// widget out from under the footer.
func downloadBar(pct float64, width int) string {
	filled := int(clampFraction(pct)*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	return npStyle.Render(strings.Repeat("█", filled)) +
		helpStyle.Render(strings.Repeat("░", width-filled))
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
