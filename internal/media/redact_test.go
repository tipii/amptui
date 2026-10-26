package media

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestRedactURL(t *testing.T) {
	got := RedactURL("http://host:32400/library/parts/1/file.flac?X-Plex-Token=secret&download=1")
	if strings.Contains(got, "secret") || !strings.HasPrefix(got, "http://host:32400/library/parts/1/file.flac") {
		t.Errorf("RedactURL = %q", got)
	}
}

func TestRedactErr(t *testing.T) {
	_, err := (&http.Client{}).Get("http://127.0.0.1:1/x?api_key=secret")
	if err == nil {
		t.Skip("unexpectedly connected")
	}
	if msg := RedactErr(err).Error(); strings.Contains(msg, "secret") {
		t.Errorf("token survived: %s", msg)
	}
	plain := errors.New("plain")
	if RedactErr(plain) != plain {
		t.Error("non-url error was altered")
	}
}
