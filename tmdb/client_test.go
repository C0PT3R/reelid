package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fakeTMDB is a made-up TMDB on a local test server: no network.
func fakeTMDB(t *testing.T, tooMany int) (*httptest.Server, *atomic.Int64) {
	var calls atomic.Int64
	refused := atomic.Int64{}
	refused.Store(int64(tooMany))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if refused.Add(-1) >= 0 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		switch r.URL.Path {
		case "/search/movie":
			if r.URL.Query().Get("query") != "quiet harbor" || r.URL.Query().Get("year") != "1998" {
				w.Write([]byte(`{"results":[]}`))
				return
			}
			w.Write([]byte(`{"results":[{"id":1,"title":"Quiet Harbor","original_title":"Quiet Harbor","release_date":"1998-04-02","popularity":12.5,"vote_count":40}]}`))
		case "/tv/10":
			w.Write([]byte(`{"id":10,"name":"Northern Line","original_name":"Nordlinjen","first_air_date":"2010-01-01",
				"seasons":[{"season_number":0,"episode_count":2},{"season_number":1,"episode_count":10}],
				"alternative_titles":{"results":[{"title":"The Northern Line"}]},
				"translations":{"translations":[{"data":{"name":"Ligne du Nord"}},{"data":{"name":""}}]}}`))
		case "/tv/10/season/1":
			w.Write([]byte(`{"season_number":1,"episodes":[{"episode_number":1,"runtime":52,"name":"Departure","air_date":"2010-01-01"},{"episode_number":2,"runtime":null}]}`))
		case "/tv/10/episode_groups":
			w.Write([]byte(`{"results":[{"id":"abc","name":"DVD Order","type":3,"group_count":2,"episode_count":3}]}`))
		case "/tv/episode_group/abc":
			w.Write([]byte(`{"groups":[
				{"name":"Volume 2","order":2,"episodes":[{"season_number":1,"episode_number":3,"order":0,"runtime":50,"name":"Third"}]},
				{"name":"Volume 1","order":1,"episodes":[
					{"season_number":1,"episode_number":2,"order":1,"name":"Second"},
					{"season_number":1,"episode_number":1,"order":0,"name":"First","air_date":"2010-01-01"}]}]}`))
		case "/find/tt0000042":
			if r.URL.Query().Get("external_source") != "imdb_id" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Write([]byte(`{"movie_results":[{"id":1,"title":"Quiet Harbor","release_date":"1998-04-02","original_language":"en"}],"tv_results":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newTestClient(t *testing.T, base, token string) *Client {
	c := NewClient(token, t.TempDir(), 1000)
	c.Base = base
	return c
}

func TestSearchAndCache(t *testing.T) {
	srv, calls := fakeTMDB(t, 0)
	c := newTestClient(t, srv.URL, "secret")
	ctx := context.Background()
	for range 3 {
		found, err := c.Search(ctx, Movie, "quiet harbor", 1998)
		if err != nil {
			t.Fatal(err)
		}
		if len(found) != 1 || found[0].ID != 1 || found[0].Year != 1998 || found[0].Name != "Quiet Harbor" || found[0].Votes != 40 {
			t.Fatalf("got %+v", found)
		}
	}
	if calls.Load() != 1 || c.Requests() != 1 || c.Hits() != 2 {
		t.Errorf("%d calls, %d requests, %d hits; want 1, 1, 2", calls.Load(), c.Requests(), c.Hits())
	}

	// Another client on the same cache, offline: still answered.
	off := NewClient("", c.cache, 1000)
	off.Base, off.Offline = srv.URL, true
	if _, err := off.Search(ctx, Movie, "quiet harbor", 1998); err != nil {
		t.Errorf("offline, cached: %v", err)
	}
	if _, err := off.Search(ctx, Movie, "never asked", 0); !errors.Is(err, ErrNotCached) {
		t.Errorf("offline, not cached: got %v", err)
	}
}

func TestDetails(t *testing.T) {
	srv, calls := fakeTMDB(t, 0)
	c := newTestClient(t, srv.URL, "secret")
	d, err := c.Details(context.Background(), TV, 10)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "Northern Line" || d.OriginalName != "Nordlinjen" || d.Year != 2010 || len(d.Seasons) != 2 || d.Seasons[1].Episodes != 10 {
		t.Errorf("got %+v", d)
	}
	names := d.Names()
	want := []string{"Northern Line", "Nordlinjen", "Ligne du Nord", "The Northern Line"}
	if len(names) != len(want) {
		t.Fatalf("names: got %q, want %q", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("names: got %q, want %q", names, want)
		}
	}

	// A 404 is kept: asked once.
	for range 2 {
		if _, err := c.Details(context.Background(), Movie, 999); !errors.Is(err, ErrNotFound) {
			t.Errorf("got %v, want not found", err)
		}
	}
	if calls.Load() != 2 {
		t.Errorf("%d calls, want 2", calls.Load())
	}
}

func TestSeasonAndFind(t *testing.T) {
	srv, _ := fakeTMDB(t, 0)
	c := newTestClient(t, srv.URL, "secret")
	ctx := context.Background()
	eps, err := c.Season(ctx, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 2 || eps[0].Number != 1 || eps[0].Runtime != 52 || eps[1].Runtime != 0 {
		t.Errorf("season: got %+v", eps)
	}
	if _, err := c.Season(ctx, 10, 7); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing season: got %v, want not found", err)
	}
	if eps[0].Name != "Departure" || eps[0].AirDate != "2010-01-01" {
		t.Errorf("season: names and dates not read: %+v", eps[0])
	}
	orders, err := c.Orders(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].ID != "abc" || orders[0].Type != DVD || orders[0].Groups != 2 {
		t.Errorf("orders: got %+v", orders)
	}
	groups, err := c.OrderSeasons(ctx, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].Name != "Volume 1" || len(groups[0].Episodes) != 2 ||
		groups[0].Episodes[0].Name != "First" || groups[0].Episodes[1].Number != 2 || groups[1].Episodes[0].Runtime != 50 {
		t.Errorf("order seasons: got %+v", groups)
	}
	found, err := c.FindIMDb(ctx, "tt0000042")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != 1 || found[0].Kind != Movie || found[0].Language != "en" {
		t.Errorf("find: got %+v", found)
	}
}

func TestTooManyRequests(t *testing.T) {
	srv, calls := fakeTMDB(t, 2)
	c := newTestClient(t, srv.URL, "secret")
	start := time.Now()
	if _, err := c.Search(context.Background(), Movie, "quiet harbor", 1998); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Errorf("%d calls, want 3 (two refused)", calls.Load())
	}
	if time.Since(start) < firstPause {
		t.Errorf("retried without pausing")
	}
}

func TestRefusedToken(t *testing.T) {
	srv, _ := fakeTMDB(t, 0)
	c := newTestClient(t, srv.URL, "wrong")
	if _, err := c.Search(context.Background(), Movie, "quiet harbor", 1998); err == nil {
		t.Error("no error for a refused token")
	}
}

func TestRateLimit(t *testing.T) {
	l := &limiter{every: 20 * time.Millisecond}
	start := time.Now()
	for range 5 {
		if err := l.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 80*time.Millisecond {
		t.Errorf("5 requests in %v, want at least 80ms", d)
	}
}
