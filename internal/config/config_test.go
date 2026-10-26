package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// useTempConfig points Path() at a fresh directory and returns the
// config file path inside it.
func useTempConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "amptui", "config.toml")
}

func TestSaveIsOwnerOnly(t *testing.T) {
	path := useTempConfig(t)
	// Simulate a file and directory written by an older version.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("server_url = \"http://x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := (Config{ServerURL: "http://x", PlexToken: "secret"}).Save(); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", p, got, want)
		}
	}
}

func TestSaveKeepsEnvSecretsOutOfFile(t *testing.T) {
	path := useTempConfig(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("server_url = \"http://file\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMPTUI_PLEX_TOKEN", "from-env")
	t.Setenv("AMPTUI_SERVER_URL", "http://env")

	c, _ := Load()
	if c.PlexToken != "from-env" || c.ServerURL != "http://env" {
		t.Fatalf("env overrides not applied: %+v", c)
	}
	c.SortAlbum = "added:desc" // what the sort modal does before saving
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(path)
	got := string(b)
	if strings.Contains(got, "from-env") || strings.Contains(got, "http://env") {
		t.Errorf("env values written to disk:\n%s", got)
	}
	if !strings.Contains(got, "http://file") || !strings.Contains(got, "added:desc") {
		t.Errorf("file values or the new sort lost:\n%s", got)
	}

	// A value the user changed in settings, though, is theirs to keep.
	c.ServerURL = "http://typed"
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "http://typed") {
		t.Errorf("edited value not saved:\n%s", b)
	}
}

func TestSaveBacksUpUnparseableFile(t *testing.T) {
	path := useTempConfig(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	broken := "server_url = \"http://x\"\nimages = \"yes\"\nplex_token = \"keep-me\"\n"
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}

	c, _ := Load()
	if LastLoadWarning == nil {
		t.Fatal("expected a load warning for the type mismatch")
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if LastLoadWarning.BackupPath == "" {
		t.Fatal("BackupPath not recorded")
	}
	if b, err := os.ReadFile(LastLoadWarning.BackupPath); err != nil || string(b) != broken {
		t.Errorf("backup = %q, %v; want the original file", b, err)
	}
}

func TestLoadTightensOpenFile(t *testing.T) {
	path := useTempConfig(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("plex_token = \"secret\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", st.Mode().Perm())
	}
}

func TestLoadNormalizesBackendAndURL(t *testing.T) {
	path := useTempConfig(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "backend = \"Jellyfin\"\nserver_url = \" http://jf:8096/ \"\njellyfin_username = \"u\"\njellyfin_password = \"p\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, _ := Load()
	if !c.IsJellyfin() || c.ServerURL != "http://jf:8096" || !c.IsValid() {
		t.Errorf("got backend %q url %q valid=%v", c.Backend, c.ServerURL, c.IsValid())
	}

	// Normalized env values still count as untouched overrides.
	t.Setenv("AMPTUI_BACKEND", "JELLYFIN")
	c, _ = Load()
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "JELLYFIN") || !strings.Contains(string(b), `"Jellyfin"`) && !strings.Contains(string(b), `"jellyfin"`) {
		t.Errorf("env backend leaked into the file:\n%s", b)
	}
}

func TestServerURLWithoutSchemeIsInvalid(t *testing.T) {
	c := Config{ServerURL: "192.168.1.10:32400", PlexToken: "t"}
	if c.IsValid() {
		t.Error("a URL without a scheme was accepted")
	}
	if err := ValidateServerURL("192.168.1.10:32400"); err == nil || !strings.Contains(err.Error(), "http://") {
		t.Errorf("error = %v, want a hint about the scheme", err)
	}
}
