package imgcache

import (
	"bytes"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func useTempCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	SetNamespace("")
	mu.Lock()
	total = -1
	mu.Unlock()
	old := MaxBytes
	t.Cleanup(func() { MaxBytes = old; SetNamespace("") })
}

func TestNamespaceSeparatesServers(t *testing.T) {
	useTempCache(t)
	SetNamespace("plex|http://a")
	if err := Put("grid/1", 0, 0, []byte("server A")); err != nil {
		t.Fatal(err)
	}
	SetNamespace("plex|http://b")
	if b, _ := Get("grid/1", 0, 0); b != nil {
		t.Errorf("server B saw server A's image: %q", b)
	}
	SetNamespace("plex|http://a")
	if b, _ := Get("grid/1", 0, 0); string(b) != "server A" {
		t.Errorf("server A's image = %q", b)
	}
}

func TestEvictsLeastRecentlyUsed(t *testing.T) {
	useTempCache(t)
	MaxBytes = 300
	blob := bytes.Repeat([]byte("x"), 100)
	for i := range 3 {
		if err := Put(fmt.Sprint("k", i), 0, 0, blob); err != nil {
			t.Fatal(err)
		}
	}
	// Age everything, then touch k0 so k1 is the oldest.
	for i := range 3 {
		p, _ := Path(fmt.Sprint("k", i), 0, 0)
		old := time.Now().Add(-time.Duration(10-i) * time.Minute)
		_ = os.Chtimes(p, old, old)
	}
	if _, err := Get("k0", 0, 0); err != nil {
		t.Fatal(err)
	}

	if err := Put("k3", 0, 0, blob); err != nil { // 400 > 300: evict to ≤270
		t.Fatal(err)
	}
	for key, want := range map[string]bool{"k0": true, "k1": false, "k2": false, "k3": true} {
		b, _ := Get(key, 0, 0)
		if got := b != nil; got != want {
			t.Errorf("%s present = %v, want %v", key, got, want)
		}
	}
	if s, _ := GetStats(); s.Bytes > 270 {
		t.Errorf("cache is %d bytes after eviction, want ≤ 270", s.Bytes)
	}
}

// Writers used to share one "<key>.tmp" file, so concurrent Puts of the
// same key could rename a half-written file into place.
func TestConcurrentPutsOfOneKey(t *testing.T) {
	useTempCache(t)
	payloads := make([][]byte, 8)
	for i := range payloads {
		payloads[i] = bytes.Repeat([]byte{byte('a' + i)}, 64<<10)
	}
	var wg sync.WaitGroup
	for _, p := range payloads {
		wg.Go(func() {
			if err := Put("same", 0, 0, p); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, _ := Get("same", 0, 0)
	ok := false
	for _, p := range payloads {
		ok = ok || bytes.Equal(got, p)
	}
	if !ok {
		t.Errorf("final file (%d bytes) is not any one writer's payload", len(got))
	}
}

func TestDelete(t *testing.T) {
	useTempCache(t)
	if err := Put("k", 0, 0, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := Delete("k", 0, 0); err != nil {
		t.Fatal(err)
	}
	if b, _ := Get("k", 0, 0); b != nil {
		t.Error("entry survived Delete")
	}
	if err := Delete("missing", 0, 0); err != nil {
		t.Errorf("deleting a missing entry: %v", err)
	}
}

func TestMigrateClearsOldFormatOnce(t *testing.T) {
	useTempCache(t)
	if err := Put("old", 0, 0, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(); err != nil {
		t.Fatal(err)
	}
	if b, _ := Get("old", 0, 0); b != nil {
		t.Error("old-format entry survived the migration")
	}
	if err := Put("new", 0, 0, []byte("y")); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(); err != nil {
		t.Fatal(err)
	}
	if b, _ := Get("new", 0, 0); b == nil {
		t.Error("second Migrate cleared a current cache")
	}
}
