package media

import (
	"context"
	"errors"
	"testing"
	"time"
)

func fastRetries(t *testing.T) {
	t.Helper()
	oldDelay, oldTimeout := retryDelay, PageTimeout
	retryDelay = time.Millisecond
	t.Cleanup(func() { retryDelay, PageTimeout = oldDelay, oldTimeout })
}

// fakeCollection serves n items, PageSize at a time, reporting total
// unless hideTotal.
func fakeCollection(n int, hideTotal bool) FetchPage[int] {
	return func(_ context.Context, start int) ([]int, int, error) {
		var page []int
		for i := start; i < n && len(page) < PageSize; i++ {
			page = append(page, i)
		}
		if hideTotal {
			return page, -1, nil
		}
		return page, n, nil
	}
}

func TestFetchAllStopsAtTotalAndReportsProgress(t *testing.T) {
	var calls [][2]int
	got, err := FetchAll(context.Background(), fakeCollection(2*PageSize+7, false),
		func(done, total int) { calls = append(calls, [2]int{done, total}) })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2*PageSize+7 || got[len(got)-1] != 2*PageSize+6 {
		t.Fatalf("got %d items", len(got))
	}
	want := [][2]int{{PageSize, 2*PageSize + 7}, {2 * PageSize, 2*PageSize + 7}, {2*PageSize + 7, 2*PageSize + 7}}
	if len(calls) != len(want) {
		t.Fatalf("progress calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("progress[%d] = %v, want %v", i, calls[i], want[i])
		}
	}
}

func TestFetchAllWithoutTotalStopsOnShortPage(t *testing.T) {
	got, err := FetchAll(context.Background(), fakeCollection(PageSize+3, true), nil)
	if err != nil || len(got) != PageSize+3 {
		t.Fatalf("got %d items, err %v", len(got), err)
	}
}

// A page that fails or stalls is retried rather than failing the sync.
func TestFetchAllRetriesFailedAndStalledPages(t *testing.T) {
	fastRetries(t)
	PageTimeout = 50 * time.Millisecond
	attempts := map[int]int{}
	base := fakeCollection(2*PageSize, false)
	flaky := func(ctx context.Context, start int) ([]int, int, error) {
		attempts[start]++
		switch {
		case start == 0 && attempts[start] == 1:
			return nil, 0, errors.New("connection reset")
		case start == PageSize && attempts[start] == 1:
			<-ctx.Done() // stalls past PageTimeout
			return nil, 0, ctx.Err()
		}
		return base(ctx, start)
	}
	got, err := FetchAll(context.Background(), flaky, nil)
	if err != nil || len(got) != 2*PageSize {
		t.Fatalf("got %d items, err %v", len(got), err)
	}
	if attempts[0] != 2 || attempts[PageSize] != 2 {
		t.Errorf("attempts = %v, want one retry per page", attempts)
	}
}

func TestFetchAllGivesUpAfterRetries(t *testing.T) {
	fastRetries(t)
	n := 0
	_, err := FetchAll(context.Background(), func(context.Context, int) ([]int, int, error) {
		n++
		return nil, 0, errors.New("server down")
	}, nil)
	if err == nil || n != pageRetries+1 {
		t.Errorf("err=%v after %d attempts, want an error after %d", err, n, pageRetries+1)
	}
}

func TestFetchAllStopsWhenCancelled(t *testing.T) {
	fastRetries(t)
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	_, err := FetchAll(ctx, func(context.Context, int) ([]int, int, error) {
		n++
		cancel()
		return nil, 0, errors.New("boom")
	}, nil)
	if !errors.Is(err, context.Canceled) || n != 1 {
		t.Errorf("err=%v after %d attempts, want Canceled after 1", err, n)
	}
}
