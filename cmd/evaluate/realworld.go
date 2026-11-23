package main

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/C0PT3R/reelid/analyse"
	"github.com/C0PT3R/reelid/inside"
	"github.com/C0PT3R/reelid/match"
	"github.com/C0PT3R/reelid/tmdb"
)

// The real-world evaluation: a downloads folder, as the downloads are
// (release names and folders), against the clean library they were put
// in as hardlinks. A download and its library file share an inode; the
// library file's path, read by stage 1, is the download's truth (its
// TMDB ID, season and episodes). Downloads without a library twin are
// counted apart, not scored.
//
//	evaluate -real-walk ~/analyser-corpus/realworld.tsv -real-root /mnt/media
//	evaluate -real ~/analyser-corpus/realworld.tsv
//
// The walk only stats the files (one walk of each tree); nothing is read.

// realTrees are the folders walked below the root: the library's two,
// and the downloads.
var realTrees = []string{"Films", "Series", "downloads"}

// walkReal writes the listing of the trees below root: tree, size,
// device, inode and path (relative to the tree) of every regular file.
func walkReal(root, out string) error {
	if root == "" {
		return fmt.Errorf("-real-walk: say which folder holds the trees (-real-root)")
	}
	if err := outsideRepository(out); err != nil {
		return err
	}
	f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, tree := range realTrees {
		base := filepath.Join(root, tree)
		n, links, odd := 0, 0, 0
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				fmt.Fprintf(os.Stderr, "walk: %v\n", err)
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				links++
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			st, ok := info.Sys().(*syscall.Stat_t)
			rel, _ := filepath.Rel(base, p)
			if !ok || strings.ContainsAny(rel, "\t\n") {
				odd++
				return nil
			}
			n++
			fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%s\n", tree, info.Size(), st.Dev, st.Ino, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "walk: %s: %d files, %d symlinks skipped, %d odd names skipped\n", tree, n, links, odd)
	}
	return w.Flush()
}

// realEntry is a file of the listing.
type realEntry struct {
	path  string
	size  int64
	inode [2]uint64
}

func readRealListing(name string) (map[string][]realEntry, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string][]realEntry{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		x := strings.SplitN(sc.Text(), "\t", 5)
		if len(x) != 5 {
			continue
		}
		size, _ := strconv.ParseInt(x[1], 10, 64)
		dev, _ := strconv.ParseUint(x[2], 10, 64)
		ino, _ := strconv.ParseUint(x[3], 10, 64)
		out[x[0]] = append(out[x[0]], realEntry{x[4], size, [2]uint64{dev, ino}})
	}
	return out, sc.Err()
}

// realTruth is what a download is, from its library twin.
type realTruth struct {
	lib      string // "Films" or "Series"
	path     string // the twin's path, "Films/…"
	id       int    // its title's TMDB ID; 0 when the library names none
	season   int
	episodes []int
	extra    analyse.ExtraKind
}

func (t *realTruth) kind() tmdb.Kind {
	if t.lib == "Series" {
		return tmdb.TV
	}
	return tmdb.Movie
}

func (t *realTruth) episode() bool { return t.lib == "Series" && t.extra == "" }

// realScores are one stage's scores of the downloads' matches.
type realScores struct {
	films, filmsOK, series, seriesOK                      int // videos
	titleFilms, titleFilmsOK, titleSeries, titleSeriesOK  int // titles
	sure, sureWrong, check, checkRight, checkListed, none int
	hard                                                  map[string]int
}

// realRun is the evaluation's state.
type realRun struct {
	probes map[string]inside.File
	src    tmdb.Source
	truth  map[[2]uint64]*realTruth
	// details of the truth's titles on TMDB, for the failure patterns.
	details map[tmdb.Kind]map[int]*tmdb.Details
}

func runReal(listing, probeFile string, client *tmdb.Client, workers, top int, dbg func(string)) error {
	trees, err := readRealListing(listing)
	if err != nil {
		return err
	}
	r := &realRun{src: client, truth: map[[2]uint64]*realTruth{}, details: map[tmdb.Kind]map[int]*tmdb.Details{tmdb.Movie: {}, tmdb.TV: {}}}

	// The truth: the library read by stage 1.
	for _, lib := range []string{"Films", "Series"} {
		var entries []analyse.Entry
		inodeOf := map[string][2]uint64{}
		for _, e := range trees[lib] {
			entries = append(entries, analyse.Entry{Path: e.path, Size: e.size})
			inodeOf[e.path] = e.inode
		}
		res := analyse.Analyse(entries)
		for _, t := range res.Titles {
			id, _ := strconv.Atoi(t.IDs.TMDB)
			for _, v := range slices.Concat(t.Videos, t.Extras) {
				tr := &realTruth{lib: lib, path: lib + "/" + v.Path, id: id, season: v.Season, episodes: v.Episodes, extra: v.Extra}
				r.truth[inodeOf[v.Path]] = tr
			}
		}
	}

	// The downloads, as they are.
	var entries []analyse.Entry
	inodeOf := map[string][2]uint64{}
	for _, e := range trees["downloads"] {
		entries = append(entries, analyse.Entry{Path: e.path, Size: e.size})
		inodeOf[e.path] = e.inode
	}
	res := analyse.Analyse(entries)
	truthOf := func(p string) *realTruth { return r.truth[inodeOf[p]] }

	if probeFile != "" {
		if r.probes, err = inside.ReadProbes(probeFile); err != nil {
			return err
		}
	}
	look := func(p string) (inside.File, bool) {
		t := truthOf(p)
		if t == nil || r.probes == nil {
			return inside.File{}, false
		}
		f, ok := r.probes[t.path]
		return f, ok
	}

	// Stage 1: what each download is, against its twin.
	type videoAt struct {
		v *analyse.Video
		t *analyse.Title
	}
	var all []videoAt
	for _, t := range res.Titles {
		for _, v := range slices.Concat(t.Videos, t.Extras) {
			all = append(all, videoAt{v, t})
		}
	}
	// Season packs: folders holding three or more episodes of one season
	// (by the truth).
	perDir := map[string]map[string]int{}
	for _, x := range all {
		if t := truthOf(x.v.Path); t != nil && t.episode() {
			d := path.Dir(x.v.Path)
			if perDir[d] == nil {
				perDir[d] = map[string]int{}
			}
			perDir[d][strconv.Itoa(t.id)+"/"+strconv.Itoa(t.season)]++
		}
	}
	isPack := func(p string) bool {
		for _, n := range perDir[path.Dir(p)] {
			if n >= 3 {
				return true
			}
		}
		return false
	}

	videos, twins, withProbe := 0, 0, 0
	tallies := map[string]*tally{}
	check := func(field string, ok bool) {
		t := tallies[field]
		if t == nil {
			t = &tally{}
			tallies[field] = t
		}
		t.n++
		if ok {
			t.ok++
		}
	}
	fails := map[string]int{}
	fail := func(field, p string, tr *realTruth, got string) {
		k := field + "\t" + strings.Join(pathTraits(p, tr, isPack(p)), ",")
		fails[k]++
		if dbg != nil {
			dbg(fmt.Sprintf("stage1\t%s\t%s\t%s\ttruth %s/%d S%d E%v %s\tgot %s", field, p, tr.path, tr.kind(), tr.id, tr.season, tr.episodes, tr.extra, got))
		}
	}
	// Downloads without a twin: what the analyser made of them.
	alone, aloneExtra, samples, samplesExtra := 0, 0, 0, 0
	for _, x := range all {
		videos++
		p := x.v.Path
		tr := truthOf(p)
		if tr == nil {
			alone++
			if x.v.Extra != "" {
				aloneExtra++
			}
			if sampleLike.MatchString(path.Base(p)) || slices.Contains(strings.Split(strings.ToLower(path.Dir(p)), "/"), "sample") {
				samples++
				if x.v.Extra != "" {
					samplesExtra++
				}
			}
			continue
		}
		twins++
		if _, ok := look(p); ok {
			withProbe++
		}
		got := fmt.Sprintf("%s S%d E%v extra=%q", x.t.Kind, x.v.Season, x.v.Episodes, x.v.Extra)
		isExtra := x.v.Extra != ""
		check("extra", isExtra == (tr.extra != ""))
		if isExtra != (tr.extra != "") {
			how := "extra-missed"
			if isExtra {
				how = "made-extra"
			}
			fail(how, p, tr, got)
		}
		if tr.extra != "" || isExtra {
			continue
		}
		kindOK := (x.t.Kind == analyse.Show) == tr.episode()
		check("kind", kindOK)
		if !kindOK {
			fail("kind", p, tr, got)
			continue
		}
		if !tr.episode() {
			continue
		}
		cat := "episodes:single"
		switch {
		case len(tr.episodes) > 1:
			cat = "episodes:multi"
		case isPack(p):
			cat = "episodes:pack"
		}
		seasonOK := x.v.Season == tr.season
		epsOK := seasonOK && slices.Equal(x.v.Episodes, tr.episodes)
		check("season", seasonOK)
		check("episodes", epsOK)
		check(cat, epsOK)
		// Numbers read as the name writes them, but not the library's: the
		// release numbers its episodes in another order than TMDB's.
		field := ""
		if !seasonOK {
			field = "season"
		} else if !epsOK {
			field = "episodes"
		}
		if field != "" {
			if m := sxxexxNumbers.FindStringSubmatch(path.Base(p)); m != nil && len(x.v.Episodes) > 0 {
				se, _ := strconv.Atoi(m[1])
				ep, _ := strconv.Atoi(m[2])
				if se == x.v.Season && ep == x.v.Episodes[0] {
					field += " (read as written: numbering not the library's)"
				}
			}
			fail(field, p, tr, got)
		}
	}
	// Analysed titles holding several truths: wrongly merged.
	merged := 0
	for _, t := range res.Titles {
		ids := map[string]bool{}
		for _, v := range t.Videos {
			if tr := truthOf(v.Path); tr != nil && tr.extra == "" {
				ids[string(tr.kind())+strconv.Itoa(tr.id)] = true
			}
		}
		if len(ids) > 1 {
			merged++
		}
	}

	fmt.Printf("downloads: %d videos in %d analysed titles (library %s); %d with a library twin (%d of those with a probe), %d without\n",
		videos, len(res.Titles), res.Library, twins, withProbe, alone)
	fmt.Printf("without a twin: %d made extras; %d named or foldered as samples, %d of them made extras\n\n", aloneExtra, samples, samplesExtra)
	fmt.Printf("stage 1 (videos with a twin):\n")
	names := slices.Sorted(func(yield func(string) bool) {
		for k := range tallies {
			if !yield(k) {
				return
			}
		}
	})
	for _, k := range names {
		t := tallies[k]
		fmt.Printf("  %-16s %6d/%-6d %6.2f%%\n", k, t.ok, t.n, t.percent())
	}
	fmt.Printf("  analysed titles holding videos of several true titles: %d\n", merged)
	printCounts("stage 1 failures (field, traits of the download's path):", fails, top)

	// Stages 2 and 3: each analysed title matched, against its videos'
	// commonest truth.
	type job struct {
		t     *analyse.Title
		truth *realTruth
		n     int // its videos with that truth
	}
	var jobs []job
	for _, t := range res.Titles {
		count := map[*realTruth]int{}
		byID := map[string]*realTruth{}
		for _, v := range t.Videos {
			tr := truthOf(v.Path)
			if tr == nil || tr.extra != "" || tr.id == 0 {
				continue
			}
			k := string(tr.kind()) + strconv.Itoa(tr.id)
			if byID[k] == nil {
				byID[k] = tr
			}
			count[byID[k]]++
		}
		var best *realTruth
		for tr, n := range count {
			if best == nil || n > count[best] || n == count[best] && tr.path < best.path {
				best = tr
			}
		}
		if best != nil {
			jobs = append(jobs, job{t, best, count[best]})
		}
	}
	// The truth's details, for the patterns.
	m := &matcher{src: client, workers: workers, matches: map[string]match.Result{}}
	var mu sync.Mutex
	if err := m.each(len(jobs), func(i int) error {
		tr := jobs[i].truth
		d, err := client.Details(context.Background(), tr.kind(), tr.id)
		if errors.Is(err, tmdb.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		mu.Lock()
		r.details[tr.kind()][tr.id] = d
		mu.Unlock()
		return nil
	}); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "real: matching %d titles\n", len(jobs))
	results := make([][2]match.Result, len(jobs))
	queries := make([][2]match.Query, len(jobs))
	if err := m.each(len(jobs), func(i int) error {
		q := match.QueryOf(jobs[i].t)
		res2, err := m.match(context.Background(), q)
		if err != nil {
			return err
		}
		q3 := q
		match.AddInside(&q3, jobs[i].t, look)
		res3, err := m.match(context.Background(), q3)
		if err != nil {
			return err
		}
		results[i], queries[i] = [2]match.Result{res2, res3}, [2]match.Query{q, q3}
		return nil
	}); err != nil {
		return err
	}
	var sc [2]realScores
	for s := range sc {
		sc[s].hard = map[string]int{}
	}
	fixed, broke := 0, 0
	for i, j := range jobs {
		tr := j.truth
		var ok [2]bool
		for s := range 2 {
			res := results[i][s]
			x := &sc[s]
			right := res.Best != nil && res.Best.ID == tr.id && res.Best.Kind == tr.kind()
			ok[s] = right
			// Per video: every video of the title with a truth.
			for _, v := range j.t.Videos {
				vt := truthOf(v.Path)
				if vt == nil || vt.extra != "" || vt.id == 0 {
					continue
				}
				vok := res.Best != nil && res.Best.ID == vt.id && res.Best.Kind == vt.kind()
				if vt.lib == "Films" {
					x.films++
					if vok {
						x.filmsOK++
					}
				} else {
					x.series++
					if vok {
						x.seriesOK++
					}
				}
			}
			if tr.lib == "Films" {
				x.titleFilms++
				if right {
					x.titleFilmsOK++
				}
			} else {
				x.titleSeries++
				if right {
					x.titleSeriesOK++
				}
			}
			switch res.Confidence {
			case match.Sure:
				x.sure++
				if !right {
					x.sureWrong++
				}
			case match.Check:
				x.check++
				if right {
					x.checkRight++
				} else if slices.ContainsFunc(res.Others, func(c match.Candidate) bool { return c.ID == tr.id && c.Kind == tr.kind() }) {
					x.checkListed++
				}
			default:
				x.none++
			}
			if right {
				continue
			}
			d := r.details[tr.kind()][tr.id]
			pattern := hardPattern(&title{lib: tr.lib}, queries[i][s], res, d)
			p := firstVideo(j.t)
			pattern += " | " + strings.Join(pathTraits(p, tr, isPack(p)), ",")
			if _, ok := look(p); s == 1 && !ok {
				pattern += ",no probe"
			}
			if d != nil && foreignName(queries[i][s].Name, d) {
				pattern += ",name in another language"
			}
			x.hard[pattern]++
			if dbg != nil {
				got := "none"
				if res.Best != nil {
					got = fmt.Sprintf("%s/%d %q (%d) %d %v", res.Best.Kind, res.Best.ID, res.Best.Name, res.Best.Year, res.Best.Score, res.Best.Why)
				}
				dbg(fmt.Sprintf("stage%d\t%s\t%s\t%s\ttruth %s/%d\tquery %q %d %s\tgot %s", s+2, pattern, res.Confidence, p, tr.kind(), tr.id, queries[i][s].Name, queries[i][s].Year, queries[i][s].Kind, got))
			}
		}
		if !ok[0] && ok[1] {
			fixed++
		}
		if ok[0] && !ok[1] {
			broke++
		}
	}
	fmt.Printf("\nTMDB IDs (stage 2 → stage 3), %d analysed titles with a truth:\n", len(jobs))
	row := func(label string, a, an, b, bn int) {
		fmt.Printf("  %-26s %6.2f%% → %6.2f%%   (%d/%d → %d/%d)\n", label, pct(a, an), pct(b, bn), a, an, b, bn)
	}
	a, b := sc[0], sc[1]
	row("film videos", a.filmsOK, a.films, b.filmsOK, b.films)
	row("episode videos", a.seriesOK, a.series, b.seriesOK, b.series)
	row("film titles", a.titleFilmsOK, a.titleFilms, b.titleFilmsOK, b.titleFilms)
	row("series titles", a.titleSeriesOK, a.titleSeries, b.titleSeriesOK, b.titleSeries)
	row("sure, wrong", a.sureWrong, a.sure, b.sureWrong, b.sure)
	row("to check, right", a.checkRight, a.check, b.checkRight, b.check)
	row("wrong to check, listed", a.checkListed, a.check-a.checkRight, b.checkListed, b.check-b.checkRight)
	fmt.Printf("  none found: %d → %d; stage 3 fixed %d titles, broke %d\n", a.none, b.none, fixed, broke)
	printCounts("stage 2 wrong titles (pattern | traits of the first video's path):", a.hard, top)
	printCounts("stage 3 wrong titles:", b.hard, top)
	return nil
}

// foreignName says whether a query's name is one of the title's
// translations or alternative titles, not its own or original name.
func foreignName(name string, d *tmdb.Details) bool {
	k := analyse.Key(name)
	if k == "" || k == analyse.Key(d.Name) || k == analyse.Key(d.OriginalName) {
		return false
	}
	return slices.ContainsFunc(append(slices.Clone(d.Translations), d.AltNames...), func(n string) bool { return analyse.Key(n) == k })
}

func firstVideo(t *analyse.Title) string {
	if len(t.Videos) > 0 {
		return t.Videos[0].Path
	}
	return ""
}

func printCounts(head string, counts map[string]int, top int) {
	type kv struct {
		k string
		n int
	}
	var list []kv
	total := 0
	for k, n := range counts {
		list = append(list, kv{k, n})
		total += n
	}
	slices.SortFunc(list, func(a, b kv) int { return cmp.Or(b.n-a.n, strings.Compare(a.k, b.k)) })
	fmt.Printf("\n%s %d\n", head, total)
	for i, e := range list {
		if i == top {
			break
		}
		fmt.Printf("%6d  %s\n", e.n, e.k)
	}
}

// The traits of a download's path, without its names.
var (
	sampleLike    = regexp.MustCompile(`(?i)(^|[ ._\-\[(])sample([ ._\-\])]|$)`)
	sxxexx        = regexp.MustCompile(`(?i)(^|[^a-z0-9])s\d{1,3}[ ._-]?e\d{1,4}`)
	sxxexxNumbers = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s(\d{1,3})[ ._-]?e(\d{1,4})`)
	nxnn          = regexp.MustCompile(`(?i)(^|[^a-z0-9])\d{1,2}x\d{2,3}([^0-9]|$)`)
	animeLike     = regexp.MustCompile(`^\[[^\]]+\]`)
	dashNumber    = regexp.MustCompile(`(?:^| - |\s|_|\.)(?:ep?\.?\s?)?(\d{2,4})(?:v\d)?(?:[ ._\[\(]|$)`)
	dateLike      = regexp.MustCompile(`(?:^|[^0-9])(?:19|20)\d\d[ ._-](?:0[1-9]|1[0-2])[ ._-](?:0[1-9]|[12]\d|3[01])(?:[^0-9]|$)`)
	seasonToken   = regexp.MustCompile(`(?i)(^|[^a-z0-9])(s\d{1,2}|season[ ._-]?\d{1,2}|saison[ ._-]?\d{1,2}|integrale|complete)([^a-z0-9e]|$)`)
	yearToken     = regexp.MustCompile(`(?:^|[^0-9])(?:19|20)\d\d(?:[^0-9]|$)`)
	hashLike      = regexp.MustCompile(`^[A-Za-z0-9]{20,}$`)
)

func pathTraits(p string, tr *realTruth, pack bool) []string {
	dir, file := path.Split(p)
	base := strings.TrimSuffix(file, path.Ext(file))
	depth := strings.Count(strings.TrimSuffix(dir, "/"), "/") // folders below the downloads' top folder
	var t []string
	add := func(ok bool, s string) {
		if ok {
			t = append(t, s)
		}
	}
	add(dir == "" || depth == 0, "loose file")
	add(depth >= 2, "nested folders")
	add(tr.lib == "Films", "film")
	if tr.lib == "Series" {
		switch {
		case sxxexx.MatchString(base):
			t = append(t, "SxxEyy")
		case nxnn.MatchString(base):
			t = append(t, "NxNN")
		case dateLike.MatchString(base):
			t = append(t, "date-based")
		case dashNumber.MatchString(base):
			t = append(t, "bare number")
		default:
			t = append(t, "no episode number")
		}
		add(animeLike.MatchString(base), "anime-style [group]")
		add(pack, "season pack folder")
		add(len(tr.episodes) > 1, "multi-episode")
		add(tr.season == 0, "special")
		add(tr.season > 1 && !sxxexx.MatchString(base) && !seasonToken.MatchString(p), "season>1 not written")
	}
	add(!yearToken.MatchString(p), "no year")
	add(sampleLike.MatchString(base), "sample")
	add(hashLike.MatchString(base), "hash-like name")
	add(strings.ContainsFunc(p, func(r rune) bool { return r > 127 }), "non-ascii")
	add(tr.extra != "", "library extra")
	return t
}
