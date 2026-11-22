package main

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"
)

// Rendering a file's path, clean or damaged. A style says which damage
// to do; its choices (which separator, which season folder name…) are
// made per title or per file from a hash, so that a run is the same
// every time, and so that a file's anonymous pattern (its path with the
// names masked) takes the same choices as its real path.

// style is a set of damages; the zero style renders the clean layout.
type style struct {
	name           string
	noIDs          bool // ID tags stripped
	noYear         bool // years removed
	punct          bool // names' punctuation, colons and accents changed
	separators     bool // spaces become dots, underscores or hyphens
	letterCase     bool // names in lower or upper case
	releaseFiles   bool // file names as releases name them ("Name.2002.1080p.BluRay.x264-GRP")
	releaseFolders bool // folders too
	seasonRenamed  bool // "S01", "Saison 1", "season01"…
	noSeason       bool // season folders removed: episodes in the series' folder
	flat           bool // every file in the library's folder
	notation       bool // episode notations other than "S01E02"
	nestRelease    bool // a release's folder between the title's folder and its files
	nestGroup      bool // a folder of several titles above the titles' folders
	extras         bool // extras added, in extras folders or with suffixes
	versions       bool // other versions of some videos added
	yearWritten    bool // years written "[2002]", " 2002" or "(2002)" without a space
	sceneTags      bool // release names with languages, editions, sites and bracketed groups
	obfuscated     bool // films' files named by a hash; their folders still say the title
	bareEpisodes   bool // episodes' files named by their numbers only ("S01E02.mkv")
	oneFolder      bool // with flat: every file in one folder below the library's, not in it
	anime          bool // episodes named as anime releases: "[GRP] Show - 05 [1080p]"
	mixed          bool // films and series analysed as one library
	translated     bool // titles named by one of their TMDB translations or alternative titles (stage 2)
}

// year writes a title's year in a folder's or a file's name, with its
// space before: " (2002)", or with yearWritten another way.
func (s style) year(y int, tkey string) string {
	if s.noYear {
		return ""
	}
	n := strconv.Itoa(y)
	if !s.yearWritten {
		return " (" + n + ")"
	}
	return []string{" [" + n + "]", " " + n, "(" + n + ")", "." + n}[s.pick(tkey+"year", 4)]
}

// pick chooses one of n ways for a key, the same every time.
func (s style) pick(key string, n int) int {
	h := fnv.New32a()
	h.Write([]byte(s.name + "|" + key))
	return int(h.Sum32() % uint32(n))
}

// How render masks a path.
const (
	plain   = iota // the real path
	names          // the title's and episode's names replaced by placeholders
	pattern        // names replaced, and digits by "#": an anonymous pattern
)

// render gives a file's path in the library, damaged by s, masked as
// mode says.
func render(f *file, s style, mode int) string {
	mask := mode != plain
	t := f.title
	tkey := strconv.Itoa(t.index)
	name, show, epName := t.name, f.show, f.epName
	if s.translated && t.translated != "" {
		// The title under another of its names: the series' name in its
		// files too.
		if strings.HasPrefix(show, name) {
			show = t.translated + show[len(name):]
		} else if show != "" {
			show = t.translated
		}
		name = t.translated
	}
	if mask {
		if strings.HasPrefix(show, name) {
			show = "<Title>" + show[len(name):]
		} else {
			show = "<Show>"
		}
		name, epName = "<Title>", "<Episode>"
	}
	if s.punct {
		way := s.pick(tkey+"punct", 3)
		name, show = punctuate(name, way), punctuate(show, way)
	}
	if s.noYear {
		show = strings.TrimSpace(strings.ReplaceAll(show, " ("+strconv.Itoa(t.year)+")", ""))
	}
	video := f
	if f.isSubtitle() {
		video = f.video
	}

	// Folders.
	var dirs []string
	folder := name + s.year(t.year, tkey)
	if t.idKind != "" && !s.noIDs && !s.releaseFolders {
		folder += " [" + t.idKind + "-" + t.id + "]"
	}
	if s.releaseFolders && t.lib == "Films" {
		folder = releaseName(s, name, t.year, s.noYear, "", t.quality, tkey)
	} else if s.releaseFolders {
		folder = dotted(folder)
	}
	if s.nestGroup && !s.flat {
		k := []rune(strings.TrimPrefix(strings.ToUpper(t.name), "THE "))
		group := "#"
		if len(k) > 0 && !mask {
			group = string(k[0])
		} else if mask {
			group = "<Letter>"
		}
		dirs = append(dirs, group)
	}
	if s.flat && s.oneFolder {
		dirs = append(dirs, "All")
	}
	if !s.flat {
		dirs = append(dirs, folder)
	}
	if t.lib == "Series" && !s.flat && !s.noSeason {
		season := video.season
		if f.extra != "" && f.extraStyle%2 == 1 {
			season = t.videos[0].season
		}
		if f.extra == "" || f.extraStyle%2 == 1 {
			dirs = append(dirs, seasonName(s, season, show, tkey))
		}
	}

	// The file's name.
	var base string
	if f.extra != "" {
		var extraDir string
		extraDir, base = extraNames(f, s, name, tkey)
		if extraDir != "" {
			dirs = append(dirs, extraDir)
		}
	} else {
		base = videoBase(video, s, name, show, epName, tkey)
		if s.nestRelease && !s.flat && t.lib == "Films" {
			dirs = append(dirs, releaseName(s, name, t.year, s.noYear, video.edition(), video.quality, tkey))
		} else if s.nestRelease && !s.flat {
			// One episode's release, in its season's folder.
			dirs = append(dirs, dotted(show)+"."+episodeNotation(style{}, video, "")+"."+sceneQuality(video.quality)+"-"+group(s, tkey))
		}
	}
	if s.separators || s.letterCase {
		for i := range dirs {
			dirs[i] = recase(s, dirs[i], tkey)
		}
		base = recase(s, base, tkey)
	}
	p := base + f.side + "." + f.ext
	if len(dirs) > 0 {
		p = strings.Join(dirs, "/") + "/" + p
	}
	if mode == pattern {
		p = strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return '#'
			}
			return r
		}, p)
		for _, g := range generic {
			p = g.re.ReplaceAllString(p, g.with)
		}
	}
	return p
}

// generic are the made-up parts of patterns that vary without changing
// the pattern: qualities, release groups, tags.
var generic = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`(?i)(?:PROPER\.)?#+p\.(?:WEB-DL\.DDP#\.#\.H\.###|WEBRip\.x###|BluRay\.x###|BluRay\.REMUX\.HEVC|HDTV\.x###)|\bHDTV\.x###`), "<Release>"},
	{regexp.MustCompile(`(?i)\b(?:Bluray|WEBDL|WEBRip|HDTV|SDTV)(?:-#+p)?(?:[ ._-](?:Remux|Proper|REAL|v#))*`), "<Quality>"},
	{regexp.MustCompile(`(?i)\b(?:GRP|TEAM|CREW|RLS|XYZ)\b`), "<Group>"},
	{regexp.MustCompile(`(?i)\b(?:FRENCH|MULTi|VOSTFR|TRUEFRENCH|iTALiAN)\b`), "<Lang>"},
	{regexp.MustCompile(`(?i)\b(?:EXTENDED|REMASTERED|UNRATED|iNTERNAL)\b`), "<Edition>"},
}

// videoBase is a video's file name without its extension.
func videoBase(v *file, s style, name, show, epName, tkey string) string {
	t := v.title
	if t.lib == "Films" {
		if s.obfuscated {
			return obfuscatedName(v)
		}
		if s.releaseFiles {
			return releaseName(s, name, t.year, s.noYear, v.edition(), v.quality, tkey)
		}
		base := name + s.year(t.year, tkey)
		if v.version != "" {
			return base + v.version
		}
		return base + " " + v.quality
	}
	notation := episodeNotation(s, v, strconv.Itoa(v.index))
	if s.bareEpisodes {
		return notation
	}
	if s.anime {
		res := "1080p"
		if _, r, ok := strings.Cut(v.quality, "-"); ok {
			res = strings.Fields(r)[0]
		}
		eps := fmt.Sprintf("%02d", v.episodes[0])
		if len(v.episodes) > 1 {
			eps += fmt.Sprintf("-%02d", v.episodes[len(v.episodes)-1])
		}
		return "[" + group(s, tkey) + "] " + show + " - " + eps + " [" + res + "]"
	}
	if s.releaseFiles {
		b := dotted(show) + "." + notation
		if s.pick(strconv.Itoa(v.index)+"epname", 2) == 0 {
			b += "." + dotted(epName)
		}
		return sceneWrap(s, b, v.quality, tkey)
	}
	b := show + " - " + notation
	if epName != "" {
		b += " - " + epName
	}
	return b + " " + v.quality
}

// episodeNotation writes an episode's numbers: "S01E02", or with the
// notation damage one of the other ways names write them. Without season
// folders, only notations that say the season are used.
func episodeNotation(s style, v *file, key string) string {
	se, eps := v.season, v.episodes
	if !s.notation {
		n := fmt.Sprintf("S%02dE%02d", se, eps[0])
		if len(eps) > 1 {
			n += fmt.Sprintf("-E%02d", eps[len(eps)-1])
		}
		return n
	}
	last := eps[len(eps)-1]
	if len(eps) > 1 {
		ways := []string{
			fmt.Sprintf("S%02dE%02dE%02d", se, eps[0], last),
			fmt.Sprintf("S%02dE%02d-%02d", se, eps[0], last),
			fmt.Sprintf("%dx%02d-%02d", se, eps[0], last),
			fmt.Sprintf("%dx%02dx%02d", se, eps[0], last),
			fmt.Sprintf("s%02de%02d-e%02d", se, eps[0], last),
		}
		if len(eps) > 2 {
			ways = ways[1:] // "E01E03" would say 1 and 3
			ways[2] = fmt.Sprintf("S%02dE%02d-E%02d", se, eps[0], last)
		}
		return ways[s.pick(key+"multi", len(ways))]
	}
	e := eps[0]
	ways := []string{
		fmt.Sprintf("%dx%02d", se, e),
		fmt.Sprintf("s%02de%02d", se, e),
		fmt.Sprintf("S%dE%d", se, e),
		fmt.Sprintf("S%02d.E%02d", se, e),
		fmt.Sprintf("S%02d E%02d", se, e),
		fmt.Sprintf("Season %d Episode %d", se, e),
	}
	if !s.noSeason && !s.flat && !s.releaseFiles {
		// In a season folder, the season may go unsaid.
		ways = append(ways, fmt.Sprintf("E%02d", e), fmt.Sprintf("Episode %d", e), fmt.Sprintf("Ep%02d", e))
	}
	return ways[s.pick(key+"notation", len(ways))]
}

// seasonName is a season folder's name.
func seasonName(s style, n int, show, tkey string) string {
	if s.releaseFolders {
		return dotted(show) + fmt.Sprintf(".S%02d.1080p.WEB-DL.x264-", n) + group(s, tkey)
	}
	if !s.seasonRenamed {
		return "Season " + strconv.Itoa(n)
	}
	if n == 0 {
		return []string{"Specials", "Season 00", "specials"}[s.pick(tkey+"season0", 3)]
	}
	ways := []string{
		fmt.Sprintf("S%02d", n), fmt.Sprintf("Saison %d", n), fmt.Sprintf("Season %02d", n),
		fmt.Sprintf("season%d", n), fmt.Sprintf("Staffel %d", n), fmt.Sprintf("S%d", n),
	}
	return ways[s.pick(tkey+"season", len(ways))]
}

// releaseName is a film's or a version's name as a release writes it:
// "Name.2002.Directors.Cut.1080p.BluRay.x264-GRP".
func releaseName(s style, name string, year int, noYear bool, edition, q, tkey string) string {
	b := dotted(name)
	if !noYear {
		b += "." + strconv.Itoa(year)
	}
	if edition != "" {
		b += "." + dotted(edition)
	}
	return sceneWrap(s, b, q, tkey)
}

// sceneWrap ends a release's name: its quality and group, and with
// sceneTags, for some titles, a language and an edition before the
// quality, a site before the name, or the group in brackets.
func sceneWrap(s style, b, q, tkey string) string {
	if !s.sceneTags {
		return b + "." + sceneQuality(q) + "-" + group(s, tkey)
	}
	if tag := []string{"", "FRENCH", "MULTi", "VOSTFR", "TRUEFRENCH", "iTALiAN"}[s.pick(tkey+"lang", 6)]; tag != "" {
		b += "." + tag
	}
	if tag := []string{"", "", "EXTENDED", "REMASTERED", "UNRATED", "iNTERNAL"}[s.pick(tkey+"edition", 6)]; tag != "" {
		b += "." + tag
	}
	b += "." + sceneQuality(q)
	switch s.pick(tkey+"wrap", 4) {
	case 0:
		return "www.Example.org - " + b + "-" + group(s, tkey)
	case 1:
		return b + "[" + group(s, tkey) + "]"
	default:
		return b + "-" + group(s, tkey)
	}
}

// obfuscatedName is a name made of a hash, as some downloads have.
func obfuscatedName(f *file) string {
	h := fnv.New64a()
	h.Write([]byte(strconv.Itoa(f.index) + "obfuscated"))
	return fmt.Sprintf("%016x", h.Sum64())
}

// edition is an added version's label when it isn't a quality
// ("Director's Cut"); "" otherwise.
func (f *file) edition() string {
	label := strings.Trim(f.version, " -[]")
	if label == f.quality {
		return ""
	}
	return label
}

// sceneQuality writes a quality ("WEBDL-1080p", "Bluray-2160p Proper") the
// way releases do ("PROPER.1080p.WEB-DL.DDP5.1.H.264").
func sceneQuality(q string) string {
	words := strings.Fields(q)
	source, res, _ := strings.Cut(words[0], "-")
	var b []string
	for _, w := range words[1:] {
		switch w {
		case "Proper", "REPACK", "REAL":
			b = append(b, strings.ToUpper(w))
		}
	}
	if res != "" {
		b = append(b, res)
	}
	switch source {
	case "WEBDL":
		b = append(b, "WEB-DL", "DDP5.1", "H.264")
	case "WEBRip":
		b = append(b, "WEBRip", "x265")
	case "Bluray":
		if strings.Contains(q, "Remux") {
			b = append(b, "BluRay", "REMUX", "HEVC")
		} else {
			b = append(b, "BluRay", "x264")
		}
	case "HDTV", "SDTV":
		b = append(b, "HDTV", "x264")
	default:
		b = append(b, strings.ToUpper(source))
	}
	return strings.Join(b, ".")
}

// group is a made-up release group.
func group(s style, tkey string) string {
	return []string{"GRP", "TEAM", "CREW", "RLS", "XYZ"}[s.pick(tkey+"group", 5)]
}

// dotted writes a name with dots for spaces, as releases do; their
// punctuation mostly goes.
func dotted(name string) string {
	name = strings.NewReplacer(":", "", ",", "", "'", "", " - ", " ", "(", "", ")", "", "[", "", "]", "").Replace(name)
	return strings.Join(strings.Fields(name), ".")
}

// punctuate changes a name's punctuation one of three ways: colons for
// " - ", no accents, no apostrophes or commas, "and" for "&".
func punctuate(name string, way int) string {
	name = strings.NewReplacer("'", "", "’", "", ",", "", "&", "and").Replace(name)
	name = foldAccents(name)
	return strings.ReplaceAll(name, " - ", []string{": ", " ", ". "}[way])
}

var accents = strings.NewReplacer(
	"à", "a", "á", "a", "â", "a", "ä", "a", "ã", "a", "å", "a", "ç", "c", "è", "e", "é", "e", "ê", "e", "ë", "e",
	"ì", "i", "í", "i", "î", "i", "ï", "i", "ñ", "n", "ò", "o", "ó", "o", "ô", "o", "ö", "o", "õ", "o", "ø", "o",
	"ù", "u", "ú", "u", "û", "u", "ü", "u", "ý", "y", "ÿ", "y", "œ", "oe", "æ", "ae",
	"À", "A", "Á", "A", "Â", "A", "Ä", "A", "Ç", "C", "È", "E", "É", "E", "Ê", "E", "Î", "I", "Ï", "I", "Ô", "O", "Ö", "O", "Ù", "U", "Û", "U", "Ü", "U",
)

func foldAccents(s string) string { return accents.Replace(s) }

// recase changes a folder's or a file's name: its spaces (one separator
// per title) and its letters' case.
func recase(s style, n, tkey string) string {
	if s.separators {
		n = strings.ReplaceAll(n, " ", []string{".", "_", "-", "."}[s.pick(tkey+"sep", 4)])
	}
	if s.letterCase {
		switch s.pick(tkey+"case", 2) {
		case 0:
			n = strings.ToLower(n)
		case 1:
			n = strings.ToUpper(n)
		}
	}
	return n
}

// extraNames gives an added extra's folder ("" for none) and file name.
// Odd styles put a series' extra in a season folder.
func extraNames(f *file, s style, name, tkey string) (string, string) {
	t := f.title
	if f.extraStyle >= 4 && t.lib == "Films" {
		// Beside the film, with a suffix.
		base := name + s.year(t.year, tkey)
		switch way := s.pick(strconv.Itoa(f.index)+"suffix", 9); way {
		case 6:
			return "", "trailer"
		case 7:
			return "", releaseName(s, name, t.year, s.noYear, "", t.quality, tkey) + "-sample"
		case 8:
			return "", releaseName(s, name, t.year, s.noYear, "", t.quality, tkey) + ".sample"
		default:
			return "", base + []string{"-trailer", " - featurette", "_behindthescenes", "-deleted", "-interview", "-trailer2"}[way]
		}
	}
	folders := map[string][]string{
		"trailer":           {"Trailers", "trailers", "TRAILERS", "Trailer"},
		"featurette":        {"Featurettes", "featurettes", "Featurette"},
		"behind-the-scenes": {"Behind The Scenes", "behind.the.scenes", "Behind the Scenes", "BehindTheScenes", "Making-Of"},
		"deleted-scene":     {"Deleted Scenes", "deleted scenes", "Deleted.Scenes"},
		"interview":         {"Interviews", "interviews"},
		"extra":             {"Extras", "extras", "EXTRAS", "Extra", "Bonus Features", "Bonus", "Extras & Bonus"},
	}[f.extra]
	return folders[s.pick(strconv.Itoa(f.index)+"folder", len(folders))], f.extraName
}
