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
//
// Stage 3 (AddInside) weighs what the files say of themselves too, when
// it is known: their ID tags, their lengths against TMDB's, and their
// title tags as other names (see inside.go).
package match

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/C0PT3R/reelid/analyse"
	"github.com/C0PT3R/reelid/tmdb"
)

// Query is what the paths say of a title.
type Query struct {
	Kind analyse.TitleKind // "" when unknown: both kinds are searched
	Name string
	Year int // 0 when unknown
	// Episodes are, for a series, the highest episode number on disk in
	// each season (season 0 is specials).
	Episodes map[int]int

	// What else is known, from the paths' ID tags or from inside the
	// files (see AddInside); every one is optional.
	//
	// TMDB and IMDB are the title's IDs (TMDBKind "" when the kind isn't
	// said).
	TMDB     int
	TMDBKind tmdb.Kind
	IMDB     string
	// Tagged are other names of the title, from the files' tags.
	Tagged []Named
	// Runtime is a film's length in seconds; Lengths a series' episodes'.
	Runtime float64
	Lengths map[Episode]float64
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
	// popularity breaks ties; nameSim is how alike its best name is;
	// slip whether the paths' name is near it only by a slip of a letter.
	popularity float64
	nameSim    float64
	slip       bool
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
	// weighedWithLengths are weighed when the files' lengths are known:
	// then more of the same name can be told apart by their lengths.
	weighedWithLengths = 6
	// weighedWide are weighed in all when the best few leave the match
	// unsure.
	weighedWide = 10
	// Sure from sureScore with sureMargin over the next; or, without a
	// year to confirm it, from nameScore with a wide margin.
	sureScore, sureMargin = 60, 15
	wideMargin            = 25
	// Candidates within closeMargin of the best are given to check.
	closeMargin = 15
)

// Match finds which TMDB title q is.
func Match(ctx context.Context, src tmdb.Source, q Query) (Result, error) {
	names := namesOf(q)
	id, err := embedded(ctx, src, q)
	if err != nil {
		return Result{}, err
	}
	if len(names) == 0 && id == nil {
		return Result{Confidence: None}, nil
	}
	kinds := []tmdb.Kind{tmdb.Movie, tmdb.TV}
	switch q.Kind {
	case analyse.Film:
		kinds = kinds[:1]
	case analyse.Show:
		kinds = kinds[1:]
	}
	w := weighing{q: q, names: names, id: id}
	cands, err := w.search(ctx, src, kinds, 0)
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
		more, err := w.search(ctx, src, []tmdb.Kind{other}, otherKind)
		if err != nil {
			return Result{}, err
		}
		cands = append(cands, more...)
		sortCandidates(cands)
	}
	return decide(cands), nil
}

// queryName is a name to search and weigh: the paths' (weight 1), or a
// tag's (tagWeight), with its year.
type queryName struct {
	key    string
	year   int
	weight float64
}

// namesOf are the query's names: the paths' first, then the tags' that
// say something more (another name, or a year the paths don't say).
func namesOf(q Query) []queryName {
	var out []queryName
	if key := analyse.Key(qualifier.ReplaceAllString(q.Name, "")); key != "" {
		out = append(out, queryName{key, q.Year, 1})
	}
	for _, t := range q.Tagged {
		key := analyse.Key(qualifier.ReplaceAllString(t.Name, ""))
		if key == "" || slices.ContainsFunc(out, func(n queryName) bool { return n.key == key && (n.year == t.Year || t.Year == 0) }) {
			continue
		}
		out = append(out, queryName{key, t.Year, tagWeight})
	}
	return out
}

// weighing is one query being matched: its names, and the title its IDs
// name (nil for none).
type weighing struct {
	q     Query
	names []queryName
	id    *tmdb.Details
}

// search searches the kinds by each name, weighs the results, and gives
// them best first. penalty is added to every score.
func (w *weighing) search(ctx context.Context, src tmdb.Source, kinds []tmdb.Kind, penalty int) ([]Candidate, error) {
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
	if w.id != nil && slices.Contains(kinds, w.id.Kind) {
		add([]tmdb.Found{w.id.Found})
	}
	// byPaths are the results the paths' name found (the first name).
	byPaths := map[tmdb.Kind]map[int]bool{}
	for i, n := range w.names {
		if i == 1 {
			for k, ids := range seen {
				byPaths[k] = maps.Clone(ids)
			}
		}
		for _, kind := range kinds {
			var withYear []tmdb.Found
			if n.year > 0 {
				list, err := src.Search(ctx, kind, n.key, n.year)
				if err != nil {
					return nil, err
				}
				withYear = list
				add(list)
			}
			// Without the year too, when the year found nothing named so:
			// the year may be one off, or not the one TMDB keeps.
			if n.year == 0 || !slices.ContainsFunc(withYear, func(f tmdb.Found) bool { return named(n.key, f) }) {
				list, err := src.Search(ctx, kind, n.key, 0)
				if err != nil {
					return nil, err
				}
				add(list)
			}
		}
	}

	// Nothing named so at all: the name may be misspelt ("Earths" for
	// "Earth", a season's number after it); it is searched again as it
	// would be without the slip.
	if len(w.names) > 0 && w.names[0].weight == 1 && !slices.ContainsFunc(found, func(f tmdb.Found) bool { return nearlyNamed(w.names[0].key, f) }) {
		for _, v := range variants(w.names[0].key) {
			for _, kind := range kinds {
				list, err := src.Search(ctx, kind, v, 0)
				if err != nil {
					return nil, err
				}
				add(list)
			}
		}
	}

	// A first weighing with the names the search gives; the best few are
	// then weighed again with all their names, seasons and lengths.
	cands := make([]Candidate, 0, len(found))
	for _, f := range found {
		cands = append(cands, w.weigh(&tmdb.Details{Found: f}, penalty, 0, ""))
	}
	sortCandidates(cands)
	// The best few are weighed closely; and, when tags gave other names,
	// the paths' name's best few too, so that a wrong tag can't crowd
	// them out before their translations are known.
	few := weighed
	if w.q.Runtime > 0 || len(w.q.Lengths) > 0 {
		few = weighedWithLengths
	}
	var close []int
	for i := range min(few, len(cands)) {
		close = append(close, i)
	}
	if len(w.names) > 1 {
		n := 0
		for i, c := range cands {
			if n == few {
				break
			}
			if len(byPaths) > 0 && byPaths[c.Kind][c.ID] {
				n++
				if !slices.Contains(close, i) {
					close = append(close, i)
				}
			}
		}
	}
	done := map[tmdb.Kind]map[int]bool{tmdb.Movie: {}, tmdb.TV: {}}
	weighClose := func(close []int) error {
		for _, i := range close {
			done[cands[i].Kind][cands[i].ID] = true
			d, err := src.Details(ctx, cands[i].Kind, cands[i].ID)
			if errors.Is(err, tmdb.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			d.Popularity = cands[i].popularity // from the search, as for every candidate
			fit, why, err := lengthsFit(ctx, src, w.q.Lengths, d)
			if err != nil {
				return err
			}
			cands[i] = w.weigh(d, penalty, fit, why)
		}
		sortCandidates(cands)
		return nil
	}
	if err := weighClose(close); err != nil {
		return nil, err
	}
	// Not sure yet: more are weighed closely, up to weighedWide, those of
	// the year said and those named as the paths say first: a title named
	// in another language (a translation the search found it by) or one
	// of several of one name is known only by its details.
	if decide(cands).Confidence != Sure {
		var more []int
		for _, pass := range []func(Candidate) bool{
			func(c Candidate) bool { return w.q.Year > 0 && c.Year == w.q.Year || c.nameSim >= 0.9 },
			func(Candidate) bool { return true },
		} {
			for i, c := range cands {
				if len(done[tmdb.Movie])+len(done[tmdb.TV])+len(more) >= weighedWide {
					break
				}
				if !done[c.Kind][c.ID] && !slices.Contains(more, i) && pass(c) {
					more = append(more, i)
				}
			}
		}
		if err := weighClose(more); err != nil {
			return nil, err
		}
	}
	return cands, nil
}

// nearlyNamed says whether a search result is named key or nearly so.
func nearlyNamed(key string, f tmdb.Found) bool {
	return similarity(key, analyse.Key(f.Name)) >= 0.9 || similarity(key, analyse.Key(f.OriginalName)) >= 0.9
}

// variants are a name as it would be without a common slip: a number
// after it (a season's, "Show 01"), a plural's "s" on its last word.
func variants(key string) []string {
	var out []string
	words := strings.Fields(key)
	if len(words) >= 2 && isNumber(words[len(words)-1]) {
		out = append(out, strings.Join(words[:len(words)-1], " "))
		words = words[:len(words)-1]
	}
	if last := words[len(words)-1]; len(words) >= 2 && len(last) > 3 && strings.HasSuffix(last, "s") {
		out = append(out, strings.Join(append(slices.Clone(words[:len(words)-1]), last[:len(last)-1]), " "))
	}
	return out
}

func isNumber(w string) bool {
	_, err := strconv.Atoi(w)
	return err == nil
}

// named says whether a search result is named key exactly.
func named(key string, f tmdb.Found) bool {
	return similarity(key, analyse.Key(f.Name)) == 1 || similarity(key, analyse.Key(f.OriginalName)) == 1
}

// weigh scores a TMDB title against the query; lengths (why) is how
// its episodes' lengths fit, weighed beforehand.
func (w *weighing) weigh(d *tmdb.Details, penalty, lengths int, lengthsWhy string) Candidate {
	q := w.q
	c := Candidate{ID: d.ID, Kind: d.Kind, Name: d.Name, Year: d.Year, Score: penalty, popularity: d.Popularity}
	if penalty != 0 {
		c.Why = append(c.Why, "other kind")
	}
	part := func(why string, n int) {
		c.Score += n
		c.Why = append(c.Why, why)
	}

	// The name: the paths', or a tag's (counting less), whichever fits
	// best. The year: the paths'; or, when they say none, a tag's, for a
	// candidate both names fit (see tagYearSim).
	// simOf is how alike a name is to this title's nearest name, and
	// whether only by a slip of a letter or two (see fuzzy).
	simOf := func(key string) (float64, bool) {
		sim, slip := 0.0, false
		for i, name := range d.Names() {
			k := analyse.Key(name)
			s := similarity(key, k)
			if i >= 2+len(d.Translations) {
				s *= altNameWeight
			}
			if s > sim {
				sim, slip = s, fuzzy(key, k)
			}
		}
		return sim, slip
	}
	paths, tagSim, tagYear := 0.0, 0.0, 0
	hasPaths := len(w.names) > 0 && w.names[0].weight == 1
	for _, n := range w.names {
		sim, slip := simOf(n.key)
		switch {
		case n.weight == 1:
			paths, c.slip = sim, slip
		case sim > tagSim || sim == tagSim && tagYear == 0:
			tagSim, tagYear = sim, n.year
		}
	}
	weight := tagWeight
	if !hasPaths {
		weight = 1 // the tags are all there is
	}
	c.nameSim = max(paths, weight*tagSim)
	if n := int(c.nameSim*nameScore + 0.5); n > 0 {
		if paths >= weight*tagSim {
			part("name", n)
		} else {
			part("name (tag)", n)
		}
	}
	switch {
	case q.Year > 0 && hasPaths && d.Year > 0:
		part(yearFit(q.Year, d.Year))
	case tagYear > 0 && d.Year > 0 && tagSim >= tagYearSim && (!hasPaths || paths >= tagYearSim):
		why, n := yearFit(tagYear, d.Year)
		part(why+" (tag)", n)
	}

	if w.id != nil && w.id.ID == d.ID && w.id.Kind == d.Kind {
		part("id", idScore)
	}
	if d.Kind == tmdb.TV && len(d.Seasons) > 0 {
		if n, why := seasonsFit(q.Episodes, d.Seasons); why != "" {
			part(why, n)
		}
	}
	if d.Kind == tmdb.Movie {
		if n, why := runtimeFit(q.Runtime, d.Runtime); why != "" {
			part(why, n)
		}
	}
	if lengthsWhy != "" {
		part(lengthsWhy, lengths)
	}
	return c
}

// yearFit weighs the year the names say against TMDB's.
func yearFit(year, tmdbYear int) (string, int) {
	switch diff := abs(year - tmdbYear); {
	case diff == 0:
		return "year", yearScore
	case diff == 1:
		return "year ±1", yearNearScore
	default:
		return "year off", yearOffScore
	}
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
	if (best.Score >= sureScore && margin >= sureMargin || best.Score >= nameScore && margin >= wideMargin) && !doubtful(best, cands[1:]) {
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

// doubtful says whether a candidate can't be sure whatever its score:
// when its length, its episodes' lengths or its seasons say it isn't
// the one and another is named as well (a remake, a namesake); or when
// the paths' name is near its only by a slip of a letter or two.
func doubtful(c Candidate, others []Candidate) bool {
	if c.slip && slices.Contains(c.Why, "name") {
		return true
	}
	for _, why := range c.Why {
		switch why {
		case "runtime off", "lengths off", "seasons misfit":
			return slices.ContainsFunc(others, func(o Candidate) bool { return o.nameSim >= c.nameSim })
		}
	}
	return false
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
	share := 0.5 * 2 * float64(common) / float64(len(wa)+len(wb))
	// A slip of a letter or two in a long enough name ("Earths" for
	// "Earth", "You" for "Your"): near, but never as much as the name.
	if slipped(a, b) {
		return max(share, fuzzySim)
	}
	return share
}

func slipped(a, b string) bool {
	return min(len(a), len(b)) >= minFuzzy && letterSimilarity(a, b) >= fuzzyRatio
}

// fuzzy says whether two names' keys are alike only by a slip of a
// letter or two (similarity's tolerance), not as the same name written
// otherwise.
func fuzzy(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = numerals(a), numerals(b)
	if a == b || strings.ReplaceAll(a, " ", "") == strings.ReplaceAll(b, " ", "") || withoutArticle(a) == withoutArticle(b) {
		return false
	}
	return slipped(a, b) && similarity(a, b) == fuzzySim
}

// The tolerance on names: two names at least minFuzzy letters long that
// differ by a letter or two (fuzzyRatio of their letters the same, by
// edit distance) are fuzzySim alike; a name so alike alone is never a
// sure match (see decide).
const (
	minFuzzy   = 8
	fuzzyRatio = 0.88
	fuzzySim   = 0.8
)

// letterSimilarity is 1 less the edit distance between two strings over
// the longer's length.
func letterSimilarity(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return 1 - float64(prev[len(rb)])/float64(max(len(ra), len(rb)))
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
	if id, err := strconv.Atoi(t.IDs.TMDB); err == nil && id > 0 {
		q.TMDB = id
	}
	q.IMDB = t.IDs.IMDB
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
