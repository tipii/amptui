package media

import (
	"errors"
	"net/url"
)

// RedactURL drops the query string from raw, which is where Plex
// (X-Plex-Token) and Jellyfin (api_key) carry credentials in stream,
// download and artwork URLs. Used before a URL goes into an error.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable URL>"
	}
	if u.RawQuery != "" {
		u.RawQuery = "…"
	}
	u.User = nil
	return u.String()
}

// RedactErr strips credentials from the URL inside an *url.Error, as
// returned by http.Client.Do, so a transport error can be shown or
// logged safely. Other errors pass through unchanged.
func RedactErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		ue.URL = RedactURL(ue.URL)
	}
	return err
}
