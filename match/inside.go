package match

import (
	"cmp"
	"context"
	"errors"
	"math"
	"slices"

	"github.com/C0PT3R/reelid/analyse"
	"github.com/C0PT3R/reelid/inside"
	"github.com/C0PT3R/reelid/tmdb"
)

// Stage 3: what the files say of themselves (see package inside), weighed
// beside what the paths say. Every signal is optional: a query without
// any is matched as in stage 2.
//
// The weights below were tuned on the evaluator (cmd/evaluate -stage 3),
// on the user's library with its paths and its tags damaged in named
// ways; they are few and round on purpose, so that they don't fit that
// one library more than libraries in general.
const (
	// idScore is for the TMDB title the tags (or the paths) name by its
	// ID. With its name it wins by far (and is sure). Alone, it is less
	// than a name and a year: an ID tag copied from another file loses to
	// what the paths say; but with a year, or over a name that is only
	// near, it wins.
	idScore = 40
	// tagWeight is how much a name from the title or show tag counts
	// beside the paths' name: clearly less, since a tag may be another
	// title's (a file re-used, a wrong release name), and a wrong tag must
	// not beat the paths' own name. Alone (no name from the paths), it
	// counts fully.
	tagWeight = 0.6
	// tagYearSim is how alike a candidate's name must be to the tag's,
	// and to the paths' name, for the tag's year to count when the paths
	// say none: the tag then only adds the year to a name both say.
	tagYearSim = 0.9
	// runtimeScore is for a film whose length is TMDB's runtime (within
	// runtimeNear); runtimeOffScore for one far from it (beyond
	// runtimeFar): another cut is rarely that far.
	runtimeScore    = 10
	runtimeOffScore = -10
	// lengthsScore is for a series whose episodes' lengths are mostly
	// TMDB's (lengthsFitShare of them within lengthNear); lengthsOffScore
	// for one where few are (under lengthsOffShare).
	lengthsScore    = 10
	lengthsOffScore = -10
)

// The tolerances on lengths, in minutes, and as a share of TMDB's
// length (the larger counts): TMDB's runtimes are rounded, and a film's
// cuts differ by some minutes.
const (
	runtimeNear, runtimeNearShare = 8, 0.10
	runtimeFar, runtimeFarShare   = 20, 0.25
	lengthNear, lengthNearShare   = 4, 0.15
	lengthsFitShare               = 0.6
	lengthsOffShare               = 0.25
	// lengthsSeasons is how many of a series' seasons on disk are fetched
	// from TMDB to compare lengths: two say enough, and keep the requests
	// few (each season is fetched once ever, through the cache).
	lengthsSeasons = 2
)

// Named is a name with its year (0 when unknown).
type Named struct {
	Name string
	Year int
}

// Episode is an episode's place: its season and number.
type Episode struct{ Season, Number int }

// The main audio track's language is not a signal, though it was meant
// as a weak one (for a title whose original language it is). Measured on
// the evaluator's library: with a small part for it, films were matched
// worse (0.4 to 0.7 points on the runs without a year: dubbed copies of
// a same-named title tip the balance their way) and series hardly better
// (0.2 points). inside.File still reads it.
//
// Chapters are not a signal either. Measured on the evaluator's library: TMDB
// knows nothing of chapters, so they can't tell two titles apart, and as
// a sign of a film or an episode they say less than the length (films
// from 0 to 30+ chapters, episodes too).

// AddInside adds to q what the title's files say of themselves; look
// gives what one video says (false when it isn't known). IDs the paths
// gave are kept over the tags'.
func AddInside(q *Query, t *analyse.Title, look func(path string) (inside.File, bool)) {
	var files []inside.File
	var videos []*analyse.Video
	for _, v := range t.Videos {
		if f, ok := look(v.Path); ok {
			files = append(files, f)
			videos = append(videos, v)
		}
	}
	if len(files) == 0 {
		return
	}

	// IDs: the one most files give, when no file gives another.
	if q.TMDB == 0 {
		type kid struct {
			kind string
			id   int
		}
		ids := agreed(files, func(f inside.File) (kid, bool) { return kid{f.TMDBKind, f.TMDB}, f.TMDB > 0 })
		if ids.id > 0 {
			q.TMDB, q.TMDBKind = ids.id, tmdb.Kind(ids.kind)
		}
	}
	if q.IMDB == "" {
		q.IMDB = agreed(files, func(f inside.File) (string, bool) { return f.IMDB, f.IMDB != "" })
	}

	// Names: the show tag's, and the title tag's read as a release name.
	// A series' title tags are often its episodes' names: only a name
	// most of its tagged episodes share counts.
	for _, tag := range []func(inside.File) string{
		func(f inside.File) string { return f.Show },
		func(f inside.File) string { return f.Title },
	} {
		n, share := commonest(files, func(f inside.File) (Named, bool) {
			name, year, _ := analyse.ParseName(tag(f))
			return Named{name, year}, analyse.Key(name) != ""
		})
		if share > 0.5 && !slices.Contains(q.Tagged, n) {
			q.Tagged = append(q.Tagged, n)
		}
	}

	// Lengths: a film's whole video (not a part); an episode's file when
	// it is one episode.
	for i, f := range files {
		v := videos[i]
		if f.Duration <= 0 {
			continue
		}
		switch {
		case t.Kind == analyse.Show && v.Season >= 0 && len(v.Episodes) == 1:
			if q.Lengths == nil {
				q.Lengths = map[Episode]float64{}
			}
			q.Lengths[Episode{v.Season, v.Episodes[0]}] = f.Duration
		case t.Kind != analyse.Show && v.Part == 0 && q.Runtime == 0:
			q.Runtime = f.Duration
		}
	}
}

// agreed is the value most files give, when no more than a third of
// those that give one give another; the zero value otherwise.
func agreed[T comparable](files []inside.File, value func(inside.File) (T, bool)) T {
	v, share := commonest(files, value)
	if share < 2.0/3 {
		var zero T
		return zero
	}
	return v
}

// commonest is the value most files give, and the share of the files
// giving one that give it (0 when none does).
func commonest[T comparable](files []inside.File, value func(inside.File) (T, bool)) (T, float64) {
	counts := map[T]int{}
	var order []T
	n := 0
	for _, f := range files {
		if v, ok := value(f); ok {
			if counts[v] == 0 {
				order = append(order, v)
			}
			counts[v]++
			n++
		}
	}
	var best T
	if n == 0 {
		return best, 0
	}
	best = slices.MaxFunc(order, func(a, b T) int { return cmp.Compare(counts[a], counts[b]) })
	return best, float64(counts[best]) / float64(n)
}

// embedded finds the title the query's IDs name: by its TMDB ID (of the
// kind the tags say, or else the paths'), or by its IMDb ID. When both
// are given and name two titles, neither counts. nil for none.
func embedded(ctx context.Context, src tmdb.Source, q Query) (*tmdb.Details, error) {
	var byTMDB, byIMDb *tmdb.Found
	if q.TMDB > 0 {
		for _, kind := range kindsFor(q.TMDBKind, q.Kind) {
			d, err := src.Details(ctx, kind, q.TMDB)
			if errors.Is(err, tmdb.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			byTMDB = &d.Found
			break
		}
	}
	if q.IMDB != "" {
		found, err := src.FindIMDb(ctx, q.IMDB)
		if err != nil && !errors.Is(err, tmdb.ErrNotFound) {
			return nil, err
		}
		for _, kind := range kindsFor("", q.Kind) {
			if i := slices.IndexFunc(found, func(f tmdb.Found) bool { return f.Kind == kind }); i >= 0 {
				byIMDb = &found[i]
				break
			}
		}
	}
	var f *tmdb.Found
	switch {
	case byTMDB != nil && byIMDb != nil && (byTMDB.ID != byIMDb.ID || byTMDB.Kind != byIMDb.Kind):
		return nil, nil // the tags disagree
	case byTMDB != nil:
		f = byTMDB
	case byIMDb != nil:
		f = byIMDb
	default:
		return nil, nil
	}
	return src.Details(ctx, f.Kind, f.ID)
}

// kindsFor are the kinds to look an ID up in: the tag's, or the paths'
// first and then the other.
func kindsFor(tag tmdb.Kind, paths analyse.TitleKind) []tmdb.Kind {
	switch {
	case tag == tmdb.Movie || tag == tmdb.TV:
		return []tmdb.Kind{tag}
	case paths == analyse.Show:
		return []tmdb.Kind{tmdb.TV, tmdb.Movie}
	default:
		return []tmdb.Kind{tmdb.Movie, tmdb.TV}
	}
}

// runtimeFit weighs a film's length against TMDB's runtime.
func runtimeFit(seconds float64, runtime int) (int, string) {
	if seconds <= 0 || runtime <= 0 {
		return 0, ""
	}
	diff := math.Abs(seconds/60 - float64(runtime))
	switch {
	case diff <= max(runtimeNear, runtimeNearShare*float64(runtime)):
		return runtimeScore, "runtime"
	case diff > max(runtimeFar, runtimeFarShare*float64(runtime)):
		return runtimeOffScore, "runtime off"
	}
	return 0, ""
}

// lengthsFit weighs a series' episodes' lengths against TMDB's, for its
// first seasons on disk that TMDB has (lengthsSeasons of them, fetched
// once each through the cache).
func lengthsFit(ctx context.Context, src tmdb.Source, lengths map[Episode]float64, d *tmdb.Details) (int, string, error) {
	if len(lengths) == 0 || d.Kind != tmdb.TV {
		return 0, "", nil
	}
	has := map[int]bool{}
	for _, s := range d.Seasons {
		has[s.Number] = s.Episodes > 0
	}
	var seasons []int
	for e := range lengths {
		if e.Season > 0 && has[e.Season] && !slices.Contains(seasons, e.Season) {
			seasons = append(seasons, e.Season)
		}
	}
	slices.Sort(seasons)
	compared, fits := 0, 0
	for _, s := range seasons[:min(lengthsSeasons, len(seasons))] {
		eps, err := src.Season(ctx, d.ID, s)
		if errors.Is(err, tmdb.ErrNotFound) {
			continue
		}
		if err != nil {
			return 0, "", err
		}
		for _, e := range eps {
			sec, ok := lengths[Episode{s, e.Number}]
			if !ok || e.Runtime <= 0 {
				continue
			}
			compared++
			if math.Abs(sec/60-float64(e.Runtime)) <= max(lengthNear, lengthNearShare*float64(e.Runtime)) {
				fits++
			}
		}
	}
	if compared < 2 {
		return 0, "", nil
	}
	switch share := float64(fits) / float64(compared); {
	case share >= lengthsFitShare:
		return lengthsScore, "lengths fit", nil
	case share < lengthsOffShare:
		return lengthsOffScore, "lengths off", nil
	}
	return 0, "", nil
}
