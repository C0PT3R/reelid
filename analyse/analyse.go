// Package analyse says what the files of a media library are, from their
// paths alone: films or episodes, the title each belongs to (its name and
// year as written, the ID tags in its names), seasons and episodes,
// versions of one title, extras, and subtitles beside their video.
//
// It reads nothing but the paths: no file is opened and no database
// asked. Analyse is a pure function: the same paths give the same result.
package analyse

import (
	"cmp"
	"path"
	"slices"
	"strconv"
	"strings"
)

// Entry is one file of a library: its path, relative to the library's
// folder and with "/" between names, and its size in bytes.
type Entry struct {
	Path string
	Size int64
}

// LibraryKind is what a library holds, from how its files are laid out.
type LibraryKind string

// The kinds of libraries.
const (
	Films  LibraryKind = "films"
	Series LibraryKind = "series"
	Mixed  LibraryKind = "mixed" // films and series, each a tenth or more of the titles
	Empty  LibraryKind = "empty" // no video
)

// TitleKind is what a title is: a film, or a series.
type TitleKind string

// The kinds of titles.
const (
	Film TitleKind = "film"
	Show TitleKind = "series"
)

// Result is what Analyse found in a library.
type Result struct {
	Library LibraryKind `json:"library"`
	Titles  []*Title    `json:"titles"`
	// Unattached are subtitles and extras no video or title could be
	// found for.
	Unattached []string `json:"unattached,omitempty"`
	// Ignored are the files neither videos nor subtitles (images, .nfo…).
	Ignored []string `json:"ignored,omitempty"`
}

// Title is a film or a series, and its files.
type Title struct {
	Kind TitleKind `json:"kind"`
	// Name and Year are as the names write them ("" and 0 when unknown).
	Name string `json:"name"`
	Year int    `json:"year,omitempty"`
	IDs  IDs    `json:"ids"`
	// Folder is the title's own folder; "" for files loose in a folder of
	// several titles.
	Folder string `json:"folder,omitempty"`
	// Videos are a film's videos (several when there are versions or
	// parts), or a series' episodes.
	Videos []*Video `json:"videos"`
	Extras []*Video `json:"extras,omitempty"`
}

// Video is one video file.
type Video struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	// Season and Episodes are an episode's numbers; Season is -1 when no
	// name says it, and Episodes empty when no number was found. Films
	// have neither.
	Season   int   `json:"season"`
	Episodes []int `json:"episodes,omitempty"`
	// Part is which part of a film split in files this is ("cd2" is 2);
	// 0 for a whole film.
	Part int `json:"part,omitempty"`
	// Version is the label that tells this file from the title's other
	// versions, as written ("Bluray-2160p", "Director's Cut").
	Version string `json:"version,omitempty"`
	// Extra is the kind of extra this video is; "" for the title's own.
	Extra     ExtraKind  `json:"extra,omitempty"`
	Subtitles []Subtitle `json:"subtitles,omitempty"`
}

// Versions groups a title's videos that are the same film, or the same
// episodes, in different versions: one group per film (or part), or per
// episode.
func (t *Title) Versions() [][]*Video {
	type item struct {
		season, part int
		episodes     string
	}
	var order []item
	groups := map[item][]*Video{}
	for _, v := range t.Videos {
		var it item
		if t.Kind == Show {
			it = item{season: v.Season, episodes: episodesKey(v.Episodes, v.Path)}
		} else {
			it = item{part: v.Part}
		}
		if _, ok := groups[it]; !ok {
			order = append(order, it)
		}
		groups[it] = append(groups[it], v)
	}
	out := make([][]*Video, 0, len(order))
	for _, it := range order {
		out = append(out, groups[it])
	}
	return out
}

// episodesKey tells episodes apart for Versions; a file without episode
// numbers stands alone.
func episodesKey(episodes []int, p string) string {
	if len(episodes) == 0 {
		return "?" + p
	}
	var b strings.Builder
	for _, e := range episodes {
		b.WriteString(strconv.Itoa(e))
		b.WriteByte(',')
	}
	return b.String()
}

// video is a video file while Analyse works on it.
type video struct {
	v        *Video
	dir      string   // the folder it is in
	fullBase string   // its file name without the extension
	ctx      []string // the folders that tell its title: above an extras folder
	file     named    // what its file name says of the title
	episode  bool     // whether it is an episode
	inSeason bool     // whether it is in a season folder
	// titleAt is the index in ctx of the title's folder; -1 for none.
	titleAt int
	// releaseAt are the folders between the title's and the file, whose
	// names are read for IDs too (a release's folder).
	releaseAt []int
}

// The extensions of videos and subtitles.
var (
	videoExtensions = map[string]bool{
		"mkv": true, "mp4": true, "m4v": true, "avi": true, "mov": true, "wmv": true, "webm": true,
		"ts": true, "m2ts": true, "mts": true, "mpg": true, "mpeg": true, "flv": true, "ogv": true,
		"iso": true, "vob": true, "divx": true, "3gp": true, "rmvb": true,
	}
	subtitleExtensions = map[string]bool{
		"srt": true, "ass": true, "ssa": true, "sub": true, "idx": true, "vtt": true, "sup": true, "smi": true,
	}
)

// Analyse says what the files of one library are. Paths are relative to
// the library's folder; folders themselves need not be listed.
func Analyse(entries []Entry) Result {
	var res Result
	var videos []*video
	var subtitles []Entry
	for _, e := range entries {
		p := strings.TrimPrefix(path.Clean("/"+e.Path), "/")
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(p), "."))
		switch {
		case videoExtensions[ext]:
			videos = append(videos, newVideo(p, e.Size))
		case subtitleExtensions[ext]:
			subtitles = append(subtitles, Entry{Path: p, Size: e.Size})
		default:
			res.Ignored = append(res.Ignored, p)
		}
	}

	findTitleFolders(videos)
	byKey := map[string]*Title{}
	var keys []string
	var extras []*video
	for _, x := range videos {
		if x.v.Extra != "" {
			extras = append(extras, x)
			continue
		}
		key := titleKey(x)
		t, ok := byKey[key]
		if !ok {
			t = &Title{Kind: Film}
			byKey[key] = t
			keys = append(keys, key)
		}
		t.add(x)
	}
	for _, x := range extras {
		t := extraTitle(x, byKey, &keys)
		if t == nil {
			res.Unattached = append(res.Unattached, x.v.Path)
			continue
		}
		t.Extras = append(t.Extras, x.v)
	}
	for _, k := range keys {
		res.Titles = append(res.Titles, byKey[k])
	}
	for _, t := range res.Titles {
		t.settle()
	}
	res.Unattached = append(res.Unattached, attachSubtitles(subtitles, videos)...)
	res.Library = libraryKind(res.Titles)

	slices.SortFunc(res.Titles, func(a, b *Title) int {
		return cmp.Or(strings.Compare(a.Folder, b.Folder), strings.Compare(a.Name, b.Name), cmp.Compare(a.Year, b.Year), strings.Compare(firstPath(a), firstPath(b)))
	})
	slices.Sort(res.Unattached)
	slices.Sort(res.Ignored)
	return res
}

func firstPath(t *Title) string {
	if len(t.Videos) > 0 {
		return t.Videos[0].Path
	}
	if len(t.Extras) > 0 {
		return t.Extras[0].Path
	}
	return ""
}

// newVideo reads what a video's own path says: whether it is an extra,
// a part, an episode, and what its file name says of the title.
func newVideo(p string, size int64) *video {
	dir, name := path.Split(p)
	dir = strings.TrimSuffix(dir, "/")
	var dirs []string
	if dir != "" {
		dirs = strings.Split(dir, "/")
	}
	base := strings.TrimSuffix(name, path.Ext(name))
	x := &video{v: &Video{Path: p, Size: size, Season: -1}, dir: dir, fullBase: base, ctx: dirs, titleAt: -1}

	// An extras folder, but not one right in the library's folder: there,
	// "Shorts" is a collection of films.
	for i := 1; i < len(dirs); i++ {
		if kind, ok := extraFolder(dirs[i]); ok {
			x.v.Extra, x.ctx = kind, dirs[:i]
			break
		}
	}
	if x.v.Extra == "" {
		// A suffix, but not an episode's name: "Show - S01E05 - Interview".
		if kind, before, ok := extraName(base); ok {
			if _, episode := findEpisode(base, false); !episode {
				x.v.Extra, base = kind, before
			}
		}
	}
	if x.v.Extra == "" {
		x.v.Part, base = partOf(base)
	}

	// Season folders, below the title's: the deepest one says the season.
	// Folders below it are a release's ("Show.S01E02.1080p-GROUP"); their
	// names are read for IDs.
	j := len(x.ctx) - 1
	for k := j; k >= 0; k-- {
		if _, ok := seasonFolder(x.ctx[k]); ok {
			for i := k + 1; i <= j; i++ {
				x.releaseAt = append(x.releaseAt, i)
			}
			j = k
			break
		}
	}
	if j >= 0 && len(x.releaseAt) == 0 {
		// Without a season folder, a folder of one episode's release.
		if _, ep := findEpisode(x.ctx[j], false); ep {
			x.releaseAt = append(x.releaseAt, j)
			j--
		}
	}
	inSeason := false
	for j >= 0 {
		n, ok := seasonFolder(x.ctx[j])
		if !ok {
			break
		}
		if !inSeason {
			x.v.Season = n
		}
		inSeason = true
		j--
	}
	x.inSeason = inSeason

	if m, ok := findEpisode(base, inSeason); ok && x.v.Extra == "" {
		x.episode = true
		if m.season >= 0 {
			x.v.Season = m.season
		}
		x.v.Episodes = m.episodes
		x.file = parseName(base[:m.start])
		tail := base[m.end:]
		x.v.Version = strings.Trim(tail[releaseStart(tail):], " -._()[]")
		// "Show (2018) - S01E01": the show's year is before the numbers,
		// but IDs may be anywhere.
		x.file.ids = x.file.ids.or(parseName(base).ids)
	} else {
		x.episode = inSeason && x.v.Extra == ""
		x.file = parseName(base)
		if x.v.Extra == "" {
			x.v.Version = x.file.rest
		}
	}

	// The title's folder: the first one above the season folders, or
	// above a release's folder of the same title ("Quiet Harbor (1995)/Quiet.Harbor.1995.
	// 1080p.BluRay-GROUP/…").
	t := j
	for t >= 1 {
		if _, isSeason := seasonFolder(x.ctx[t-1]); isSeason {
			break
		}
		here := x.ctx[t]
		if releaseStart(here) == len(here) || !sameTitle(parseName(here), parseName(x.ctx[t-1])) {
			break
		}
		x.releaseAt = append(x.releaseAt, t)
		t--
	}
	x.titleAt = t
	return x
}

// findTitleFolders drops the title folders that hold several titles: a
// folder whose videos' names disagree is a collection ("Films/Action/"),
// and its videos are each their own title. A folder above season folders
// is a series' whatever its files say.
func findTitleFolders(videos []*video) {
	byFolder := map[string][]*video{}
	for _, x := range videos {
		if x.titleAt >= 0 && x.v.Extra == "" && !x.inSeason {
			f := strings.Join(x.ctx[:x.titleAt+1], "/")
			byFolder[f] = append(byFolder[f], x)
		}
	}
	for _, xs := range byFolder {
		var kinds []named
		for _, x := range xs {
			if Key(x.file.name) == "" {
				continue
			}
			if !slices.ContainsFunc(kinds, func(n named) bool { return sameTitle(n, x.file) }) {
				kinds = append(kinds, x.file)
			}
			if len(kinds) > 1 {
				break
			}
		}
		if len(kinds) <= 1 {
			continue
		}
		for _, x := range xs {
			x.titleAt, x.releaseAt = -1, nil
		}
	}
}

// titleKey is what tells a video's title from others: its folder, or for
// a loose file, the kind, name and year its file name says.
func titleKey(x *video) string {
	if x.titleAt >= 0 {
		return "d:" + strings.Join(x.ctx[:x.titleAt+1], "/")
	}
	if Key(x.file.name) == "" {
		return "p:" + x.v.Path
	}
	kind := "f"
	if x.episode {
		kind = "s"
	}
	return "n:" + kind + ":" + Key(x.file.name) + ":" + strconv.Itoa(x.file.year)
}

// add puts a video in its title, taking what its names say of the title
// where the title doesn't know yet.
func (t *Title) add(x *video) {
	if x.episode {
		t.Kind = Show
	}
	n := x.file
	if x.titleAt >= 0 {
		t.Folder = strings.Join(x.ctx[:x.titleAt+1], "/")
		folder := parseName(x.ctx[x.titleAt])
		if folder.name != "" {
			n.name = folder.name
		}
		if folder.year != 0 {
			n.year = folder.year
		}
		ids := folder.ids
		for _, i := range x.releaseAt {
			ids = ids.or(parseName(x.ctx[i]).ids)
		}
		n.ids = ids.or(n.ids)
	}
	if t.Name == "" {
		t.Name = n.name
	}
	if t.Year == 0 {
		t.Year = n.year
	}
	t.IDs = t.IDs.or(n.ids)
	t.Videos = append(t.Videos, x.v)
}

// extraTitle finds the title an extra belongs to, or makes one for its
// folder; nil when there is none.
func extraTitle(x *video, byKey map[string]*Title, keys *[]string) *Title {
	if x.titleAt >= 0 {
		key := "d:" + strings.Join(x.ctx[:x.titleAt+1], "/")
		if t, ok := byKey[key]; ok {
			return t
		}
	}
	if k := Key(x.file.name); k != "" {
		for _, kind := range []string{"f", "s"} {
			if t, ok := byKey["n:"+kind+":"+k+":"+strconv.Itoa(x.file.year)]; ok {
				return t
			}
		}
	}
	if x.titleAt < 0 {
		return nil
	}
	// Extras alone in a title's folder: the title is still there.
	key := "d:" + strings.Join(x.ctx[:x.titleAt+1], "/")
	folder := parseName(x.ctx[x.titleAt])
	t := &Title{Kind: Film, Name: folder.name, Year: folder.year, IDs: folder.ids, Folder: key[2:]}
	if x.v.Season >= 0 {
		t.Kind = Show
	}
	byKey[key] = t
	*keys = append(*keys, key)
	return t
}

// settle finishes a title: a series' videos are all episodes, and videos
// and extras go in order.
func (t *Title) settle() {
	if t.Kind == Film {
		for _, v := range t.Videos {
			v.Season, v.Episodes = 0, nil
		}
	}
	for _, v := range t.Extras {
		if t.Kind == Film {
			v.Season = 0
		}
	}
	slices.SortFunc(t.Videos, func(a, b *Video) int {
		return cmp.Or(cmp.Compare(a.Season, b.Season), slices.Compare(a.Episodes, b.Episodes), cmp.Compare(a.Part, b.Part), strings.Compare(a.Path, b.Path))
	})
	slices.SortFunc(t.Extras, func(a, b *Video) int { return strings.Compare(a.Path, b.Path) })
}

// libraryKind says what a library holds from its titles: series when
// most are, films when most are, mixed when each is a tenth or more.
func libraryKind(titles []*Title) LibraryKind {
	films, series := 0, 0
	for _, t := range titles {
		if len(t.Videos) == 0 {
			continue
		}
		if t.Kind == Show {
			series++
		} else {
			films++
		}
	}
	switch total := films + series; {
	case total == 0:
		return Empty
	case films*10 >= total && series*10 >= total:
		return Mixed
	case series > films:
		return Series
	default:
		return Films
	}
}

// subtitleFolders are the folders of a video's subtitles, by their Key.
var subtitleFolders = map[string]bool{"subs": true, "sub": true, "subtitles": true, "subtitle": true, "soustitres": true, "sous titres": true}

// attachSubtitles puts each subtitle with its video: the one in its
// folder whose name starts its name ("Quiet Harbor (1995).en.srt" goes with
// "Quiet Harbor (1995).mkv"), else the video of a subtitle folder's parent
// ("Subs/"), else the only video of its folder. It gives the subtitles
// with no video.
func attachSubtitles(subtitles []Entry, videos []*video) []string {
	byDir := map[string][]*video{}
	for _, x := range videos {
		byDir[x.dir] = append(byDir[x.dir], x)
	}
	var unattached []string
	for _, s := range subtitles {
		dir, name := path.Split(s.Path)
		dir = strings.TrimSuffix(dir, "/")
		base := strings.TrimSuffix(name, path.Ext(name))
		x, tail := byPrefix(byDir[dir], base)
		if x == nil {
			// "Subs/2_English.srt", "Subs/<video's name>/2_English.srt".
			parent, last := path.Split(dir)
			parent = strings.TrimSuffix(parent, "/")
			if subtitleFolders[Key(last)] {
				x, tail = byPrefix(byDir[parent], base)
				if x == nil {
					x, tail = only(byDir[parent]), base
				}
			} else if grand, folder := path.Split(parent); subtitleFolders[Key(folder)] {
				x, _ = byPrefix(byDir[strings.TrimSuffix(grand, "/")], last)
				tail = base
			}
		}
		if x == nil {
			x, tail = only(byDir[dir]), lastWords(base, 3)
		}
		if x == nil {
			unattached = append(unattached, s.Path)
			continue
		}
		sub := subtitleTags(tail)
		sub.Path = s.Path
		x.v.Subtitles = append(x.v.Subtitles, sub)
	}
	return unattached
}

// byPrefix finds the video whose name starts base, the longest one, and
// what follows it in base.
func byPrefix(videos []*video, base string) (*video, string) {
	var best *video
	for _, x := range videos {
		if len(base) > len(x.fullBase) && strings.HasPrefix(base, x.fullBase) && strings.ContainsRune(". _-[", rune(base[len(x.fullBase)])) || base == x.fullBase {
			if best == nil || len(x.fullBase) > len(best.fullBase) {
				best = x
			}
		}
	}
	if best == nil {
		return nil, ""
	}
	return best, base[len(best.fullBase):]
}

// only is the one video of a folder that isn't an extra; nil when there
// are none or several.
func only(videos []*video) *video {
	var one *video
	for _, x := range videos {
		if x.v.Extra != "" {
			continue
		}
		if one != nil {
			return nil
		}
		one = x
	}
	return one
}

// lastWords is the last n dot-separated words of s: where a subtitle's
// tags are when its name isn't its video's.
func lastWords(s string, n int) string {
	words := strings.Split(s, ".")
	if len(words) > n {
		words = words[len(words)-n:]
	}
	return strings.Join(words, ".")
}
