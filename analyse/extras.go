package analyse

import (
	"regexp"
	"strconv"
	"strings"
)

// Extras: videos that belong to a title without being it (trailers,
// featurettes, deleted scenes…), known by the folder they are in
// ("Extras", "Behind The Scenes") or by a suffix of their name
// ("Quiet Harbor (1995)-trailer"). And parts: one film in several files ("cd1",
// "part 2").

// ExtraKind is what kind of extra a video is; "" for a title's own video.
type ExtraKind string

// The kinds of extras.
const (
	Trailer        ExtraKind = "trailer"
	Featurette     ExtraKind = "featurette"
	BehindTheScene ExtraKind = "behind-the-scenes"
	DeletedScene   ExtraKind = "deleted-scene"
	Interview      ExtraKind = "interview"
	Scene          ExtraKind = "scene"
	Short          ExtraKind = "short"
	Clip           ExtraKind = "clip"
	Sample         ExtraKind = "sample"
	OtherExtra     ExtraKind = "extra"
)

// extraFolders are the folder names of extras, by their Key.
var extraFolders = map[string]ExtraKind{
	"extras": OtherExtra, "extra": OtherExtra, "bonus": OtherExtra, "bonuses": OtherExtra, "bonus features": OtherExtra,
	"other": OtherExtra, "others": OtherExtra, "special features": OtherExtra,
	"trailers": Trailer, "trailer": Trailer, "bandes annonces": Trailer, "bande annonce": Trailer,
	"featurettes": Featurette, "featurette": Featurette,
	"behind the scenes": BehindTheScene, "behindthescenes": BehindTheScene, "making of": BehindTheScene, "coulisses": BehindTheScene,
	"deleted scenes": DeletedScene, "deleted": DeletedScene, "deletedscenes": DeletedScene, "scenes coupees": DeletedScene,
	"interviews": Interview, "interview": Interview, "entretiens": Interview,
	"scenes": Scene, "scene": Scene,
	"shorts": Short, "short": Short,
	"clips": Clip, "clip": Clip,
	"samples": Sample, "sample": Sample,
}

// extraSuffixes are the suffixes of extras beside a title's video, by the
// word after the separator.
var extraSuffixes = map[string]ExtraKind{
	"trailer": Trailer, "teaser": Trailer,
	"featurette":      Featurette,
	"behindthescenes": BehindTheScene,
	"deleted":         DeletedScene, "deletedscene": DeletedScene,
	"interview": Interview,
	"scene":     Scene,
	"short":     Short,
	"clip":      Clip,
	"other":     OtherExtra, "extra": OtherExtra,
	"sample": Sample,
}

// extraSuffix is a suffix word after a hyphen or an underscore, maybe
// numbered: "-trailer", "_featurette2", " - trailer 2". A sample may also
// follow a dot (".sample").
var extraSuffix = regexp.MustCompile(`(?i)(?:\s*[-_]\s*|\.)([a-z]+)\s*\d*$`)

// extraFolder says whether a folder name is one of extras'.
func extraFolder(name string) (ExtraKind, bool) {
	key := Key(name)
	if k, ok := extraFolders[key]; ok {
		return k, true
	}
	// Several words of extras: "Extras & Bonus", "Trailers and Teasers".
	var kind ExtraKind
	words := strings.Fields(key)
	for _, w := range words {
		k, ok := extraFolders[w]
		switch {
		case ok && kind == "":
			kind = k
		case ok, extraLinks[w]:
		default:
			return "", false
		}
	}
	return kind, kind != "" && len(words) > 1
}

// extraLinks are the words that may join extras' words in a folder name.
var extraLinks = map[string]bool{"and": true, "et": true, "features": true, "teasers": true, "material": true}

// extraName says whether a file name without its extension is an extra's
// by its suffix, and what precedes the suffix. A name that is only the
// word ("trailer", "sample") is one too, with nothing before it.
func extraName(base string) (ExtraKind, string, bool) {
	if k, ok := extraSuffixes[Key(strings.TrimRight(base, "0123456789 "))]; ok {
		return k, "", true
	}
	m := extraSuffix.FindStringSubmatchIndex(base)
	if m == nil {
		return "", "", false
	}
	word := strings.ToLower(base[m[2]:m[3]])
	k, ok := extraSuffixes[word]
	if !ok || base[m[0]] == '.' && k != Sample {
		return "", "", false
	}
	return k, strings.TrimSpace(base[:m[0]]), true
}

// partSuffix is a part's number at the end of a name: "cd1", "part 2",
// "pt.3", "disc1", " - part1".
var partSuffix = regexp.MustCompile(`(?i)[ ._-]*\b(?:cd|part|pt|disc|disk)[ ._-]?(\d{1,2})$`)

// partOf says which part of a film a name is (0 for none), and the name
// without it.
func partOf(base string) (int, string) {
	m := partSuffix.FindStringSubmatchIndex(base)
	if m == nil || m[0] == 0 {
		return 0, base
	}
	n, _ := strconv.Atoi(base[m[2]:m[3]])
	return n, base[:m[0]]
}
