package media

import (
	"context"
	"fmt"
	"time"
)

// Progress reports how far a library fetch has got: done items of total
// (total is 0 while unknown).
type Progress func(done, total int)

// PageSize is how many tracks a library fetch asks for per request.
// Per-request overhead dominates on a healthy server (10.5k tracks: ~8s
// at 500, ~16s at 200), and with a per-page timeout of minutes rather
// than the HTTP client's seconds, a slow server copes with 500 too.
const PageSize = 500

// PageTimeout bounds one page request. There is deliberately no bound on
// the whole fetch: a large library on a slow server takes as long as it
// takes, as long as each page keeps arriving. (A fixed 90s cap on the
// whole sync made libraries of a few thousand tracks on such servers
// impossible to load.)
var PageTimeout = 2 * time.Minute

// pageRetries is how many times a failed page is retried, waiting
// retryDelay × attempt between tries.
var (
	pageRetries = 3
	retryDelay  = 2 * time.Second
)

// FetchPage fetches up to PageSize items starting at offset start. It
// returns the items and the server's total count, or -1 if the server
// doesn't say.
type FetchPage[T any] func(ctx context.Context, start int) (items []T, total int, err error)

// FetchAll pages through a collection with fetch until the server's
// total is reached (or, when it doesn't report one, until a short page),
// retrying failed pages and reporting progress after each one.
func FetchAll[T any](ctx context.Context, fetch FetchPage[T], progress Progress) ([]T, error) {
	var all []T
	for start := 0; ; {
		items, total, err := fetchWithRetry(ctx, fetch, start)
		if err != nil {
			return nil, fmt.Errorf("fetching items %d–%d: %w", start, start+PageSize, err)
		}
		all = append(all, items...)
		start += len(items)
		if progress != nil {
			progress(len(all), max(total, 0))
		}
		done := len(items) == 0
		if total >= 0 {
			done = done || start >= total
		} else {
			done = done || len(items) < PageSize
		}
		if done {
			return all, nil
		}
	}
}

// fetchWithRetry runs one page under PageTimeout, retrying on failure
// unless the caller's context is done.
func fetchWithRetry[T any](ctx context.Context, fetch FetchPage[T], start int) ([]T, int, error) {
	var lastErr error
	for attempt := 0; attempt <= pageRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(retryDelay * time.Duration(attempt)):
			}
		}
		pctx, cancel := context.WithTimeout(ctx, PageTimeout)
		items, total, err := fetch(pctx, start)
		cancel()
		if err == nil {
			return items, total, nil
		}
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		lastErr = err
	}
	return nil, 0, fmt.Errorf("after %d attempts: %w", pageRetries+1, lastErr)
}
