package chafa

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// fakeChafa puts a stand-in "chafa" on PATH that records its arguments
// and prints a 2×2 colored block, so tests don't need the real one.
func fakeChafa(t *testing.T) (argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\nprintf '\\033[31mAB\\033[0m\\nCD\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "chafa"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	binPath, lookOnce = "", sync.Once{} // look PATH up again
	return argsFile
}

func TestRenderPadsToTheBox(t *testing.T) {
	args := fakeChafa(t)
	out, err := Render(context.Background(), "/img.jpg", 6, 4)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 4 {
		t.Fatalf("%d lines, want 4:\n%q", len(lines), out)
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != 6 {
			t.Errorf("line %d is %d cells, want 6: %q", i, w, l)
		}
	}
	// 2×2 image centered in 6×4: one blank row above, two cells of margin.
	if strings.TrimSpace(ansi.Strip(lines[1])) != "AB" || !strings.HasPrefix(ansi.Strip(lines[1]), "  AB") {
		t.Errorf("not centered: %q", ansi.Strip(lines[1]))
	}
	b, _ := os.ReadFile(args)
	for _, want := range []string{"--format symbols", "--size 6x4", "--polite on", "/img.jpg"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("chafa args %q missing %q", b, want)
		}
	}
}

func TestFitBoxTruncatesOverflow(t *testing.T) {
	out := fitBox("ABCDEFGH\n1\n2\n3\n4\n5\n", 3, 2)
	lines := strings.Split(out, "\n")
	if len(lines) != 2 || ansi.Strip(lines[0]) != "ABC" {
		t.Errorf("fitBox = %q", out)
	}
}
