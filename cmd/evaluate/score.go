package main

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/C0PT3R/reelid/analyse"
)

// Scoring one run: the damaged library analysed, and each field of each
// file compared with the truth.

// The fields scored.
const (
	fLibrary  = "library"
	fKind     = "kind"
	fID       = "id"
	fName     = "name"
	fYear     = "year"
	fSeason   = "season"
	fEpisodes = "episodes"
	fGrouping = "grouping"
	fAll      = "all-fields"
	fSubtitle = "subtitle"
	fSubLang  = "sub-language"
	fExtra    = "extra"
	fVersion  = "version"
)

var fieldOrder = []string{fLibrary, fKind, fID, fName, fYear, fSeason, fEpisodes, fGrouping, fAll, fSubtitle, fSubLang, fExtra, fVersion}

// tally counts a field's checks and successes.
type tally struct{ ok, n int }

func (t tally) percent() float64 {
	if t.n == 0 {
		return 0
	}
	return 100 * float64(t.ok) / float64(t.n)
}

// failure is a failed check, anonymised: the field, how it failed, and
// the path's pattern.
type failure struct{ field, how, pattern string }

// run is one damage's scores.
type run struct {
	style    style
	fields   map[string]*tally
	failures map[failure]int
	// traits count the traits of the failures' titles' names.
	traits map[failure]map[string]int
	files  int
	// debug lines with real paths, for -debug only.
	debug []string
}

func (r *run) check(field string, ok bool, f *file, how string, p string, got string) {
	t := r.fields[field]
	if t == nil {
		t = &tally{}
		r.fields[field] = t
	}
	t.n++
	if ok {
		t.ok++
		return
	}
	k := failure{field, how, render(f, r.style, pattern)}
	r.failures[k]++
	if r.traits[k] == nil {
		r.traits[k] = map[string]int{}
	}
	r.traits[k][traits(f.title.name)]++
	r.debug = append(r.debug, field+"\t"+how+"\t"+p+"\t"+got)
}

// where an analysed file is.
type found struct {
	title *analyse.Title
	video *analyse.Video
	sub   *analyse.Subtitle
}

// score analyses the damaged library lib and scores it. languages are the
// baseline's subtitle languages by file index; a baseline run fills them.
func score(r *run, lib string, files []*file, languages map[int]string, baseline bool) {
	paths := map[*file]string{}
	byPath := map[string]*file{}
	var entries []analyse.Entry
	for _, f := range files {
		if lib != "" && f.title.lib != lib {
			continue
		}
		p := render(f, r.style, plain)
		if _, taken := byPath[p]; taken {
			continue // an added file that would replace another
		}
		byPath[p] = f
		paths[f] = p
		entries = append(entries, analyse.Entry{Path: p, Size: f.size})
	}
	res := analyse.Analyse(entries)
	where := map[string]found{}
	for _, t := range res.Titles {
		for _, list := range [][]*analyse.Video{t.Videos, t.Extras} {
			for _, v := range list {
				where[v.Path] = found{title: t, video: v}
				for i := range v.Subtitles {
					where[v.Subtitles[i].Path] = found{title: t, video: v, sub: &v.Subtitles[i]}
				}
			}
		}
	}

	want := map[string]analyse.LibraryKind{"Films": analyse.Films, "Series": analyse.Series, "": analyse.Mixed}[lib]
	r.fields[fLibrary] = addTally(r.fields[fLibrary], res.Library == want)

	// Grouping: a title's main videos must make one analysed title, and
	// it nothing else. Added versions count with their title.
	members := map[*analyse.Title]map[*title]int{}
	sizes := map[*title]int{}
	for f, p := range paths {
		if f.isSubtitle() || f.extra != "" {
			continue
		}
		sizes[f.title]++
		if w, ok := where[p]; ok {
			if members[w.title] == nil {
				members[w.title] = map[*title]int{}
			}
			members[w.title][f.title]++
		}
	}
	// The analysed title of each title: where most of its videos are.
	home := map[*title]*analyse.Title{}
	homeCount := map[*title]int{}
	for at, m := range members {
		for t, n := range m {
			if n > homeCount[t] {
				home[t], homeCount[t] = at, n
			}
		}
	}

	ordered := make([]*file, 0, len(paths))
	for f := range paths {
		ordered = append(ordered, f)
	}
	slices.SortFunc(ordered, func(a, b *file) int { return a.index - b.index })
	for _, f := range ordered {
		p := paths[f]
		w, ok := where[p]
		t := f.title
		switch {
		case f.isSubtitle():
			vp := paths[f.video]
			attached := ok && w.video.Path == vp
			how := "unattached"
			if ok {
				how = "wrong video"
			}
			r.check(fSubtitle, attached, f, how, p, "")
			if baseline && attached {
				languages[f.index] = w.sub.Language
			}
			if lang, known := languages[f.index]; known && attached {
				r.check(fSubLang, w.sub.Language == lang, f, "other language", p, w.sub.Language)
			}
		case f.extra != "":
			good := ok && w.video.Extra != "" && w.title == home[t]
			how := "lost"
			if ok && w.video.Extra == "" {
				how = "not an extra"
			} else if ok {
				how = "other title"
			}
			r.check(fExtra, good, f, how, p, "")
		default:
			r.files++
			if f.original != nil {
				o, ook := where[paths[f.original]]
				good := ok && ook && w.title == o.title && w.video.Extra == "" && sameGroup(w.title, w.video, o.video)
				how := "other title"
				if ok && ook && w.title == o.title {
					how = "not grouped"
				}
				r.check(fVersion, good, f, how, p, "")
			}
			scoreVideo(r, f, p, w, ok, members, sizes)
		}
	}
}

func addTally(t *tally, ok bool) *tally {
	if t == nil {
		t = &tally{}
	}
	t.n++
	if ok {
		t.ok++
	}
	return t
}

// sameGroup says whether two videos are in one group of versions.
func sameGroup(t *analyse.Title, a, b *analyse.Video) bool {
	for _, g := range t.Versions() {
		if slices.Contains(g, a) && slices.Contains(g, b) {
			return true
		}
	}
	return false
}

// scoreVideo checks a title's own video: its title's kind, IDs, name and
// year, its episode's numbers, and its grouping.
func scoreVideo(r *run, f *file, p string, w found, ok bool, members map[*analyse.Title]map[*title]int, sizes map[*title]int) {
	t := f.title
	all := true
	check := func(field string, good bool, how, got string) {
		r.check(field, good, f, how, p, got)
		all = all && good
	}
	if !ok {
		for _, field := range []string{fKind, fName, fGrouping} {
			check(field, false, "lost", "")
		}
		r.check(fAll, false, f, "lost", p, "")
		return
	}
	at := w.title
	wantKind := analyse.Film
	if t.lib == "Series" {
		wantKind = analyse.Show
	}
	how := "as " + string(at.Kind)
	if w.video.Extra != "" {
		how = "as an extra"
	}
	check(fKind, at.Kind == wantKind && w.video.Extra == "", how, string(at.Kind))

	// The ID and the year are checked when the damaged path still says
	// them, outside the names ("Title 2049" doesn't say a year).
	masked := strings.ToLower(render(f, r.style, names))
	if t.idKind != "" && strings.Contains(masked, t.idKind+"-"+t.id) {
		got := map[string]string{"tmdbid": at.IDs.TMDB, "tvdbid": at.IDs.TVDB, "imdbid": at.IDs.IMDB}[t.idKind]
		check(fID, got == t.id, noneOr(got, "none", "other"), got)
	}

	// The name: the title's, or the series' as its files write it.
	gotKey := analyse.Key(at.Name)
	wantKeys := []string{analyse.Key(t.name)}
	if f.show != "" {
		wantKeys = append(wantKeys, analyse.Key(strings.ReplaceAll(f.show, "("+strconv.Itoa(t.year)+")", "")))
	}
	check(fName, slices.Contains(wantKeys, gotKey), nameHow(gotKey, wantKeys[0]), at.Name)

	if saysYear(masked, t.year) {
		check(fYear, at.Year == t.year, noneOr(yearText(at.Year), "none", "other"), strconv.Itoa(at.Year))
	}
	if t.lib == "Series" {
		check(fSeason, w.video.Season == f.season, noneOr(seasonText(w.video.Season), "none", "other"), strconv.Itoa(w.video.Season))
		epHow := "other"
		if len(w.video.Episodes) == 0 {
			epHow = "none"
		} else if len(f.episodes) > 1 && len(w.video.Episodes) != len(f.episodes) {
			epHow = "multi-episode count"
		}
		check(fEpisodes, slices.Equal(w.video.Episodes, f.episodes), epHow, "")
	}

	// Titles the damage made look the same (two films of one name in
	// "Name/" folders once years and IDs are gone) may merge.
	m := members[at]
	split, merged := m[t] < sizes[t], false
	for other := range m {
		if other != t && identity(r.style, other) != identity(r.style, t) {
			merged = true
		}
	}
	how = "split"
	if merged {
		how = "merged"
		if split {
			how = "split+merged"
		}
	}
	check(fGrouping, !split && !merged, how, "")
	r.check(fAll, all, f, "", p, "")
}

// identity is what tells a title from others once damaged: its name, and
// its year and ID when the damage leaves them.
func identity(s style, t *title) string {
	id := analyse.Key(t.name)
	if !s.noYear {
		id += "|" + strconv.Itoa(t.year)
	}
	if !s.noIDs && !s.releaseFolders && !s.flat {
		id += "|" + t.id
	}
	return id
}

// saysYear says whether a path writes a year: its digits, not within a
// longer number.
func saysYear(p string, year int) bool {
	y := strconv.Itoa(year)
	for i := strings.Index(p, y); i >= 0; {
		if (i == 0 || !isDigit(p[i-1])) && (i+4 == len(p) || !isDigit(p[i+4])) {
			return true
		}
		next := strings.Index(p[i+1:], y)
		if next < 0 {
			break
		}
		i += 1 + next
	}
	return false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func noneOr(got, none, other string) string {
	if got == "" {
		return none
	}
	return other
}

func yearText(y int) string {
	if y == 0 {
		return ""
	}
	return strconv.Itoa(y)
}

func seasonText(s int) string {
	if s < 0 {
		return ""
	}
	return strconv.Itoa(s)
}

// nameHow says how a name failed without saying the name.
func nameHow(got, want string) string {
	switch {
	case got == "":
		return "empty"
	case strings.HasPrefix(want, got):
		return "cut short"
	case strings.HasPrefix(got, want):
		return "too long"
	case strings.Contains(got, want):
		return "has more before"
	default:
		return "other"
	}
}

var (
	yearLike    = regexp.MustCompile(`\b(?:19|20)\d\d\b`)
	seasonLike  = regexp.MustCompile(`(?i)\bseason\b|\bsaison\b|\bS\d+\b|\b\d+x\d+\b|\bE\d+\b|\bpart\b|\bcd\d`)
	releaseLike = regexp.MustCompile(`(?i)\b(?:\d{3,4}p|web|hdtv|dvd|bluray|remux|proper|repack|uhd|4k|hd|sd|multi|ac3|aac|dts|x264|hevc|internal|limited)\b`)
	extraLike   = regexp.MustCompile(`(?i)\b(?:trailer|featurette|interview|scene|scenes|short|shorts|clip|sample|extras?|bonus|other|deleted)\b`)
)

// traits describe a title's name without saying it: what in it may
// trouble an analysis.
func traits(name string) string {
	var t []string
	add := func(ok bool, trait string) {
		if ok {
			t = append(t, trait)
		}
	}
	add(yearLike.MatchString(name), "year-like")
	add(!yearLike.MatchString(name) && strings.ContainsAny(name, "0123456789"), "digits")
	add(name != "" && name[0] >= '0' && name[0] <= '9', "starts-with-digit")
	add(strings.Contains(name, " - "), "dash")
	add(strings.Count(name, "-") > strings.Count(name, " - "), "hyphen")
	add(strings.Contains(name, "."), "dot")
	add(strings.ContainsAny(name, "'’"), "apostrophe")
	add(strings.ContainsAny(name, "&,!?"), "&,!?")
	add(strings.ContainsAny(name, "()[]"), "brackets")
	add(strings.ContainsFunc(name, func(r rune) bool { return r > 127 }), "non-ascii")
	add(seasonLike.MatchString(name), "episode-like")
	add(releaseLike.MatchString(name), "release-like")
	add(extraLike.MatchString(name), "extra-like")
	words := len(strings.Fields(name))
	switch {
	case words == 1:
		t = append(t, "1 word")
	case words >= 5:
		t = append(t, "5+ words")
	}
	if len(t) == 0 {
		return "plain"
	}
	return strings.Join(t, ",")
}
