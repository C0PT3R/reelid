// Package match says which TMDB title an identified title is: stage 2
// of the analyser, after analyse has read the paths.
//
// TMDB is searched by the title's name, with its year and kind when the
// paths say them. Its results are weighed against what the paths say:
// the name (TMDB's, the original, translations and alternative titles),
// the year (one off is close), the kind, and for a series whether the
// seasons and episodes on disk fit TMDB's. Popularity only breaks ties.
// The best is taken, and said "sure" when it is clearly the one;
// otherwise the close candidates come with it, to check.
package match

import (
	"cmp"
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/overfin/analyser/analyse"
	"github.com/overfin/analyser/tmdb"
)

// Query is what the paths say of a title.
type Query struct {
	Kind analyse.TitleKind // "" when unknown: both kinds are searched
	Name string
	Year int // 0 when unknown
	// Episodes are, for a series, the highest episode number on disk in
	// each season (season 0 is specials).
	Episodes map[int]int
}

// Confidence says how sure a match is.
type Confidence string

// The confidences.
const (
	Sure  Confidence = "sure"     // clearly the one
	Check Confidence = "to check" // the best, but not clearly: see Others
	None  Confidence = "none"     // nothing found
)

// Candidate is a TMDB title weighed for a query.
type Candidate struct {
	ID    int       `json:"id"`
	Kind  tmdb.Kind `json:"kind"`
	Name  string    `json:"name"`
	Year  int       `json:"year,omitempty"`
	Score int       `json:"score"`
	// Why are the score's parts, for reading it ("name 50", "year 20"…).
	Why []string `json:"why,omitempty"`
	// popularity breaks ties; nameSim is how alike its best name is.
	popularity float64
	nameSim    float64
}

// qualifier is a country or a region after a series' name, as Sonarr
// writes them ("Name (US)"): not a part of TMDB's name.
var qualifier = regexp.MustCompile(`\s*[(\[][A-Za-z]{2}[)\]]\s*$`)

// Result is a match: the best candidate, how sure it is, and the other
// candidates close to it when it isn't sure.
type Result struct {
	Best       *Candidate  `json:"best,omitempty"`
	Confidence Confidence  `json:"confidence"`
	Others     []Candidate `json:"others,omitempty"`
	// All are every candidate weighed, best first, for looking into a
	// match.
	All []Candidate `json:"-"`
}

// The scores. A name that is one of the title's counts most; then the
// year, a series' seasons, and the kind when the paths didn't say it.
const (
	nameScore     = 50  // the name exactly; less for a near name
	altNameWeight = 0.9 // an alternative title counts a little less than a real name
	yearScore     = 20  // the same year
	yearNearScore = 12  // one year off
	yearOffScore  = -20 // further off
	fitScore      = 15  // the seasons and episodes on disk are TMDB's
	overScore     = -10 // an episode number beyond TMDB's count
	misfitScore   = -25 // a season TMDB doesn't have
	otherKind     = -15 // the other kind than the paths said
)

// The search's limits: how many results are weighed closely (their
// details fetched), and when a match is sure.
const (
	weighed = 3
	// Sure from sureScore with sureMargin over the next; or, without a
	// year to confirm it, from nameScore with a wide margin.
	sureScore, sureMargin = 60, 15
	wideMargin            = 25
	// Candidates within closeMargin of the best are given to check.
	closeMargin = 15
)

// Match finds which TMDB title q is.
func Match(ctx context.Context, src tmdb.Source, q Query) (Result, error) {
	key := analyse.Key(qualifier.ReplaceAllString(q.Name, ""))
	if key == "" {
		return Result{Confidence: None}, nil
	}
	kinds := []tmdb.Kind{tmdb.Movie, tmdb.TV}
	switch q.Kind {
	case analyse.Film:
		kinds = kinds[:1]
	case analyse.Show:
		kinds = kinds[1:]
	}
	cands, err := search(ctx, src, q, key, kinds, 0)
	if err != nil {
		return Result{}, err
	}
	// The paths' kind may be wrong (a film in a series' layout): when no
	// candidate has the name, the other kind is searched too, a little
	// behind. (Only the name: a series whose seasons don't fit TMDB's,
	// numbered from its first episode, is still a series.)
	if len(kinds) == 1 && !slices.ContainsFunc(cands, func(c Candidate) bool { return c.nameSim >= 0.9 }) {
		other := tmdb.TV
		if kinds[0] == tmdb.TV {
			other = tmdb.Movie
		}
		more, err := search(ctx, src, q, key, []tmdb.Kind{other}, otherKind)
		if err != nil {
			return Result{}, err
		}
		cands = append(cands, more...)
		sortCandidates(cands)
	}
	return decide(cands), nil
}

// search searches the kinds, weighs the results, and gives them best
// first. penalty is added to every score.
func search(ctx context.Context, src tmdb.Source, q Query, key string, kinds []tmdb.Kind, penalty int) ([]Candidate, error) {
	var found []tmdb.Found
	seen := map[tmdb.Kind]map[int]bool{}
	add := func(list []tmdb.Found) {
		for _, f := range list {
			if seen[f.Kind] == nil {
				seen[f.Kind] = map[int]bool{}
			}
			if !seen[f.Kind][f.ID] {
				seen[f.Kind][f.ID] = true
				found = append(found, f)
			}
		}
	}
	for _, kind := range kinds {
		var withYear []tmdb.Found
		if q.Year > 0 {
			list, err := src.Search(ctx, kind, key, q.Year)
			if err != nil {
				return nil, err
			}
			withYear = list
			add(list)
		}
		// Without the year too, when the year found nothing named so: the
		// year may be one off, or not the one TMDB keeps.
		if q.Year == 0 || !slices.ContainsFunc(withYear, func(f tmdb.Found) bool { return named(key, f) }) {
			list, err := src.Search(ctx, kind, key, 0)
			if err != nil {
				return nil, err
			}
			add(list)
		}
	}

	// A first weighing with the names the search gives; the best few are
	// then weighed again with all their names and seasons.
	cands := make([]Candidate, 0, len(found))
	for _, f := range found {
		cands = append(cands, weigh(q, key, &tmdb.Details{Found: f}, penalty))
	}
	sortCandidates(cands)
	for i := range min(weighed, len(cands)) {
		d, err := src.Details(ctx, cands[i].Kind, cands[i].ID)
		if errors.Is(err, tmdb.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		d.Popularity = cands[i].popularity // from the search, as for every candidate
		cands[i] = weigh(q, key, d, penalty)
	}
	sortCandidates(cands)
	return cands, nil
}

// named says whether a search result is named key exactly.
func named(key string, f tmdb.Found) bool {
	return similarity(key, analyse.Key(f.Name)) == 1 || similarity(key, analyse.Key(f.OriginalName)) == 1
}

// weigh scores a TMDB title against the query.
func weigh(q Query, key string, d *tmdb.Details, penalty int) Candidate {
	c := Candidate{ID: d.ID, Kind: d.Kind, Name: d.Name, Year: d.Year, Score: penalty, popularity: d.Popularity}
	if penalty != 0 {
		c.Why = append(c.Why, "other kind")
	}
	part := func(why string, n int) {
		c.Score += n
		c.Why = append(c.Why, why)
	}

	best := 0.0
	for i, name := range d.Names() {
		s := similarity(key, analyse.Key(name))
		if i >= 2+len(d.Translations) {
			s *= altNameWeight
		}
		best = max(best, s)
	}
	c.nameSim = best
	if n := int(best*nameScore + 0.5); n > 0 {
		part("name", n)
	}

	if q.Year > 0 && d.Year > 0 {
		switch diff := abs(q.Year - d.Year); {
		case diff == 0:
			part("year", yearScore)
		case diff == 1:
			part("year ±1", yearNearScore)
		default:
			part("year off", yearOffScore)
		}
	}

	if d.Kind == tmdb.TV && len(d.Seasons) > 0 {
		if n, why := seasonsFit(q.Episodes, d.Seasons); why != "" {
			part(why, n)
		}
	}
	return c
}

// seasonsFit weighs the seasons on disk against TMDB's: each season must
// be one of TMDB's (specials aside), with no episode number beyond its
// count. "" when there is nothing to weigh.
func seasonsFit(disk map[int]int, seasons []tmdb.Season) (int, string) {
	counts := map[int]int{}
	for _, s := range seasons {
		counts[s.Number] = s.Episodes
	}
	weighed, over := 0, false
	for season, last := range disk {
		if season <= 0 {
			continue // specials, or no season said
		}
		weighed++
		n, ok := counts[season]
		if !ok {
			return misfitScore, "seasons misfit"
		}
		if last > n {
			over = true
		}
	}
	switch {
	case weighed == 0:
		return 0, ""
	case over:
		return overScore, "episodes over"
	default:
		return fitScore, "seasons fit"
	}
}

// decide takes the best candidate and says how sure it is.
func decide(cands []Candidate) Result {
	if len(cands) == 0 {
		return Result{Confidence: None}
	}
	best := cands[0]
	res := Result{Best: &best, Confidence: Check, All: cands}
	margin := best.Score // over nothing
	if len(cands) > 1 {
		margin = best.Score - cands[1].Score
	}
	if best.Score >= sureScore && margin >= sureMargin || best.Score >= nameScore && margin >= wideMargin {
		res.Confidence = Sure
		return res
	}
	for _, c := range cands[1:] {
		if best.Score-c.Score <= closeMargin {
			res.Others = append(res.Others, c)
		}
	}
	return res
}

// sortCandidates puts the best first: by score, then by popularity, then
// TMDB's order (the sort is stable).
func sortCandidates(cands []Candidate) {
	slices.SortStableFunc(cands, func(a, b Candidate) int {
		return cmp.Or(b.Score-a.Score, cmp.Compare(b.popularity, a.popularity))
	})
}

// similarity says how alike two names' keys are, from 0 to 1: 1 for the
// same name; nearly 1 for the same name written apart or together
// ("spider man", "spiderman"), or with or without its article; less for
// a name that starts the other ("heat" in "heat wave"); then the share
// of words in common.
func similarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	a, b = numerals(a), numerals(b)
	switch {
	case a == b:
		return 1
	case strings.ReplaceAll(a, " ", "") == strings.ReplaceAll(b, " ", ""):
		return 0.95
	case withoutArticle(a) == withoutArticle(b):
		return 0.9
	}
	wa, wb := strings.Fields(a), strings.Fields(b)
	if len(wa) > len(wb) {
		wa, wb = wb, wa
	}
	if slices.Equal(wa, wb[:len(wa)]) {
		return 0.6
	}
	common := 0
	for _, w := range wa {
		if slices.Contains(wb, w) {
			common++
		}
	}
	return 0.5 * 2 * float64(common) / float64(len(wa)+len(wb))
}

// articles are the words that may start a name or not ("The Name",
// "Name"), in the languages of most libraries.
var articles = []string{"the", "a", "an", "le", "la", "les", "l", "un", "une", "el", "los", "las", "il", "der", "die", "das"}

func withoutArticle(key string) string {
	first, rest, ok := strings.Cut(key, " ")
	if ok && slices.Contains(articles, first) {
		return rest
	}
	return key
}

// numerals writes a key's Roman numerals from II to X as digits, so that
// "Part II" and "Part 2" are one. "I" alone stays: it's mostly a word.
func numerals(key string) string {
	words := strings.Fields(key)
	for i, w := range words {
		if n, ok := roman[w]; ok && i > 0 {
			words[i] = n
		}
	}
	return strings.Join(words, " ")
}

var roman = map[string]string{"ii": "2", "iii": "3", "iv": "4", "v": "5", "vi": "6", "vii": "7", "viii": "8", "ix": "9", "x": "10"}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// QueryOf is the query for an analysed title: its kind, name and year,
// and a series' highest episode number per season.
func QueryOf(t *analyse.Title) Query {
	q := Query{Kind: t.Kind, Name: t.Name, Year: t.Year}
	if t.Kind == analyse.Show {
		q.Episodes = map[int]int{}
		for _, v := range t.Videos {
			if v.Season < 0 {
				continue
			}
			last := 0
			if len(v.Episodes) > 0 {
				last = v.Episodes[len(v.Episodes)-1]
			}
			q.Episodes[v.Season] = max(q.Episodes[v.Season], last)
		}
	}
	return q
}
