package main

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Reading the answer key: a listing of a library laid out the clean way,
// "Films/Name (Year) [tmdbid-N]/Name (Year) Quality.mkv" and
// "Series/Name (Year) [tmdbid-N]/Season N/Show - S01E02 - Episode Quality.mkv".
// What the names tag is the truth. Files that don't follow that layout are
// left out (and counted): the truth can't be read from them.

// title is a film or a series of the key.
type title struct {
	index   int
	lib     string // "Films" or "Series"
	name    string
	year    int
	idKind  string // "tmdbid", "tvdbid", "imdbid"; "" for none
	id      string
	quality string // its first video's, for release-style folder names
	// translated is another of its names on TMDB, for the translated
	// damage; "" when it has none (or TMDB wasn't asked).
	translated string
	videos     []*file
}

// file is a video or a subtitle of the key, or one the damage adds.
type file struct {
	index    int
	title    *title
	show     string // an episode's series, as its file name writes it
	season   int
	episodes []int
	epName   string
	quality  string
	side     string // a subtitle's tags after its video's name (".es-MX")
	ext      string
	size     int64
	video    *file // a subtitle's video
	// Added by damage: an extra (its kind and name, and how it is laid
	// out), or another version of a video (its label).
	extra      string
	extraName  string
	extraStyle int
	version    string
	original   *file
}

func (f *file) isSubtitle() bool { return f.video != nil }
func (f *file) isEpisode() bool  { return f.title.lib == "Series" && f.extra == "" }

// The names of the clean layout.
var (
	titleFolder = regexp.MustCompile(`^(.+) \(((?:19|20)\d\d)\)(?: \[(tmdbid|tvdbid|imdbid)-([^\]]+)\])?$`)
	quality     = `(?:Bluray|WEBDL|WEBRip|HDTV|SDTV|DVD|Remux|Raw-HD)(?:-\d{3,4}p)?(?: (?:Remux|Proper|REAL|REPACK|v\d))*`
	filmFile    = regexp.MustCompile(`^(.+) \(((?:19|20)\d\d)\) (` + quality + `)((?:\.[A-Za-z-]+)*)\.([A-Za-z0-9]+)$`)
	episodeFile = regexp.MustCompile(`^(.+?) - S(\d{2,})E(\d{2,})(?:-E(\d{2,}))?(?: - (.*?))? (` + quality + `)((?:\.[A-Za-z-]+)*)\.([A-Za-z0-9]+)$`)
	seasonDir   = regexp.MustCompile(`^Season (\d+)$`)
)

var videoExts = map[string]bool{"mkv": true, "mp4": true, "avi": true, "m4v": true}

// corpus is the key, read.
type corpus struct {
	titles  []*title
	files   []*file
	skipped int // files whose truth couldn't be read
}

func readCorpus(name string) (*corpus, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	c := &corpus{}
	byFolder := map[string]*title{}
	type pending struct {
		f    *file
		dir  string
		base string
	}
	var subs []pending
	videosIn := map[string][]pending{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.SplitN(sc.Text(), "\t", 3)
		if len(fields) != 3 || fields[0] != "f" {
			continue
		}
		size, _ := strconv.ParseInt(fields[1], 10, 64)
		p := fields[2]
		parts := strings.Split(p, "/")
		lib := parts[0]
		if !(lib == "Films" && len(parts) == 3 || lib == "Series" && len(parts) == 4) {
			c.skipped++
			continue
		}
		folder := lib + "/" + parts[1]
		t, ok := byFolder[folder]
		if !ok {
			m := titleFolder.FindStringSubmatch(parts[1])
			if m == nil {
				c.skipped++
				continue
			}
			t = &title{index: len(c.titles), lib: lib, name: m[1], idKind: m[3], id: m[4]}
			t.year, _ = strconv.Atoi(m[2])
			byFolder[folder] = t
			c.titles = append(c.titles, t)
		}
		x := &file{title: t, size: size}
		name := parts[len(parts)-1]
		if lib == "Films" {
			m := filmFile.FindStringSubmatch(name)
			if m == nil || m[1] != t.name || m[2] != strconv.Itoa(t.year) {
				c.skipped++
				continue
			}
			x.quality, x.side, x.ext = m[3], m[4], m[5]
		} else {
			sm := seasonDir.FindStringSubmatch(parts[2])
			m := episodeFile.FindStringSubmatch(name)
			if sm == nil || m == nil {
				c.skipped++
				continue
			}
			x.show = m[1]
			x.season, _ = strconv.Atoi(m[2])
			first, _ := strconv.Atoi(m[3])
			last := first
			if m[4] != "" {
				last, _ = strconv.Atoi(m[4])
			}
			for e := first; e <= last; e++ {
				x.episodes = append(x.episodes, e)
			}
			x.epName, x.quality, x.side, x.ext = m[5], m[6], m[7], m[8]
			if sm[1] != strconv.Itoa(x.season) {
				c.skipped++
				continue
			}
		}
		// Only what renders back to its own path is kept: then the
		// damaged names differ from it by the damage alone.
		if render(x, style{}, plain) != strings.TrimPrefix(p, lib+"/") {
			c.skipped++
			continue
		}
		dir := path.Dir(p)
		if videoExts[strings.ToLower(x.ext)] && x.side == "" {
			videosIn[dir] = append(videosIn[dir], pending{x, dir, strings.TrimSuffix(name, path.Ext(name))})
			c.add(x)
		} else if x.ext == "srt" || x.ext == "ass" || x.ext == "sub" {
			subs = append(subs, pending{x, dir, strings.TrimSuffix(name, path.Ext(name))})
		} else {
			c.skipped++
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// A subtitle's video: the one whose name starts its name, the longest.
	for _, s := range subs {
		var best pending
		for _, v := range videosIn[s.dir] {
			if strings.HasPrefix(s.base, v.base+".") && len(v.base) > len(best.base) {
				best = v
			}
		}
		if best.f == nil || s.f.side != s.base[len(best.base):] {
			c.skipped++
			continue
		}
		s.f.video = best.f
		c.add(s.f)
	}
	for _, t := range c.titles {
		if len(t.videos) > 0 {
			t.quality = t.videos[0].quality
		}
	}
	if len(c.files) == 0 {
		return nil, fmt.Errorf("%s: no file follows the clean layout", name)
	}
	return c, nil
}

func (c *corpus) add(x *file) {
	x.index = len(c.files)
	c.files = append(c.files, x)
	if !x.isSubtitle() {
		x.title.videos = append(x.title.videos, x)
	}
}
