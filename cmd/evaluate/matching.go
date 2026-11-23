package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/overfin/analyser/analyse"
	"github.com/overfin/analyser/inside"
	"github.com/overfin/analyser/match"
	"github.com/overfin/analyser/tmdb"
)

// Stage 2: matching the identified titles to TMDB. Every run strips the
// ID tags (with them, there is nothing to find), alone or with other
// damages, and the translated damage names titles by another of their
// TMDB names. A run scores, per title of the key, whether the TMDB ID
// found is its ID, and how well the confidence said so.

// matchRuns are stage 2's damages.
var matchRuns = []style{
	{name: "match:ids-stripped", noIDs: true},
	{name: "match:no-ids+no-year", noIDs: true, noYear: true},
	{name: "match:no-ids+separators+case+punct", noIDs: true, separators: true, letterCase: true, punct: true},
	{name: "match:no-ids+release-all", noIDs: true, releaseFiles: true, releaseFolders: true},
	{name: "match:no-ids+scene-tags+no-year", noIDs: true, releaseFiles: true, sceneTags: true, noYear: true},
	{name: "match:flat+no-ids+no-year+notation", flat: true, noIDs: true, noYear: true, notation: true},
	{name: "match:mixed+release-all+flat", mixed: true, noIDs: true, releaseFiles: true, releaseFolders: true, flat: true},
	{name: "match:everything-but-flat", noIDs: true, separators: true, letterCase: true, punct: true, seasonRenamed: true,
		notation: true, nestGroup: true, extras: true, versions: true},
	{name: "match:translated", noIDs: true, translated: true},
	{name: "match:translated+no-year", noIDs: true, translated: true, noYear: true},
	{name: "match:translated+scene-tags", noIDs: true, translated: true, releaseFiles: true, sceneTags: true},
}

// matcher runs stage 2: TMDB, the truth's details, and the matches made,
// shared by the runs (one query is matched once).
type matcher struct {
	src     tmdb.Source
	workers int
	truth   map[*title]*tmdb.Details

	mu      sync.Mutex
	matches map[string]match.Result
}

// matchRun is one stage 2 run's scores.
type matchRun struct {
	name              string
	films, filmsOK    int
	series, seriesOK  int
	sure, sureWrong   int
	check, checkRight int
	checkListed       int // to check, wrong, but the truth listed beside it
	none              int
	renamed           int // titles the translated damage renamed
	hard              map[string]*hardCase
	debug             []string
	// wrong are the titles matched wrong, with their pattern.
	wrong map[*title]string
}

// hardCase counts a pattern of wrong matches.
type hardCase struct {
	n, sure, check, none int
	traits               map[string]int
}

// kindOf is the TMDB kind of a key title.
func kindOf(t *title) tmdb.Kind {
	if t.lib == "Series" {
		return tmdb.TV
	}
	return tmdb.Movie
}

// matchable are the key's titles stage 2 can score: those tagged with
// their TMDB ID.
func matchable(c *corpus) []*title {
	var out []*title
	for _, t := range c.titles {
		if t.idKind == "tmdbid" && len(t.videos) > 0 {
			out = append(out, t)
		}
	}
	return out
}

// loadTruth fetches the key's titles' details from TMDB (their names,
// for the translated damage and for telling why a match failed).
func (m *matcher) loadTruth(ctx context.Context, titles []*title) error {
	m.truth = map[*title]*tmdb.Details{}
	var mu sync.Mutex
	return m.each(len(titles), func(i int) error {
		t := titles[i]
		id, _ := strconv.Atoi(t.id)
		d, err := m.src.Details(ctx, kindOf(t), id)
		if errors.Is(err, tmdb.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		mu.Lock()
		m.truth[t] = d
		mu.Unlock()
		return nil
	})
}

// each runs do(0…n-1) on the matcher's workers; the first error stops it.
func (m *matcher) each(n int, do func(int) error) error {
	next := make(chan int)
	errs := make(chan error, m.workers)
	var wg sync.WaitGroup
	for range m.workers {
		wg.Go(func() {
			for i := range next {
				if err := do(i); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	var err error
loop:
	for i := range n {
		select {
		case next <- i:
		case err = <-errs:
			break loop
		}
	}
	close(next)
	wg.Wait()
	close(errs)
	if err == nil {
		err = <-errs
	}
	return err
}

// translate gives each key title another of its TMDB names, for the
// translated damage: one of its translations or alternative titles that
// is written in Latin letters and isn't its name, chosen by a hash.
func (m *matcher) translate(titles []*title) {
	for _, t := range titles {
		d := m.truth[t]
		if d == nil {
			continue
		}
		var names []string
		seen := map[string]bool{analyse.Key(t.name): true}
		for _, n := range append(slices.Clone(d.Translations), d.AltNames...) {
			n = pathSafe(n)
			k := analyse.Key(n)
			if k == "" || seen[k] || !latin(n) {
				continue
			}
			seen[k] = true
			names = append(names, n)
		}
		if len(names) == 0 {
			continue
		}
		h := fnv.New32a()
		h.Write([]byte(strconv.Itoa(t.index) + "translated"))
		t.translated = names[h.Sum32()%uint32(len(names))]
	}
}

// pathSafe takes out of a name what a file name can't have.
func pathSafe(n string) string {
	n = strings.NewReplacer("/", " ", "\\", " ", ":", " -", "*", "", "?", "", "\"", "", "<", "", ">", "", "|", "").Replace(n)
	return strings.Join(strings.Fields(n), " ")
}

// latin says whether a name's letters are all Latin ones.
func latin(n string) bool {
	for _, r := range n {
		if unicode.IsLetter(r) && !unicode.In(r, unicode.Latin) {
			return false
		}
	}
	return true
}

// match matches a query, once per query over all runs.
func (m *matcher) match(ctx context.Context, q match.Query) (match.Result, error) {
	key := fmt.Sprintf("%s|%s|%d|%v|%d|%s|%s|%v|%v|%v", q.Kind, analyse.Key(q.Name), q.Year, q.Episodes,
		q.TMDB, q.TMDBKind, q.IMDB, q.Tagged, q.Runtime, q.Lengths)
	m.mu.Lock()
	res, ok := m.matches[key]
	m.mu.Unlock()
	if ok {
		return res, nil
	}
	res, err := match.Match(ctx, m.src, q)
	if err != nil {
		return res, err
	}
	m.mu.Lock()
	m.matches[key] = res
	m.mu.Unlock()
	return res, nil
}

// runMatch damages the key as s says, analyses it, matches each title
// found, and scores the matches against the key's IDs. With look (what
// a key file says of itself, see stage3), each title is also matched
// with what its files say, and scored apart: stage 3 beside stage 2.
func (m *matcher) runMatch(ctx context.Context, c *corpus, s style, look func(*file) (inside.File, bool)) (r, r3 *matchRun, err error) {
	r = newMatchRun(s.name)
	if look != nil {
		r3 = newMatchRun(s.name)
	}
	files := append(slices.Clip(c.files), added(c, s)...)
	libs := []string{"Films", "Series"}
	if s.mixed {
		libs = []string{""}
	}
	for _, lib := range libs {
		// The damaged library, analysed.
		var entries []analyse.Entry
		fileOf := map[string]*file{}
		for _, f := range files {
			if lib != "" && f.title.lib != lib {
				continue
			}
			p := render(f, s, plain)
			if _, taken := fileOf[p]; taken {
				continue
			}
			fileOf[p] = f
			entries = append(entries, analyse.Entry{Path: p, Size: f.size})
		}
		res := analyse.Analyse(entries)

		// Each key title's analysed title: where most of its videos are.
		counts := map[*title]map[*analyse.Title]int{}
		for _, at := range res.Titles {
			for _, v := range at.Videos {
				f := fileOf[v.Path]
				if f == nil || f.extra != "" || f.isSubtitle() {
					continue
				}
				if counts[f.title] == nil {
					counts[f.title] = map[*analyse.Title]int{}
				}
				counts[f.title][at]++
			}
		}
		var titles []*title
		home := map[*title]*analyse.Title{}
		for _, t := range matchable(c) {
			if lib != "" && t.lib != lib {
				continue
			}
			titles = append(titles, t)
			best := 0
			for at, n := range counts[t] {
				if n > best || n == best && at.Name < home[t].Name {
					home[t], best = at, n
				}
			}
		}

		// The matches, on the workers.
		results, results3 := make([]match.Result, len(titles)), make([]match.Result, len(titles))
		queries, queries3 := make([]match.Query, len(titles)), make([]match.Query, len(titles))
		err := m.each(len(titles), func(i int) error {
			at := home[titles[i]]
			if at == nil {
				results[i] = match.Result{Confidence: match.None}
				results3[i] = results[i]
				return nil
			}
			queries[i] = match.QueryOf(at)
			var err error
			if results[i], err = m.match(ctx, queries[i]); err != nil || look == nil {
				return err
			}
			queries3[i] = queries[i]
			match.AddInside(&queries3[i], at, func(p string) (inside.File, bool) {
				if f := fileOf[p]; f != nil {
					return look(f)
				}
				return inside.File{}, false
			})
			results3[i], err = m.match(ctx, queries3[i])
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		for i, t := range titles {
			r.score(t, queries[i], results[i], m.truth[t], s)
			if r3 != nil {
				r3.score(t, queries3[i], results3[i], m.truth[t], s)
			}
		}
	}
	return r, r3, nil
}

func newMatchRun(name string) *matchRun {
	return &matchRun{name: name, hard: map[string]*hardCase{}, wrong: map[*title]string{}}
}

// score scores one title's match.
func (r *matchRun) score(t *title, q match.Query, res match.Result, truth *tmdb.Details, s style) {
	right := res.Best != nil && strconv.Itoa(res.Best.ID) == t.id && res.Best.Kind == kindOf(t)
	if s.translated && t.translated != "" {
		r.renamed++
	}
	if t.lib == "Films" {
		r.films++
		if right {
			r.filmsOK++
		}
	} else {
		r.series++
		if right {
			r.seriesOK++
		}
	}
	switch res.Confidence {
	case match.Sure:
		r.sure++
		if !right {
			r.sureWrong++
		}
	case match.Check:
		r.check++
		if right {
			r.checkRight++
		} else if slices.ContainsFunc(res.Others, func(c match.Candidate) bool {
			return strconv.Itoa(c.ID) == t.id && c.Kind == kindOf(t)
		}) {
			r.checkListed++
		}
	default:
		r.none++
	}
	if right {
		return
	}
	pattern := hardPattern(t, q, res, truth)
	r.wrong[t] = pattern
	h := r.hard[pattern]
	if h == nil {
		h = &hardCase{traits: map[string]int{}}
		r.hard[pattern] = h
	}
	h.n++
	switch res.Confidence {
	case match.Sure:
		h.sure++
	case match.Check:
		h.check++
	default:
		h.none++
	}
	h.traits[traits(t.name)]++
	got := "none"
	if res.Best != nil {
		got = fmt.Sprintf("%s/%d %q (%d) %d %v", res.Best.Kind, res.Best.ID, res.Best.Name, res.Best.Year, res.Best.Score, res.Best.Why)
	}
	truthAs := "truth not weighed"
	if i := slices.IndexFunc(res.All, func(c match.Candidate) bool { return strconv.Itoa(c.ID) == t.id && c.Kind == kindOf(t) }); i >= 0 {
		truthAs = fmt.Sprintf("truth #%d %d %v", i+1, res.All[i].Score, res.All[i].Why)
	}
	r.debug = append(r.debug, fmt.Sprintf("%s\t%s\t%s %q (%d) %s/%s\tquery %q %d %s\tgot %s\t%s",
		pattern, res.Confidence, t.lib, t.name, t.year, kindOf(t), t.id, q.Name, q.Year, q.Kind, got, truthAs))
}

// hardPattern says, without a name, why a title was matched wrong: from
// what the paths said (the query), the truth's names and year on TMDB,
// and the best candidate.
func hardPattern(t *title, q match.Query, res match.Result, truth *tmdb.Details) string {
	key := analyse.Key(q.Name)
	switch {
	case q.Name == "" || key == "":
		return "no name from the paths"
	case q.Kind != "" && (q.Kind == analyse.Show) != (t.lib == "Series"):
		return "kind wrong from the paths"
	case truth == nil:
		return "truth unknown to TMDB"
	}
	truthNames := map[string]bool{}
	for _, n := range truth.Names() {
		truthNames[analyse.Key(n)] = true
	}
	listed := slices.ContainsFunc(res.All, func(c match.Candidate) bool { return c.ID == truth.ID && c.Kind == truth.Kind })
	if res.Best == nil {
		if truthNames[key] {
			return "not found by its own name"
		}
		return "not found, name not TMDB's"
	}
	got := res.Best
	gotKey := analyse.Key(got.Name)
	sameName := truthNames[gotKey] || gotKey == analyse.Key(truth.Name)
	switch {
	case !truthNames[key] && !listed:
		return "name not TMDB's, truth not in the search"
	case sameName && q.Year == 0:
		return "same name, no year"
	case sameName && got.Year != truth.Year:
		return "same name, other year (remake, reboot)"
	case sameName:
		return "same name and year"
	case stripDigits(gotKey) == stripDigits(analyse.Key(truth.Name)):
		return "sequel number"
	case strings.HasPrefix(gotKey, analyse.Key(truth.Name)+" ") || strings.HasPrefix(analyse.Key(truth.Name), gotKey+" "):
		return "name starts the other (sequel, spin-off)"
	case q.Year != 0 && truth.Year != 0 && q.Year != truth.Year:
		return "year not TMDB's"
	case !listed:
		return "truth not in the search"
	case !truthNames[key]:
		return "name not TMDB's"
	default:
		return "outscored"
	}
}

// stripDigits takes the numbers out of a key ("name 2" is "name").
func stripDigits(key string) string {
	words := strings.Fields(key)
	words = slices.DeleteFunc(words, func(w string) bool {
		_, err := strconv.Atoi(w)
		return err == nil || roman[w]
	})
	return strings.Join(words, " ")
}

var roman = map[string]bool{"ii": true, "iii": true, "iv": true, "v": true, "vi": true}

// reportMatch prints stage 2's scores and its hardest patterns.
func reportMatch(runs []*matchRun, top int) {
	fmt.Printf("%-38s %7s %7s %6s %6s %6s %6s %7s %5s\n", "stage 2 damage", "films", "series", "sure", "wrong", "check", "right", "listed", "none")
	for _, r := range runs {
		name := r.name
		if r.renamed > 0 {
			name += fmt.Sprintf(" (%d renamed)", r.renamed)
		}
		fmt.Printf("%-38s %6.2f%% %6.2f%% %6d %5.1f%% %6d %5.1f%% %6.1f%% %5d\n", name,
			pct(r.filmsOK, r.films), pct(r.seriesOK, r.series), r.sure, pct(r.sureWrong, r.sure),
			r.check, pct(r.checkRight, r.check), pct(r.checkListed, r.check-r.checkRight), r.none)
	}
	fmt.Println("\n(films, series: TMDB IDs right; sure: how many were said sure, wrong: how many of those were wrong;")
	fmt.Println(" check: how many were given to check, right: how many of those were right, listed: of the wrong ones,")
	fmt.Println(" how many had the truth listed beside them; none: nothing found)")

	type counted struct {
		run, pattern string
		h            *hardCase
	}
	var all []counted
	for _, r := range runs {
		for p, h := range r.hard {
			all = append(all, counted{r.name, p, h})
		}
	}
	slices.SortFunc(all, func(a, b counted) int {
		return cmp.Or(b.h.n-a.h.n, strings.Compare(a.run, b.run), strings.Compare(a.pattern, b.pattern))
	})
	fmt.Printf("\nHardest cases (wrong matches by pattern; sure/check/none; the commonest traits of the names):\n")
	for i, c := range all {
		if i == top {
			break
		}
		fmt.Printf("%6d  %-38s %-42s %d/%d/%d  %s\n", c.h.n, c.run, c.pattern, c.h.sure, c.h.check, c.h.none, commonest(c.h.traits, 2))
	}
}

func pct(n, of int) float64 {
	if of == 0 {
		return 0
	}
	return 100 * float64(n) / float64(of)
}

// stage2 runs stage 2's runs whose names contain only ("" for all).
func stage2(ctx context.Context, c *corpus, src tmdb.Source, workers int, only string, top int, dbg func(string)) error {
	m := &matcher{src: src, workers: workers, matches: map[string]match.Result{}}
	titles := matchable(c)
	fmt.Fprintf(os.Stderr, "stage 2: %d titles tagged with their TMDB ID; fetching their details\n", len(titles))
	if err := m.loadTruth(ctx, titles); err != nil {
		return err
	}
	m.translate(titles)
	var runs []*matchRun
	for _, s := range matchRuns {
		if only != "" && !strings.Contains(s.name, only) {
			continue
		}
		fmt.Fprintf(os.Stderr, "stage 2: %s\n", s.name)
		r, _, err := m.runMatch(ctx, c, s, nil)
		if err != nil {
			return err
		}
		runs = append(runs, r)
		if dbg != nil {
			for _, line := range r.debug {
				dbg(s.name + "\t" + line)
			}
		}
	}
	reportMatch(runs, top)
	return nil
}
