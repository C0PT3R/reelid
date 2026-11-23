package match

import (
	"context"
	"errors"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/C0PT3R/reelid/analyse"
	"github.com/C0PT3R/reelid/inside"
	"github.com/C0PT3R/reelid/tmdb"
)

// Placing a series' episodes: once the series is known, which of TMDB's
// episodes each file is, in TMDB's default order. The numbers the names
// write are most often TMDB's; but a release may number its episodes
// another way:
//
//   - absolutely, from the series' first episode ("Show 783" in a pack
//     of season 19), or with no season at all ("Show E05");
//   - by broadcasts where TMDB lists the segments ("S05E01" holding
//     TMDB's episodes 1 and 2, two eleven-minute cartoons), or in two
//     parts where TMDB has one double episode;
//   - in another of the series' orders (a DVD's, a streaming service's
//     seasons: "S14" where TMDB's default has eleven).
//
// Each way is tried on a season's files only when the numbers as written
// don't fit TMDB's default: an episode number beyond the season's count,
// a season TMDB doesn't have, or the files' lengths far from TMDB's. A
// way is taken when it places every file and the files' lengths and
// names agree with it clearly better; otherwise the numbers stay as
// written.

// Place is where a video's episodes are in TMDB's default order.
type Place struct {
	Season   int   `json:"season"`
	Episodes []int `json:"episodes,omitempty"`
	// How says how they were placed: AsWritten, InSeason, Absolute,
	// ByLengths, Ordered, or Unplaced (as written, though they don't fit TMDB's).
	How string `json:"how"`
}

// The ways of placing.
const (
	AsWritten = "as written"
	Absolute  = "absolute"
	InSeason  = "in season"
	ByLengths = "by lengths"
	Ordered   = "order"
	Unplaced  = "unplaced"
)

// The thresholds of placing: a way fits when this share of its files'
// lengths and names agree; another way than the numbers as written must
// leave at most half their disagreements.
const (
	placeFit = 0.6
	// nameWeight is how much an episode's name in a file name counts
	// beside its length: a name is nearly proof, a length only a sign.
	nameWeight = 2
	// lengthsKnown is how many files' lengths a season needs for them to
	// say anything.
	lengthsKnown = 2
)

// placer places one series' videos.
type placer struct {
	ctx    context.Context
	src    tmdb.Source
	id     int
	counts map[int]int // TMDB's default seasons' episode counts
	// seasons are TMDB's default seasons, fetched when needed (nil for
	// none).
	seasons map[int][]tmdb.Episode
	orders  []tmdb.Order
	ordered bool // whether orders were fetched
}

// file is a video being placed.
type file struct {
	v       *analyse.Video
	minutes float64 // its length; 0 when unknown
}

// Places says where each of a series' videos (by path) is in TMDB's
// default order; id is the series' TMDB ID, look what a video says of
// itself (its length), when known.
func Places(ctx context.Context, src tmdb.Source, t *analyse.Title, id int, look func(path string) (inside.File, bool)) (map[string]Place, error) {
	out := map[string]Place{}
	d, err := src.Details(ctx, tmdb.TV, id)
	if errors.Is(err, tmdb.ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	p := &placer{ctx: ctx, src: src, id: id, counts: map[int]int{}, seasons: map[int][]tmdb.Episode{}}
	for _, s := range d.Seasons {
		p.counts[s.Number] = s.Episodes
	}
	// The videos by the season their names say (-1 for none).
	bySeason := map[int][]file{}
	var order []int
	for _, v := range t.Videos {
		if len(v.Episodes) == 0 {
			out[v.Path] = Place{Season: v.Season, How: AsWritten}
			continue
		}
		f := file{v: v}
		if look != nil {
			if in, ok := look(v.Path); ok && in.Duration > 0 {
				f.minutes = in.Duration / 60
			}
		}
		if _, ok := bySeason[v.Season]; !ok {
			order = append(order, v.Season)
		}
		bySeason[v.Season] = append(bySeason[v.Season], f)
	}
	for _, s := range order {
		files := bySeason[s]
		slices.SortStableFunc(files, func(a, b file) int { return a.v.Episodes[0] - b.v.Episodes[0] })
		places, how, err := p.season(s, files)
		if err != nil {
			return nil, err
		}
		for i, f := range files {
			pl := Place{Season: f.v.Season, Episodes: f.v.Episodes, How: how}
			if places != nil {
				pl.Season, pl.Episodes = places[i][0].Season, nil
				for _, e := range places[i] {
					pl.Episodes = append(pl.Episodes, e.Number)
				}
			}
			out[f.v.Path] = pl
		}
	}
	return out, nil
}

// placing is a way of placing a season's files: each file's TMDB
// episodes (nil for a file it can't place).
type placing [][]tmdb.Placed

// season places one season's files (by the season their names say),
// sorted by their numbers: their TMDB episodes, and how; nil for as
// written.
func (p *placer) season(s int, files []file) (placing, string, error) {
	if s == 0 {
		return nil, AsWritten, nil // specials: TMDB's are a jumble, kept as written
	}
	written, err := p.asWritten(s, files)
	if err != nil {
		return nil, "", err
	}
	all := written.placesAll()
	fit, known := p.fit(files, written)
	if all && (!known || fit >= placeFit) {
		return nil, AsWritten, nil
	}

	// Other ways, cheapest first; the first that fits clearly better is
	// taken.
	better := func(pl placing) bool {
		if pl == nil || !pl.placesAll() {
			return false
		}
		f, k := p.fit(files, pl)
		if !k {
			// Nothing to weigh it by: only when the numbers as written
			// can't be TMDB's.
			return !all
		}
		// Clearly better than the numbers as written, where they fit at
		// all: at most half their disagreements left (a file or two beyond
		// TMDB's count don't make another way right); where they don't
		// place every file, as good is enough.
		gain := f > fit || !all && f >= fit
		return f >= placeFit && (!known || gain && 1-f <= (1-fit)/2)
	}
	ways := []struct {
		how   string
		place func(int, []file) (placing, error)
	}{
		{Absolute, func(_ int, files []file) (placing, error) { return p.writtenAbsolute(files) }},
		{InSeason, p.inSeason},
		{Absolute, p.absolute},
		{ByLengths, p.byLengths},
	}
	for _, w := range ways {
		pl, err := w.place(s, files)
		if err != nil {
			return nil, "", err
		}
		if better(pl) {
			return pl, w.how, nil
		}
	}
	o, err := p.otherOrder(s, files, better)
	if err != nil {
		return nil, "", err
	}
	if o != nil {
		return o, Ordered, nil
	}
	if all {
		return nil, AsWritten, nil
	}
	return nil, Unplaced, nil
}

func (pl placing) placesAll() bool {
	return !slices.ContainsFunc(pl, func(e []tmdb.Placed) bool { return len(e) == 0 })
}

// episodes are a TMDB default season's episodes, fetched once.
func (p *placer) episodes(s int) ([]tmdb.Episode, error) {
	if eps, ok := p.seasons[s]; ok {
		return eps, nil
	}
	var eps []tmdb.Episode
	if p.counts[s] > 0 {
		var err error
		eps, err = p.src.Season(p.ctx, p.id, s)
		if err != nil && !errors.Is(err, tmdb.ErrNotFound) {
			return nil, err
		}
	}
	p.seasons[s] = eps
	return eps, nil
}

// at is TMDB's default season s's episode numbered n; false when it has
// none. (Most seasons number their episodes from 1; some go on from the
// season before: a long series' "S21" listing episodes 892 to 1088.)
func (p *placer) at(s, n int) (tmdb.Placed, bool, error) {
	eps, err := p.episodes(s)
	if err != nil {
		return tmdb.Placed{}, false, err
	}
	if len(eps) == 0 {
		// Counted but not listed: placed by the count, with nothing to
		// weigh it by.
		return tmdb.Placed{Season: s, Number: n}, n >= 1 && n <= p.counts[s], nil
	}
	for _, e := range eps {
		if e.Number == n {
			return placedOf(s, e), true, nil
		}
	}
	return tmdb.Placed{}, false, nil
}

// nth is TMDB's default season s's k-th episode (from 1), whatever its
// number; false when it has none.
func (p *placer) nth(s, k int) (tmdb.Placed, bool, error) {
	eps, err := p.episodes(s)
	if err != nil {
		return tmdb.Placed{}, false, err
	}
	if len(eps) == 0 {
		return tmdb.Placed{Season: s, Number: k}, k >= 1 && k <= p.counts[s], nil
	}
	if k < 1 || k > len(eps) {
		return tmdb.Placed{}, false, nil
	}
	return placedOf(s, eps[k-1]), true, nil
}

// numbered says whether TMDB's season s numbers its episodes from 1.
func (p *placer) numbered(s int) (bool, error) {
	eps, err := p.episodes(s)
	if err != nil {
		return false, err
	}
	return len(eps) == 0 || eps[0].Number <= 1, nil
}

// each places every file's numbers by one lookup; a file is placed when
// all its numbers are.
func each(files []file, lookup func(n int) (tmdb.Placed, bool, error)) (placing, error) {
	out := make(placing, len(files))
	for i, f := range files {
		var eps []tmdb.Placed
		for _, n := range f.v.Episodes {
			e, ok, err := lookup(n)
			if err != nil {
				return nil, err
			}
			if !ok {
				eps = nil
				break
			}
			eps = append(eps, e)
		}
		out[i] = eps
	}
	return out, nil
}

// asWritten places the files where their numbers say.
func (p *placer) asWritten(s int, files []file) (placing, error) {
	if s < 1 || p.counts[s] == 0 {
		return make(placing, len(files)), nil
	}
	return each(files, func(n int) (tmdb.Placed, bool, error) { return p.at(s, n) })
}

// inSeason places the files by their numbers as places in their season,
// for a season TMDB numbers on from the season before ("S21E02" is its
// second episode, numbered 893).
func (p *placer) inSeason(s int, files []file) (placing, error) {
	if s < 1 || p.counts[s] == 0 {
		return nil, nil
	}
	if ok, err := p.numbered(s); err != nil || ok {
		return nil, err
	}
	return each(files, func(n int) (tmdb.Placed, bool, error) { return p.nth(s, n) })
}

// absolute places the files by their numbers counted from the series'
// first episode (specials left out): in the season that lists that
// number, for seasons TMDB numbers on from the one before, or else
// through the seasons' counts. Only for files with no season, or
// numbers beyond their season's count.
func (p *placer) absolute(s int, files []file) (placing, error) {
	// Every number beyond its season's count; or, for a season TMDB
	// doesn't have, beyond its first season's. (A few numbers beyond are
	// rather episodes TMDB doesn't list yet.)
	limit := p.counts[s]
	if limit == 0 {
		limit = p.counts[1]
	}
	beyond := true
	for _, f := range files {
		beyond = beyond && f.v.Episodes[0] > limit
	}
	if s >= 1 && !beyond {
		return nil, nil
	}
	var seasons []int
	for n := range p.counts {
		if n > 0 && p.counts[n] > 0 {
			seasons = append(seasons, n)
		}
	}
	slices.Sort(seasons)
	return each(files, func(n int) (tmdb.Placed, bool, error) { return p.absoluteAt(seasons, n) })
}

// writtenAbsolute places the files by the absolute numbers their names
// write beside their season's ("0749 Show S18E03"), when all do.
func (p *placer) writtenAbsolute(files []file) (placing, error) {
	if slices.ContainsFunc(files, func(f file) bool { return f.v.Absolute == 0 }) {
		return nil, nil
	}
	var seasons []int
	for n := range p.counts {
		if n > 0 && p.counts[n] > 0 {
			seasons = append(seasons, n)
		}
	}
	slices.Sort(seasons)
	out := make(placing, len(files))
	for i, f := range files {
		e, ok, err := p.absoluteAt(seasons, f.v.Absolute)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out[i] = []tmdb.Placed{e}
		for k := 1; k < len(f.v.Episodes); k++ {
			e, ok, err := p.absoluteAt(seasons, f.v.Absolute+k)
			if err != nil {
				return nil, err
			}
			if !ok {
				out[i] = nil
				break
			}
			out[i] = append(out[i], e)
		}
	}
	return out, nil
}

// absoluteAt is the episode numbered n from the series' first: in the
// season that lists that number, for seasons TMDB numbers on from the one
// before; or else through the seasons' counts (seasons, in order).
func (p *placer) absoluteAt(seasons []int, n int) (tmdb.Placed, bool, error) {
	for _, season := range seasons {
		if ok, err := p.numbered(season); err != nil {
			return tmdb.Placed{}, false, err
		} else if ok {
			continue
		}
		if e, ok, err := p.at(season, n); err != nil || ok {
			return e, ok, err
		}
	}
	left := n
	for _, season := range seasons {
		if left <= p.counts[season] {
			return p.nth(season, left)
		}
		left -= p.counts[season]
	}
	return tmdb.Placed{}, false, nil
}

// The costs of aligning files with episodes by their lengths (see
// byLengths): a run whose lengths disagree costs 1; an episode skipped
// that the files' numbers don't skip (or the other way) costs skipCost;
// the first file's distance from where its number says, leadCost an
// episode.
const (
	skipCost = 0.3
	leadCost = 0.05
	maxSkip  = 6
)

// byLengths aligns the files, in the order of their numbers, with TMDB's
// season's episodes by their lengths: a file may be one episode, two or
// three (segments shown in one broadcast: "S05E01" holding TMDB's
// episodes 1 and 2, two eleven-minute cartoons), or two files one
// episode (a double episode released in two parts). Episodes may be
// missing between files where their numbers skip. Copies (files of the
// same numbers) are placed as the first.
func (p *placer) byLengths(s int, files []file) (placing, error) {
	known := 0
	for _, f := range files {
		if f.minutes > 0 {
			known++
		}
	}
	if known < lengthsKnown || p.counts[s] == 0 {
		return nil, nil
	}
	eps, err := p.episodes(s)
	if err != nil || len(eps) == 0 {
		return nil, err
	}
	for _, e := range eps {
		if e.Runtime <= 0 {
			return nil, nil // nothing to align by
		}
	}
	// The distinct files, by their numbers.
	var uniq []int // indexes in files
	for i, f := range files {
		if len(uniq) > 0 && slices.Equal(files[uniq[len(uniq)-1]].v.Episodes, f.v.Episodes) {
			continue
		}
		uniq = append(uniq, i)
	}
	n, m := len(uniq), len(eps)
	first := func(u int) int { return files[uniq[u]].v.Episodes[0] }
	last := func(u int) int { e := files[uniq[u]].v.Episodes; return e[len(e)-1] }
	mins := func(u int) float64 { return files[uniq[u]].minutes }
	runtime := func(j, k int) float64 { // episodes j..k-1
		sum := 0
		for _, e := range eps[j:k] {
			sum += e.Runtime
		}
		return float64(sum)
	}
	// agree is the cost of lengths d against runtime r.
	agree := func(d, r float64) float64 {
		if d <= 0 {
			return 1
		}
		if math.Abs(d-r) <= max(lengthNear, lengthNearShare*r) {
			return 0.01 * math.Abs(d-r) / r
		}
		return 1
	}
	// cost[u][j]: the files before u placed, the episodes before j used.
	inf := math.Inf(1)
	cost := make([][]float64, n+1)
	type step struct{ u, j, files, eps int }
	from := make([][]step, n+1)
	for u := range cost {
		cost[u] = make([]float64, m+1)
		from[u] = make([]step, m+1)
		for j := range cost[u] {
			cost[u][j] = inf
		}
	}
	for j := 0; j <= m; j++ {
		cost[0][j] = leadCost * math.Abs(float64(j-(first(0)-1)))
	}
	for u := 0; u < n; u++ {
		for j := 0; j <= m; j++ {
			if cost[u][j] == inf {
				continue
			}
			gap := 0
			if u > 0 {
				gap = first(u) - last(u-1) - 1
			}
			for skip := 0; skip <= maxSkip && j+skip <= m; skip++ {
				c0 := cost[u][j]
				if u > 0 {
					c0 += skipCost * math.Abs(float64(skip-max(gap, 0)))
				}
				start := j + skip
				try := func(nf, ne int, c float64) {
					if u+nf > n || start+ne > m {
						return
					}
					if c0+c < cost[u+nf][start+ne] {
						cost[u+nf][start+ne] = c0 + c
						from[u+nf][start+ne] = step{u, j, nf, ne}
					}
				}
				if start < m {
					if mins(u) <= 0 {
						try(1, 1, 0) // unknown length: as one episode
					} else {
						for ne := 1; ne <= 3; ne++ {
							if start+ne <= m {
								try(1, ne, agree(mins(u), runtime(start, start+ne)))
							}
						}
						// Two parts of one episode, numbered one after the other.
						if u+1 < n && first(u+1) == last(u)+1 && mins(u+1) > 0 {
							try(2, 1, agree(mins(u)+mins(u+1), runtime(start, start+1)))
						}
					}
				}
			}
		}
	}
	end := slices.Index(cost[n], slices.Min(cost[n]))
	if cost[n][end] == inf {
		return nil, nil
	}
	runs := make([][]tmdb.Placed, n)
	for u, j := n, end; u > 0; {
		st := from[u][j]
		start := j - st.eps
		var run []tmdb.Placed
		for _, e := range eps[start:j] {
			run = append(run, placedOf(s, e))
		}
		for k := st.u; k < st.u+st.files; k++ {
			runs[k] = run
		}
		u, j = st.u, st.j
	}
	out := make(placing, len(files))
	for u, i := range uniq {
		next := len(files)
		if u+1 < n {
			next = uniq[u+1]
		}
		for k := i; k < next; k++ {
			out[k] = runs[u]
		}
	}
	return out, nil
}

func placedOf(s int, e tmdb.Episode) tmdb.Placed {
	return tmdb.Placed{Season: s, Number: e.Number, Runtime: e.Runtime, Name: e.Name, AirDate: e.AirDate}
}

// orderTypes are the kinds of orders tried, in the order they are
// preferred on a tie: those that number seasons as releases do. Story
// arcs aren't seasons, and absolute numbering is tried apart.
var orderTypes = []tmdb.OrderType{tmdb.Digital, tmdb.DVD, tmdb.Production, tmdb.OriginalAirDate, tmdb.TVOrder}

// seasonNumber is a number in an order's season's name ("Season 4",
// "Part 4", "Saison 4").
var seasonNumber = regexp.MustCompile(`\d+`)

// otherOrder places the files through the series' other orders (TMDB's
// episode groups, fetched only now): the order's season that has the
// files' season's number, by its name or its place. The best that fits
// (by better) is taken; nil when none does.
func (p *placer) otherOrder(s int, files []file, better func(placing) bool) (placing, error) {
	if s < 1 {
		return nil, nil
	}
	if !p.ordered {
		p.ordered = true
		orders, err := p.src.Orders(p.ctx, p.id)
		if err != nil && !errors.Is(err, tmdb.ErrNotFound) {
			return nil, err
		}
		p.orders = orders
	}
	var best placing
	bestFit := -1.0
	for _, kind := range orderTypes {
		for _, o := range p.orders {
			if o.Type != kind {
				continue
			}
			seasons, err := p.src.OrderSeasons(p.ctx, o.ID)
			if errors.Is(err, tmdb.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			group := orderSeason(seasons, s)
			if group == nil {
				continue
			}
			pl := make(placing, len(files))
			for i, f := range files {
				var eps []tmdb.Placed
				for _, n := range f.v.Episodes {
					// Not into the specials: an order's season holding
					// TMDB's specials is rather a part of them.
					if n < 1 || n > len(group.Episodes) || group.Episodes[n-1].Season == 0 {
						eps = nil
						break
					}
					eps = append(eps, group.Episodes[n-1])
				}
				pl[i] = eps
			}
			if !better(pl) {
				continue
			}
			f, k := p.fit(files, pl)
			if k && f > bestFit || !k && best == nil {
				best, bestFit = pl, f
			}
		}
	}
	return best, nil
}

// orderSeason is an order's season s: the one its name numbers so, or
// else the one at that place (its order); nil for none.
func orderSeason(seasons []tmdb.OrderSeason, s int) *tmdb.OrderSeason {
	for i, g := range seasons {
		if m := seasonNumber.FindAllString(g.Name, -1); len(m) == 1 {
			if n, _ := strconv.Atoi(m[0]); n == s {
				return &seasons[i]
			}
		}
	}
	for i, g := range seasons {
		if g.Order == s && !seasonNumber.MatchString(g.Name) {
			return &seasons[i]
		}
	}
	return nil
}

// fit says how well a placing fits the files: the share of what they
// say (lengths, and names, which count nameWeight times) that agrees
// with the episodes it gives them; known is false when they tell nothing
// it can be weighed by.
//
// A file's name counts only when it is one of TMDB's names for the
// series' episodes (in the seasons fetched, or in this placing): a name
// in another language than TMDB's, or a release's words read as a name,
// says nothing either way.
func (p *placer) fit(files []file, pl placing) (share float64, known bool) {
	var names []string
	for _, eps := range p.seasons {
		for _, e := range eps {
			names = append(names, e.Name)
		}
	}
	for _, eps := range pl {
		for _, e := range eps {
			names = append(names, e.Name)
		}
	}
	tmdbName := func(name string) bool {
		return slices.ContainsFunc(names, func(n string) bool { return SameEpisodeName(name, n) })
	}
	agree, said := 0.0, 0.0
	lengths := 0
	for i, f := range files {
		eps := pl[i]
		if len(eps) == 0 {
			continue
		}
		// Parts of one episode (files numbered one after the other, placed
		// alike): their lengths are weighed together, at the first.
		if i > 0 && samePlaces(pl[i-1], eps) && files[i-1].v.Episodes[0] != f.v.Episodes[0] {
			continue
		}
		for k := i + 1; k < len(files) && samePlaces(pl[k], eps) && files[k].v.Episodes[0] != files[k-1].v.Episodes[0]; k++ {
			if files[k].minutes > 0 && f.minutes > 0 {
				f.minutes += files[k].minutes
			}
		}
		if f.minutes > 0 {
			sum := 0
			for _, e := range eps {
				if e.Runtime <= 0 {
					sum = 0
					break
				}
				sum += e.Runtime
			}
			if sum > 0 {
				lengths++
				said++
				if math.Abs(f.minutes-float64(sum)) <= max(lengthNear, lengthNearShare*float64(sum)) {
					agree++
				}
			}
		}
		if f.v.Name != "" && tmdbName(f.v.Name) {
			said += nameWeight
			if slices.ContainsFunc(eps, func(e tmdb.Placed) bool { return SameEpisodeName(f.v.Name, e.Name) }) {
				agree += nameWeight
			}
		}
	}
	if said == 0 || said < nameWeight && lengths < lengthsKnown {
		return 0, false
	}
	return agree / said, true
}

func samePlaces(a, b []tmdb.Placed) bool {
	return slices.EqualFunc(a, b, func(x, y tmdb.Placed) bool { return x.Season == y.Season && x.Number == y.Number })
}

// partMark is a part's mark after an episode's name: "(2)", "(Part 2)",
// ", Part 1", " - Part One", "Pt. 2", "Partie 2".
var partMark = regexp.MustCompile(`(?i)\s*(?:\((?:part\s*|partie\s*|pt\.?\s*)?(?:\d{1,2}|one|two|three|i{1,3})\)|[-:,]?\s*(?:part|partie|pt\.?)\s*(?:\d{1,2}|one|two|three|i{1,3}))\s*$`)

// SameEpisodeName says whether two names of an episode are one: the same
// words, or nearly ("Part One" and "Part 1" aside), punctuation and case
// aside; or one name holding the other whole when it is long enough to
// say something (a release's name may add the series' or a part's).
func SameEpisodeName(a, b string) bool {
	// A part's mark ("Name (2)", "Name, Part 1") names the episode the
	// parts are of, where TMDB has them as one.
	if pa, pb := partMark.ReplaceAllString(a, ""), partMark.ReplaceAllString(b, ""); pa != a || pb != b {
		if k := analyse.Key(pa); k != "" && k == analyse.Key(pb) {
			return true
		}
	}
	ka, kb := analyse.Key(a), analyse.Key(b)
	if ka == "" || kb == "" {
		return false
	}
	if similarity(ka, kb) >= 0.9 {
		return true
	}
	if len(ka) > len(kb) {
		ka, kb = kb, ka
	}
	return len(strings.Fields(ka)) >= 3 && strings.Contains(" "+kb+" ", " "+ka+" ")
}
