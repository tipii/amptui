package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/tipii/amptui/internal/config"
	"github.com/tipii/amptui/internal/downloader"
	"github.com/tipii/amptui/internal/media"
)

// TestObserveComputesSpeed: two readings a known interval apart should
// give the rate between them. The first reading only establishes the
// baseline — there's nothing to difference against yet.
func TestObserveComputesSpeed(t *testing.T) {
	j := &downloadJob{}
	t0 := time.Now()
	j.observe(downloader.Progress{Name: "01 a.flac", Written: 0, Total: 1000}, t0)
	if j.speed != 0 {
		t.Errorf("speed after one sample = %v, want 0", j.speed)
	}
	j.observe(downloader.Progress{Name: "01 a.flac", Written: 500, Total: 1000}, t0.Add(time.Second))
	if want := 500.0; j.speed != want {
		t.Errorf("speed = %v, want %v B/s", j.speed, want)
	}
	if j.curBytes != 500 || j.curSize != 1000 {
		t.Errorf("curBytes/curSize = %d/%d, want 500/1000", j.curBytes, j.curSize)
	}
	// A steady rate stays put under smoothing.
	j.observe(downloader.Progress{Name: "01 a.flac", Written: 1000, Total: 1000}, t0.Add(2*time.Second))
	if got := j.speed; got < 490 || got > 510 {
		t.Errorf("steady-rate speed drifted to %v", got)
	}
}

// TestObserveResetsOnNewFile: differencing across two files would report
// a huge negative or positive spike, so a new name restarts the average.
func TestObserveResetsOnNewFile(t *testing.T) {
	j := &downloadJob{}
	t0 := time.Now()
	j.observe(downloader.Progress{Name: "01 a.flac", Written: 0, Total: 900}, t0)
	j.observe(downloader.Progress{Name: "01 a.flac", Written: 900, Total: 900}, t0.Add(time.Second))
	if j.speed == 0 {
		t.Fatal("setup: expected a speed after two samples")
	}
	// Next track starts from zero bytes.
	j.observe(downloader.Progress{Name: "02 b.flac", Written: 0, Total: 400}, t0.Add(2*time.Second))
	if j.speed != 0 {
		t.Errorf("speed = %v after switching files, want a restarted average", j.speed)
	}
	if j.curFile != "02 b.flac" || j.curBytes != 0 {
		t.Errorf("curFile/curBytes = %q/%d, want the new file at 0", j.curFile, j.curBytes)
	}
	j.observe(downloader.Progress{Name: "02 b.flac", Written: 200, Total: 400}, t0.Add(3*time.Second))
	if want := 200.0; j.speed != want {
		t.Errorf("speed = %v, want %v B/s from the new file alone", j.speed, want)
	}
}

// TestClearProgressOnJobCompletion: a finished row must stop advertising
// the last file it happened to be writing.
func TestClearProgressOnJobCompletion(t *testing.T) {
	m := Model{cfg: config.Config{DownloadFolder: "/tmp"}, dl: &liveDownload{}}
	job := &downloadJob{id: 1, label: "Album", state: downloadRunning,
		tracks: []media.Track{{Title: "one"}}}
	job.observe(downloader.Progress{Name: "01 one.flac", Written: 10, Total: 20}, time.Now())
	m.downloadJobs = []*downloadJob{job}

	m, _ = m.applyDownloadTick(downloadTickMsg{jobID: 1})
	if job.state != downloadDone {
		t.Fatalf("state = %v, want downloadDone", job.state)
	}
	if job.curFile != "" || job.speed != 0 || job.curBytes != 0 {
		t.Errorf("finished job still shows progress: file=%q bytes=%d speed=%v",
			job.curFile, job.curBytes, job.speed)
	}
}

// TestDownloadPollFoldsInProgress walks the whole hand-off: the worker
// publishes to the shared snapshot, the poll reads it back on the Update
// goroutine, and the footer picks up the rate.
func TestDownloadPollFoldsInProgress(t *testing.T) {
	m := Model{cfg: config.Config{DownloadFolder: "/tmp"}, dl: &liveDownload{}, downloadPolling: true}
	job := &downloadJob{id: 7, label: "Album", state: downloadRunning,
		tracks: []media.Track{{Title: "one"}, {Title: "two"}}}
	m.downloadJobs = []*downloadJob{job}

	m.dl.publish(7, downloader.Progress{Name: "01 one.flac", Written: 0, Total: 2048})
	m, cmd := m.applyDownloadPoll()
	if cmd == nil {
		t.Error("poll should rearm while a job is running")
	}
	// Backdate the sample so the next reading spans a known second.
	job.sampleAt = time.Now().Add(-time.Second)
	m.dl.publish(7, downloader.Progress{Name: "01 one.flac", Written: 1024, Total: 2048})
	m, _ = m.applyDownloadPoll()

	if job.curFile != "01 one.flac" {
		t.Errorf("curFile = %q, want the published name", job.curFile)
	}
	if job.speed <= 0 {
		t.Errorf("speed = %v, want a positive rate", job.speed)
	}
	if !strings.Contains(m.downloadStatus, "downloading Album (1/2)") {
		t.Errorf("status = %q, want the job's progress", m.downloadStatus)
	}
	if !strings.Contains(m.downloadStatus, "/s") {
		t.Errorf("status = %q, want a transfer rate", m.downloadStatus)
	}

	// Once nothing is running the poll stops rearming, so an idle app
	// isn't waking up four times a second forever.
	job.state = downloadDone
	m, cmd = m.applyDownloadPoll()
	if cmd != nil {
		t.Error("poll should not rearm once every job has finished")
	}
	if m.downloadPolling {
		t.Error("downloadPolling should be cleared so the next job restarts the loop")
	}
}

// TestDownloadDetailShowsFileProgressAndSpeed covers the row the user
// actually reads.
func TestDownloadDetailShowsFileProgressAndSpeed(t *testing.T) {
	j := &downloadJob{label: "Album", state: downloadRunning,
		tracks:  []media.Track{{Title: "one"}, {Title: "two"}},
		curFile: "01 Tumbling Dice.flac", curBytes: 512 << 10, curSize: 1024 << 10, speed: 1 << 20}
	out := renderDownloadRow(j, 70)

	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("a running job should render a detail line, got %d line(s):\n%s", len(lines), out)
	}
	detail := lines[1]
	for _, want := range []string{"Tumbling Dice", "50%", "1.0 MB/s"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail line %q missing %q", detail, want)
		}
	}
}

// TestDownloadDetailWithoutTotalShowsBytes: a transcode arrives chunked
// with no Content-Length, so there's no honest percentage to show — the
// byte count and the rate are what's left.
func TestDownloadDetailWithoutTotalShowsBytes(t *testing.T) {
	j := &downloadJob{label: "Album", state: downloadRunning,
		tracks:  []media.Track{{Title: "one"}},
		curFile: "01 one.mp3", curBytes: 3 << 20, curSize: 0, speed: 512 << 10}
	detail := strings.Split(renderDownloadRow(j, 70), "\n")[1]
	if !strings.Contains(detail, "3.0 MB") {
		t.Errorf("detail %q should show the bytes written", detail)
	}
	if strings.Contains(detail, "%") {
		t.Errorf("detail %q should not invent a percentage without a total", detail)
	}
	if !strings.Contains(detail, "512.0 KB/s") {
		t.Errorf("detail %q should still show the rate", detail)
	}
}

// TestDownloadRowIdleJobsHaveNoDetailLine: queued and finished jobs stay
// one line each, so the modal doesn't grow a blank row per history entry.
func TestDownloadRowIdleJobsHaveNoDetailLine(t *testing.T) {
	for _, state := range []downloadState{downloadQueued, downloadDone, downloadErrored} {
		j := &downloadJob{label: "Album", state: state, tracks: []media.Track{{Title: "one"}}}
		if got := renderDownloadRow(j, 70); strings.Contains(got, "\n") {
			t.Errorf("state %v rendered more than one line:\n%s", state, got)
		}
	}
}

// TestDownloadQualityComesFromConfig: the preset is read per track, so
// changing it in settings applies without a relaunch.
func TestDownloadQualityComesFromConfig(t *testing.T) {
	m := Model{cfg: config.Config{DownloadQuality: "mp3-192"}}
	if got, want := m.downloadQuality(), (media.DownloadQuality{Codec: "mp3", Bitrate: 192}); got != want {
		t.Errorf("downloadQuality() = %+v, want %+v", got, want)
	}
	// An empty or unusable setting downloads the original.
	m.cfg.DownloadQuality = ""
	if !m.downloadQuality().IsOriginal() {
		t.Error("an unset quality should mean the original file")
	}
	m.cfg.DownloadQuality = "not-a-quality"
	if !m.downloadQuality().IsOriginal() {
		t.Error("an unrecognized quality should fall back to the original")
	}
}

// TestFooterKeepsStatusWhenHelpIsLong: the rate lives at the end of the
// status, so a help line that doesn't fit must be the thing that gives
// way — otherwise the number the user is watching is the first casualty.
func TestFooterKeepsStatusWhenHelpIsLong(t *testing.T) {
	m := Model{width: 80, downloadStatus: "downloading Album (2/9) · 1.2 MB/s"}
	help := "? help • s search • enter open • tab dash/lib • Q queue • i info • n next • p prev • ctrl+q quit"
	got := m.footerLine(help)

	// One row. A lipgloss width narrower than its content wraps instead
	// of truncating, and the wrapped remainder takes the status with it
	// onto a line the footer never renders.
	if n := strings.Count(got, "\n"); n != 0 {
		t.Errorf("footer wrapped onto %d extra line(s):\n%s", n, got)
	}
	if lipgloss.Width(got) > m.width {
		t.Errorf("footer is %d cells wide, want at most %d:\n%s", lipgloss.Width(got), m.width, got)
	}
	if !strings.Contains(got, "1.2 MB/s") {
		t.Errorf("footer dropped the transfer rate: %q", got)
	}
	if !strings.Contains(ansi.Strip(got), "  downloading") {
		t.Errorf("trimmed help should not butt against the status: %q", ansi.Strip(got))
	}
	// And the status must still end the row, not sit mid-line. Compared
	// after stripping styles: the rendered row ends with a reset.
	if !strings.HasSuffix(ansi.Strip(got), "1.2 MB/s") {
		t.Errorf("status should be flush right, got %q", got)
	}
}
