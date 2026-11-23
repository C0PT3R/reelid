package analyse

import (
	"regexp"
	"strconv"
	"strings"
)

// Episodes: the numbers a file name gives ("S01E02", "1x02", "Season 1
// Episode 2", multi-episode files such as "S01E01-E02"), and the season a
// folder stands for ("Season 1", "S01", "Saison 1", "Specials").

// mark is an episode's numbers found in a name, and where they are in it.
type mark struct {
	season     int   // -1 when the name doesn't say
	episodes   []int // one, or several for a multi-episode file
	start, end int   // the numbers' place in the name
}

// The episode notations, the most explicit first. Each match ends where
// further episodes of a multi-episode file may follow.
var (
	// "S01E02", "s1e2", "S01.E02", "S01 E02", "S01-E02".
	seasonEpisode = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(s(\d{1,4})[ ._-]?e(\d{1,4}))`)
	// "1x02" (but not "1920x1080").
	crossed = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])((\d{1,2})x(\d{1,3}))`)
	// "Season 1 Episode 2", "Saison 1 Épisode 2".
	spelled = regexp.MustCompile(`(?i)(?:^|[^\pL0-9])((?:season|saison|staffel|temporada|stagione|seizoen)[ ._-]*(\d{1,4})[ ._,-]*(?:episode|épisode|episodio|folge|aflevering|ep)[ ._-]*(\d{1,4}))`)
	// "Episode 2", "Ep02", "E02": the season comes from the folder.
	episodeOnly = regexp.MustCompile(`(?i)(?:^|[^\pL0-9])((?:(?:episode|épisode|episodio|ep)[ ._-]?|e)(\d{1,4}))(?:[^\pL0-9]|$)`)
	// More episodes after the first: "-E03", " - E03", "E03", "-03",
	// "x03", "&E03", or with the season again: "-S01E03", " - S01E03".
	// A number after a spaced hyphen is the episode's name ("S07E14 - 46
	// Minutes"). Group 1 is the season, when written again; group 2 the
	// episode.
	nextEpisode = regexp.MustCompile(`(?i)^(?:-e?|[ ._]-[ ._]?e|e|[ ._]?&[ ._]?e?|x|[ ._]?-[ ._]?s(\d{1,4})[ ._]?e)(\d{1,4})`)
	// A bare episode number, said only in a season folder: "05 - Name",
	// "Show - 05 - Name", "Show 05".
	leadingNumber  = regexp.MustCompile(`^((\d{1,3}))(?:[ ._-]|$)`)
	numberInside   = regexp.MustCompile(` - ((\d{1,3}))(?:[- .\[(]|$)`)
	trailingNumber = regexp.MustCompile(`[ ._]((\d{1,3}))$`)
)

// findEpisode finds the episode numbers in a file name without its
// extension. inSeason says the file is in a season folder, where a bare
// number is an episode's too ("05 - Name").
func findEpisode(s string, inSeason bool) (mark, bool) {
	if m := seasonEpisode.FindStringSubmatchIndex(s); m != nil {
		return withNext(s, m, true), true
	}
	if m := spelled.FindStringSubmatchIndex(s); m != nil {
		return withNext(s, m, true), true
	}
	if m := crossed.FindStringSubmatchIndex(s); m != nil && !isResolutionAt(s, m[2]) {
		return withNext(s, m, true), true
	}
	if m := episodeOnly.FindStringSubmatchIndex(s); m != nil {
		return withNext(s, m, false), true
	}
	if inSeason {
		for _, re := range []*regexp.Regexp{leadingNumber, numberInside, trailingNumber} {
			if m := re.FindStringSubmatchIndex(s); m != nil {
				return withNext(s, m, false), true
			}
		}
	}
	return mark{}, false
}

// isResolutionAt says whether a "1x02"-like match at i is part of a
// longer number, as in a resolution ("1920x1080").
func isResolutionAt(s string, i int) bool {
	return i > 0 && s[i-1] >= '0' && s[i-1] <= '9'
}

// withNext makes a mark from a match: group 1 is the whole notation;
// with a season, groups 2 and 3 are the season and the episode, without
// one, group 2 is the episode. Episodes that follow are added: "E01-E03"
// is a range (1, 2, 3), "E01E02" a list.
func withNext(s string, m []int, hasSeason bool) mark {
	k := mark{season: -1, start: m[2], end: m[3]}
	ep := 2
	if hasSeason {
		k.season, _ = strconv.Atoi(s[m[4]:m[5]])
		ep = 3
	}
	first, _ := strconv.Atoi(s[m[2*ep]:m[2*ep+1]])
	k.episodes = []int{first}
	for {
		n := nextEpisode.FindStringSubmatchIndex(s[k.end:])
		if n == nil {
			break
		}
		after := k.end + n[1]
		if after < len(s) && isAlnum(s[after]) {
			break // "-1080p", "-02x": not an episode
		}
		if n[2] >= 0 {
			// The season again: only the same one continues the file.
			if season, _ := strconv.Atoi(s[k.end+n[2] : k.end+n[3]]); !hasSeason || season != k.season {
				break
			}
		}
		next, _ := strconv.Atoi(s[k.end+n[4] : k.end+n[5]])
		last := k.episodes[len(k.episodes)-1]
		if next <= last || next > last+50 {
			break
		}
		if strings.Contains(s[k.end:k.end+n[4]], "-") {
			for e := last + 1; e < next; e++ {
				k.episodes = append(k.episodes, e)
			}
		}
		k.episodes = append(k.episodes, next)
		k.end = after
	}
	return k
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// Season folders: "Season 1", "Saison 01", "Staffel 1", "S01", "Season1".
// A release's season pack ("Show.S01.1080p.WEB-GROUP") counts too.
var (
	seasonWord = regexp.MustCompile(`(?i)^(?:season|saison|staffel|temporada|stagione|seizoen|series|sæson|säsong|sezon|kausi)[ ._-]*(\d{1,4})(?:$|[^0-9])`)
	seasonS    = regexp.MustCompile(`(?i)^s(\d{1,4})$`)
	// A season pack's marker after its name: "Show.S01.1080p", "Show
	// S-01 MULTi", "Show - Saison 2", "Show.Season.3.WEB".
	seasonPack = regexp.MustCompile(`(?i)(?:^|[ ._-])(?:s-?|(?:season|saison|staffel|temporada|stagione|seizoen)[ ._-]*)(\d{1,2})(?:$|[ ._()\[-])`)
)

// packName is the name before a season pack folder's marker
// ("Show.S01.1080p" is "Show"); "" when the folder has none ("Season 1",
// "S01") or isn't a pack.
func packName(folder string) string {
	if _, ok := seasonFolder(folder); !ok {
		return ""
	}
	m := seasonPack.FindStringIndex(folder)
	if m == nil || !hasName(folder[:m[0]]) {
		return ""
	}
	return folder[:m[0]]
}

// specials are the names of season 0's folder.
var specials = map[string]bool{"specials": true, "special": true, "speciaux": true, "spéciaux": true, "season 0": true}

// seasonFolder says which season a folder name stands for.
func seasonFolder(name string) (int, bool) {
	if specials[strings.ToLower(strings.TrimSpace(name))] {
		return 0, true
	}
	for _, re := range []*regexp.Regexp{seasonWord, seasonS} {
		if m := re.FindStringSubmatch(name); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n, true
		}
	}
	if m := seasonPack.FindStringSubmatch(name); m != nil {
		if _, episode := findEpisode(name, false); !episode {
			n, _ := strconv.Atoi(m[1])
			return n, true
		}
	}
	return 0, false
}
