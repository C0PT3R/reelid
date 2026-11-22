package tmdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Client is TMDB's API, behind a cache on disk and a rate limit.
type Client struct {
	// Base is the API's address; tests point it at a fake server.
	Base string
	// Offline makes a request missing from the cache fail (ErrNotCached)
	// instead of going out.
	Offline bool

	token string
	cache string // the cache's folder
	http  *http.Client
	limit *limiter

	// keys serialises requests for one key, so that two workers asking the
	// same thing at once make one request.
	keys sync.Map // key -> *sync.Mutex

	requests, hits atomic.Int64
}

// DefaultBase is TMDB's v3 API.
const DefaultBase = "https://api.themoviedb.org/3"

// NewClient makes a client that sends token (TMDB's "API read access
// token") as its bearer, keeps answers in the folder cache, and makes at
// most perSecond requests a second.
func NewClient(token, cache string, perSecond float64) *Client {
	if perSecond <= 0 {
		perSecond = 10
	}
	return &Client{
		Base:  DefaultBase,
		token: token,
		cache: cache,
		http:  &http.Client{Timeout: 30 * time.Second},
		limit: &limiter{every: time.Duration(float64(time.Second) / perSecond)},
	}
}

// ReadToken reads a bearer token from a file: the line it holds, trimmed.
// The token is a secret: it goes nowhere but the requests' header.
func ReadToken(name string) (string, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("tmdb token: %w", err)
	}
	token, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	if token == "" {
		return "", fmt.Errorf("tmdb token: %s is empty", name)
	}
	return strings.TrimSpace(token), nil
}

// Requests is how many requests went out to TMDB; Hits how many answers
// came from the cache.
func (c *Client) Requests() int64 { return c.requests.Load() }
func (c *Client) Hits() int64     { return c.hits.Load() }

// Search looks for titles by name (see Source). Only TMDB's first page
// of results, its twenty best, is asked for.
func (c *Client) Search(ctx context.Context, kind Kind, query string, year int) ([]Found, error) {
	params := url.Values{"query": {query}, "include_adult": {"false"}}
	if year > 0 {
		if kind == TV {
			params.Set("first_air_date_year", strconv.Itoa(year))
		} else {
			params.Set("year", strconv.Itoa(year))
		}
	}
	body, err := c.get(ctx, "/search/"+string(kind), params)
	if err != nil {
		return nil, err
	}
	var page struct {
		Results []result `json:"results"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("tmdb search: %w", err)
	}
	out := make([]Found, 0, len(page.Results))
	for _, r := range page.Results {
		out = append(out, r.found(kind))
	}
	return out, nil
}

// Details are a title's details (see Source), with its alternative
// titles and translations in the same request.
func (c *Client) Details(ctx context.Context, kind Kind, id int) (*Details, error) {
	params := url.Values{"append_to_response": {"alternative_titles,translations"}}
	body, err := c.get(ctx, "/"+string(kind)+"/"+strconv.Itoa(id), params)
	if err != nil {
		return nil, err
	}
	var r struct {
		result
		Seasons []struct {
			Number   int `json:"season_number"`
			Episodes int `json:"episode_count"`
		} `json:"seasons"`
		Alternative struct {
			Titles  []struct{ Title string } `json:"titles"`  // a film's
			Results []struct{ Title string } `json:"results"` // a series'
		} `json:"alternative_titles"`
		Translations struct {
			Translations []struct {
				Data struct {
					Title string `json:"title"` // a film's
					Name  string `json:"name"`  // a series'
				} `json:"data"`
			} `json:"translations"`
		} `json:"translations"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("tmdb details: %w", err)
	}
	d := &Details{Found: r.found(kind)}
	for _, s := range r.Seasons {
		d.Seasons = append(d.Seasons, Season{Number: s.Number, Episodes: s.Episodes})
	}
	for _, a := range append(r.Alternative.Titles, r.Alternative.Results...) {
		if a.Title != "" {
			d.AltNames = append(d.AltNames, a.Title)
		}
	}
	for _, t := range r.Translations.Translations {
		if name := t.Data.Title + t.Data.Name; name != "" {
			d.Translations = append(d.Translations, name)
		}
	}
	return d, nil
}

// result is a title as TMDB's answers write it, films and series alike.
type result struct {
	ID            int     `json:"id"`
	Title         string  `json:"title"`
	Name          string  `json:"name"`
	OriginalTitle string  `json:"original_title"`
	OriginalName  string  `json:"original_name"`
	ReleaseDate   string  `json:"release_date"`
	FirstAirDate  string  `json:"first_air_date"`
	Popularity    float64 `json:"popularity"`
	VoteCount     int     `json:"vote_count"`
}

func (r result) found(kind Kind) Found {
	f := Found{ID: r.ID, Kind: kind, Popularity: r.Popularity, Votes: r.VoteCount}
	if kind == TV {
		f.Name, f.OriginalName, f.Year = r.Name, r.OriginalName, yearOf(r.FirstAirDate)
	} else {
		f.Name, f.OriginalName, f.Year = r.Title, r.OriginalTitle, yearOf(r.ReleaseDate)
	}
	return f
}

// cached is an answer as the cache keeps it.
type cached struct {
	Key    string          `json:"key"`
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// get answers a GET of path with params: from the cache, or from TMDB
// (then kept). A 404 is kept too: TMDB isn't asked twice for a title it
// doesn't have.
func (c *Client) get(ctx context.Context, p string, params url.Values) ([]byte, error) {
	key := p + "?" + params.Encode() // Encode sorts the parameters
	mu, _ := c.keys.LoadOrStore(key, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	file := c.cacheFile(key)
	if ans, ok := c.fromCache(file, key); ok {
		c.hits.Add(1)
		return answer(ans)
	}
	if c.Offline {
		return nil, ErrNotCached
	}
	status, body, err := c.fetch(ctx, key)
	if err != nil {
		return nil, err
	}
	ans := cached{Key: key, Status: status}
	if status == http.StatusOK {
		if !json.Valid(body) {
			return nil, fmt.Errorf("tmdb: %s: the answer isn't JSON", p)
		}
		ans.Body = body
	}
	if err := c.toCache(file, ans); err != nil {
		return nil, err
	}
	return answer(ans)
}

// answer turns a kept answer into its body or its error.
func answer(ans cached) ([]byte, error) {
	if ans.Status == http.StatusNotFound {
		return nil, ErrNotFound
	}
	return ans.Body, nil
}

// cacheFile is where key's answer is kept: a file named by the key's
// hash, in a folder named by its first two characters.
func (c *Client) cacheFile(key string) string {
	sum := sha256.Sum256([]byte(key))
	h := hex.EncodeToString(sum[:])
	return filepath.Join(c.cache, h[:2], h+".json")
}

func (c *Client) fromCache(file, key string) (cached, bool) {
	b, err := os.ReadFile(file)
	if err != nil {
		return cached{}, false
	}
	var ans cached
	if json.Unmarshal(b, &ans) != nil || ans.Key != key {
		return cached{}, false
	}
	return ans, true
}

// toCache writes an answer to a temporary file, then renames it: a file
// of the cache is whole or absent, even if the program is stopped.
func (c *Client) toCache(file string, ans cached) error {
	b, err := json.Marshal(ans)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return fmt.Errorf("tmdb cache: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".tmp-*")
	if err != nil {
		return fmt.Errorf("tmdb cache: %w", err)
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("tmdb cache: %w", err)
	}
	return os.Rename(tmp.Name(), file)
}

// The retries of one request: "too many requests" and TMDB's own errors
// are tried again, after a pause that doubles each time.
const (
	retries    = 6
	firstPause = time.Second
)

// fetch makes the request, within the rate limit, and gives TMDB's
// status and body. Only 200 and 404 are answers; other statuses are
// retried or are errors.
func (c *Client) fetch(ctx context.Context, key string) (int, []byte, error) {
	pause := firstPause
	for try := 0; ; try++ {
		if err := c.limit.wait(ctx); err != nil {
			return 0, nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+key, nil)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/json")
		c.requests.Add(1)
		resp, err := c.http.Do(req)
		var body []byte
		if err == nil {
			body, err = io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			resp.Body.Close()
		}
		path, _, _ := strings.Cut(key, "?") // the query may name a title: kept out of errors
		switch {
		case err != nil && ctx.Err() != nil:
			return 0, nil, ctx.Err()
		case err == nil && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound):
			return resp.StatusCode, body, nil
		case err == nil && resp.StatusCode == http.StatusUnauthorized:
			return 0, nil, errors.New("tmdb: the token was refused (401)")
		case err == nil && resp.StatusCode == http.StatusTooManyRequests:
			// Everyone waits: as long as TMDB says, or the pause.
			wait := pause
			if s, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && s > 0 {
				wait = max(wait, time.Duration(s)*time.Second)
			}
			c.limit.hold(wait)
		case err == nil && resp.StatusCode < 500:
			return 0, nil, fmt.Errorf("tmdb: %s: status %d", path, resp.StatusCode)
		}
		if try == retries {
			if err == nil {
				err = fmt.Errorf("status %d", resp.StatusCode)
			}
			return 0, nil, fmt.Errorf("tmdb: %s: %w (after %d tries)", path, err, try+1)
		}
		select {
		case <-time.After(pause):
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		}
		pause *= 2
	}
}

// limiter spaces requests: one every `every`, all workers together.
type limiter struct {
	mu    sync.Mutex
	every time.Duration
	next  time.Time // when the next request may go
}

// wait waits for the next request's turn.
func (l *limiter) wait(ctx context.Context) error {
	l.mu.Lock()
	at := later(time.Now(), l.next)
	l.next = at.Add(l.every)
	l.mu.Unlock()
	select {
	case <-time.After(time.Until(at)):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// hold makes every request wait d from now.
func (l *limiter) hold(d time.Duration) {
	l.mu.Lock()
	l.next = later(l.next, time.Now().Add(d))
	l.mu.Unlock()
}

// later is the later of two times.
func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
