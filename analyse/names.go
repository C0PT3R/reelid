package analyse

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Reading one folder or file name: the title's name and year as written,
// the ID tags it carries, and what follows the year (a version's label).

// IDs are the database IDs written in names, as text ("" when none):
// TMDB and TVDB numbers ("4821"), IMDb IDs ("tt7654321").
type IDs struct {
	TMDB string `json:"tmdb,omitempty"`
	IMDB string `json:"imdb,omitempty"`
	TVDB string `json:"tvdb,omitempty"`
}

// Empty says whether no ID is known.
func (ids IDs) Empty() bool { return ids == IDs{} }

// or fills the IDs ids lacks from other.
func (ids IDs) or(other IDs) IDs {
	if ids.TMDB == "" {
		ids.TMDB = other.TMDB
	}
	if ids.IMDB == "" {
		ids.IMDB = other.IMDB
	}
	if ids.TVDB == "" {
		ids.TVDB = other.TVDB
	}
	return ids
}

// named is what a name says.
type named struct {
	name string // the title's name, as written ("Brass-Kite", "A.B. the Slow Tide")
	year int    // 0 when none
	ids  IDs
	// rest is what follows the name and year, trimmed: a version's label
	// ("Bluray-2160p", "Director's Cut", "1080p.BluRay.x264-GROUP").
	rest string
}

// An ID tag is a database's name and a number in brackets, braces or
// parentheses: "[tmdbid-4821]", "{tmdb-4821}", "[tmdb=4821]", "(tvdbid 54321)".
// IMDb IDs may also stand alone in brackets: "[tt7654321]".
var (
	tmdbTag = regexp.MustCompile(`(?i)[\[{(]\s*tmdb(?:id)?\s*[-=: ]\s*(\d{1,9})\s*[\]})]`)
	tvdbTag = regexp.MustCompile(`(?i)[\[{(]\s*tvdb(?:id)?\s*[-=: ]\s*(\d{1,9})\s*[\]})]`)
	imdbTag = regexp.MustCompile(`(?i)[\[{(]\s*(?:imdb(?:id)?\s*[-=: ]\s*)?(tt\d{5,10})\s*[\]})]`)
)

// leadingGroup is a bracketed group at the start of a name, and the
// separators after it.
var leadingGroup = regexp.MustCompile(`^\[[^\]]*\][ ._-]*`)

// leadingSite is a web site's name at the start of a name, and the
// separators after it: "www.example.org - ", "www.example.org.",
// "example.com - " (without "www.", only before a dash: "Back.to.the"
// is a name).
var leadingSite = regexp.MustCompile(`(?i)^(?:www\.[a-z0-9-]+\.[a-z]{2,6}[ ._-]+|[a-z0-9-]+\.(?:com|org|net|to|cc|tv|io|me|ws|xyz|club|info|biz)\s*[-–]\s*)`)

// parenYear is a year in parentheses or brackets, "(1995)".
var (
	parenYear = regexp.MustCompile(`[(\[]((?:19|20)\d\d)[)\]]`)
)

// parseName reads a folder name or a file name without its extension.
func parseName(s string) named {
	var n named
	n.ids.TMDB = numberID(tmdbTag, s)
	n.ids.TVDB = numberID(tvdbTag, s)
	if m := imdbTag.FindStringSubmatch(s); m != nil {
		n.ids.IMDB = strings.ToLower(m[1])
	}
	for _, tag := range []*regexp.Regexp{tmdbTag, tvdbTag, imdbTag} {
		s = tag.ReplaceAllString(s, " ")
	}
	s = strings.TrimSpace(s)
	// A release group or a site before the name: "[GRP] Name",
	// "www.example.org - Name".
	for _, lead := range []*regexp.Regexp{leadingGroup, leadingSite} {
		if m := lead.FindStringIndex(s); m != nil && m[1] < len(s) {
			s = s[m[1]:]
		}
	}

	// The year: one in parentheses after some name comes first ("2047
	// (2019)"; "Remastered 2015" after it is a label). Otherwise the last
	// one before the release's description, with a name before it
	// ("Orbit.2047.2017.1080p" is from 2017; "2047" alone is a name).
	nameEnd, restStart := len(s), len(s)
	for _, m := range parenYear.FindAllStringSubmatchIndex(s, -1) {
		if hasName(s[:m[0]]) {
			n.year, _ = strconv.Atoi(s[m[2]:m[3]])
			nameEnd, restStart = m[0], m[1]
			break
		}
	}
	if n.year == 0 {
		limit := releaseStart(s)
		for _, at := range years(s[:limit]) {
			if hasName(s[:at]) {
				n.year, _ = strconv.Atoi(s[at : at+4])
				nameEnd, restStart = at, at+4
			}
		}
	}
	if n.year == 0 {
		// Without a year, the name stops where the release's description or
		// a bracket starts ("The.Candid.Hour.1080p.WEB", "Quiet Harbor [Remastered]").
		nameEnd = releaseStart(s)
		if i := strings.IndexAny(s, "[{"); i > 0 && i < nameEnd {
			nameEnd = i
		}
		restStart = nameEnd
	}
	n.name = cleanName(s[:nameEnd])
	n.rest = strings.Trim(s[restStart:], " -._()[]{}")
	return n
}

// years finds where years are in s: four digits from 1900 to 2099 with a
// separator, or nothing, on each side.
func years(s string) []int {
	var at []int
	for i := 0; i+4 <= len(s); i++ {
		y := s[i : i+4]
		if (y[:2] != "19" && y[:2] != "20") || !isDigits(y[2:]) {
			continue
		}
		if (i == 0 || isYearSep(s[i-1])) && (i+4 == len(s) || isYearSep(s[i+4])) {
			at = append(at, i)
		}
	}
	return at
}

func isYearSep(c byte) bool { return strings.IndexByte(" ._-()[]", c) >= 0 }

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// hasName says whether s holds a name: some letter or digit.
func hasName(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0
}

// cleanName turns the name part of a folder or file name into the name as
// written: release names' dots and underscores become spaces ("The.Candid.Hour"
// is "The Candid Hour"), but a name with spaces keeps its dots ("A.B. the
// Slow Tide"); a name only joined by hyphens is split too.
func cleanName(s string) string {
	switch {
	case strings.Contains(s, " "):
		s = strings.ReplaceAll(s, "_", " ")
	case strings.ContainsAny(s, "._"):
		s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	case strings.Count(s, "-") >= 2:
		s = strings.ReplaceAll(s, "-", " ")
	}
	s = strings.Join(strings.Fields(s), " ")
	return strings.Trim(s, " -._([{,")
}

// releaseStart is where the release's description starts in s ("1080p",
// "BluRay", "x264"…): the first such word after the first one; len(s)
// when there is none.
func releaseStart(s string) int {
	// Tags in capitals ("FRENCH", "EXTENDED") are a release's only where
	// the whole name isn't in capitals.
	capitals := s == strings.ToUpper(s)
	first := true
	i := 0
	for i < len(s) {
		// Words are separated by spaces, dots, underscores and brackets;
		// hyphens stay inside words ("WEB-DL", "x264-GROUP").
		j := i
		for j < len(s) && !isWordSep(s[j]) {
			j++
		}
		if j > i {
			if !first && (isReleaseWord(s[i:j]) || !capitals && isTag(s[i:j])) {
				return i
			}
			first = false
		}
		i = j + 1
	}
	return len(s)
}

func isWordSep(c byte) bool {
	return c == ' ' || c == '.' || c == '_' || c == '[' || c == ']' || c == '(' || c == ')' || c == '{' || c == '}'
}

// releaseWords are words of a release's description, never of a title's
// name. Languages ("French") and editions ("Extended") aren't here: they
// are names too.
var releaseWords = map[string]bool{
	"4k": true, "uhd": true, "sd": true, "hd": true, "fhd": true,
	"bluray": true, "blu-ray": true, "bdrip": true, "brrip": true, "bdremux": true, "remux": true,
	"web": true, "web-dl": true, "webdl": true, "webrip": true, "web-rip": true, "hdtv": true, "sdtv": true,
	"hdrip": true, "dvdrip": true, "dvdr": true, "dvd": true, "dvd5": true, "dvd9": true, "pdtv": true, "raw-hd": true,
	"x264": true, "x265": true, "h264": true, "h265": true, "hevc": true, "avc": true, "av1": true, "xvid": true, "divx": true, "vc-1": true,
	"10bit": true, "8bit": true, "hdr": true, "hdr10": true, "hdr10+": true, "dovi": true, "sdr": true,
	"aac": true, "ac3": true, "eac3": true, "dts": true, "dts-hd": true, "truehd": true, "atmos": true, "flac": true,
	"ddp": true, "ddp5": true, "dd5": true, "dd+": true, "opus": true,
	"proper": true, "repack": true, "rerip": true, "internal": true, "limited": true,
	"multi": true, "vostfr": true, "truefrench": true, "subfrench": true, "vff": true, "vfq": true, "vf2": true,
	"amzn": true, "nf": true, "dsnp": true, "hmax": true, "atvp": true, "hulu": true,
}

// tags are words releases write in capitals after a name, but that are
// names' words too when written as words: "FRENCH" is a tag, "French" a
// name's word ("The French Window").
var tags = map[string]bool{
	"french": true, "german": true, "italian": true, "spanish": true, "english": true, "japanese": true,
	"dubbed": true, "subbed": true, "extended": true, "remastered": true, "unrated": true, "uncut": true,
	"imax": true, "hybrid": true, "complete": true, "dc": true,
}

// isTag says whether a word is a release's tag: one of tags, not
// written as a name's word (all lower case, or capitalised).
func isTag(w string) bool {
	lower := strings.ToLower(w)
	if !tags[lower] || w == lower {
		return false
	}
	rest := w[1:]
	return rest != strings.ToLower(rest) // "FRENCH", "iTALiAN"; not "French"
}

// resolution is a video's height as written: "1080p", "720i", "2160p".
var resolution = regexp.MustCompile(`^\d{3,4}[pi]$`)

// isReleaseWord says whether a word describes a release: "1080p",
// "WEB-DL", "Bluray-2160p", "x264-GROUP", "H.264" (as "H" then "264": the
// "H" alone isn't one).
func isReleaseWord(w string) bool {
	w = strings.ToLower(strings.Trim(w, "-"))
	if releaseWords[w] || resolution.MatchString(w) {
		return true
	}
	before, _, found := strings.Cut(w, "-")
	return found && (releaseWords[before] || resolution.MatchString(before))
}

// numberID is the number a tag finds in s, as TMDB and TVDB write them
// ("04821" is 4821, and 0 is none); "" without one.
func numberID(tag *regexp.Regexp, s string) string {
	m := tag.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	if id, _ := strconv.Atoi(m[1]); id > 0 {
		return strconv.Itoa(id)
	}
	return ""
}

// Key is a name reduced for comparing: lower case, accents and
// apostrophes gone, "&" as "and", every other punctuation a space.
// "Élodie: Part 2" and "elodie - part.2" have the same key.
func Key(name string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(name) {
		if two, ok := ligatures[r]; ok {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteString(two)
			continue
		}
		r = fold(r)
		switch {
		case r == '\'' || r == '’' || r == '`':
			continue
		case r == '&':
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString("and")
			space = true
			continue
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		default:
			space = true
		}
	}
	return b.String()
}

// fold takes the accent off a Latin letter ("é" is "e"); other runes stay.
func fold(r rune) rune {
	if r < 0xc0 {
		return r
	}
	if f, ok := folds[r]; ok {
		return f
	}
	return r
}

// ligatures are letters written as two in plain Latin ("œ" is "oe").
var ligatures = map[rune]string{'œ': "oe", 'æ': "ae", 'ß': "ss", 'ĳ': "ij"}

var folds = func() map[rune]rune {
	m := map[rune]rune{}
	for base, accented := range map[rune]string{
		'a': "àáâãäåāăą", 'c': "çćĉċč", 'd': "ďđ", 'e': "èéêëēĕėęě", 'g': "ĝğġģ",
		'h': "ĥħ", 'i': "ìíîïĩīĭįı", 'j': "ĵ", 'k': "ķ", 'l': "ĺļľŀł", 'n': "ñńņňŉ",
		'o': "òóôõöøōŏő", 'r': "ŕŗř", 's': "śŝşšș", 't': "ţťŧț", 'u': "ùúûüũūŭůűų",
		'w': "ŵ", 'y': "ýÿŷ", 'z': "źżž",
	} {
		for _, r := range accented {
			m[r] = base
		}
	}
	return m
}()

// sameTitle says whether two names may be the same title: their keys are
// equal or one starts the other ("Quiet Harbor" and "Quiet Harbor Director's Cut"), and
// their years, when both are known, are the same. An empty name agrees
// with anything.
func sameTitle(a, b named) bool {
	if a.year != 0 && b.year != 0 && a.year != b.year {
		return false
	}
	if a.ids.TMDB != "" && b.ids.TMDB != "" && a.ids.TMDB != b.ids.TMDB {
		return false
	}
	ka, kb := Key(a.name), Key(b.name)
	if ka == "" || kb == "" {
		return true
	}
	if len(ka) > len(kb) {
		ka, kb = kb, ka
	}
	return kb == ka || strings.HasPrefix(kb, ka+" ")
}
