// Package tmdb asks The Movie Database (TMDB, its v3 API) what the
// analyser needs to match a title: searches by name, and a title's names
// (translations, alternative titles), year and seasons.
//
// TMDB is a free service, so the client is careful with it: every answer
// is kept in a cache on disk, so that a request is made once ever; the
// requests that do go out are spaced (a rate limit well under TMDB's),
// and a "too many requests" answer pauses all of them for as long as
// TMDB asks.
package tmdb

import (
	"context"
	"errors"
	"strconv"
)

// Kind is TMDB's kind of title, as its URLs write it.
type Kind string

// The kinds of titles.
const (
	Movie Kind = "movie"
	TV    Kind = "tv"
)

// ErrNotFound is TMDB's answer for an ID it doesn't know.
var ErrNotFound = errors.New("tmdb: not found")

// ErrNotCached is the answer of an offline client for a request it has
// never made.
var ErrNotCached = errors.New("tmdb: not in the cache (offline)")

// Found is one result of a search.
type Found struct {
	ID           int
	Kind         Kind
	Name         string
	OriginalName string
	Year         int // of its first release or first airing; 0 when unknown
	Popularity   float64
	Votes        int
	// Language is the title's original language, as TMDB writes it (ISO
	// 639-1, "en"); "" when unknown.
	Language string
}

// Season is one of a series' seasons on TMDB.
type Season struct {
	Number   int
	Episodes int // how many episodes TMDB lists
}

// Details are what TMDB knows of one title that matching weighs.
type Details struct {
	Found
	// Translations are the title's names in other languages; AltNames
	// its alternative titles (other countries' names, working titles…).
	Translations []string
	AltNames     []string
	Seasons      []Season // a series' only
	// Runtime is a film's length in minutes, or a series' usual episode
	// length; 0 when TMDB doesn't say.
	Runtime int
	// IMDB is a film's IMDb ID ("tt7654321"); TMDB gives none for series
	// in these details.
	IMDB string
}

// Episode is one episode of a season on TMDB.
type Episode struct {
	Number  int
	Runtime int // in minutes; 0 when TMDB doesn't say
}

// Names are all the names of the title: its own, its original, its
// translations and its alternative titles, in that order (repeats kept).
func (d *Details) Names() []string {
	out := []string{d.Name, d.OriginalName}
	out = append(out, d.Translations...)
	return append(out, d.AltNames...)
}

// Source is TMDB as matching asks it; Client is the real one, tests use
// fakes.
type Source interface {
	// Search looks for titles of a kind by name; year (0 for none) narrows
	// it to titles released (films) or first aired (series) that year.
	Search(ctx context.Context, kind Kind, query string, year int) ([]Found, error)
	// Details are one title's details; ErrNotFound when TMDB has no such
	// title.
	Details(ctx context.Context, kind Kind, id int) (*Details, error)
	// Season is a series' season: its episodes and their lengths;
	// ErrNotFound when TMDB has no such season.
	Season(ctx context.Context, id, number int) ([]Episode, error)
	// FindIMDb gives the titles TMDB knows by an IMDb ID ("tt7654321"),
	// films and series; none when it knows none.
	FindIMDb(ctx context.Context, imdb string) ([]Found, error)
}

// yearOf reads the year of a TMDB date ("2001-05-18"); 0 for none.
func yearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}
	return y
}
