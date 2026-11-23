package analyse

import (
	"fmt"
	"maps"
	"slices"
	"testing"
)

// The names in these tests are made up.

func TestParseName(t *testing.T) {
	tests := []struct {
		in   string
		name string
		year int
		ids  IDs
		rest string
	}{
		{"Quiet Harbor (1995) [tmdbid-4821]", "Quiet Harbor", 1995, IDs{TMDB: "4821"}, ""},
		{"Quiet Harbor (1995) {tmdb-04821} [imdbid-tt7654321]", "Quiet Harbor", 1995, IDs{TMDB: "4821", IMDB: "tt7654321"}, ""},
		{"Lantern Bay (2018) [tvdbid-54321]", "Lantern Bay", 2018, IDs{TVDB: "54321"}, ""},
		{"Quiet Harbor (1995) Bluray-2160p", "Quiet Harbor", 1995, IDs{}, "Bluray-2160p"},
		{"Quiet Harbor (1995) - Director's Cut", "Quiet Harbor", 1995, IDs{}, "Director's Cut"},
		{"Quiet.Harbor.1995.1080p.BluRay.x264-GRP", "Quiet Harbor", 1995, IDs{}, "1080p.BluRay.x264-GRP"},
		{"Quiet_Harbor_1995_720p", "Quiet Harbor", 1995, IDs{}, "720p"},
		{"The.Candid.Hour.1080p.WEB-DL", "The Candid Hour", 0, IDs{}, "1080p.WEB-DL"},
		{"Quiet Harbor Bluray-2160p", "Quiet Harbor", 0, IDs{}, "Bluray-2160p"},
		{"1999", "1999", 0, IDs{}, ""},
		{"2047 (2019)", "2047", 2019, IDs{}, ""},
		{"Orbit.2047.2017.2160p", "Orbit 2047", 2017, IDs{}, "2160p"},
		{"Quiet Harbor (1995) - Remastered 2015", "Quiet Harbor", 1995, IDs{}, "Remastered 2015"},
		{"A.B. the Slow Tide (1982)", "A.B. the Slow Tide", 1982, IDs{}, ""},
		{"Brass-Kite (2002)", "Brass-Kite", 2002, IDs{}, ""},
		{"Brass-Kite Bluray-1080p", "Brass-Kite", 0, IDs{}, "Bluray-1080p"},
		{"Quiet Harbor [tt7654321]", "Quiet Harbor", 0, IDs{IMDB: "tt7654321"}, ""},
		{"[GRP] Lantern Bay [1080p]", "Lantern Bay", 0, IDs{}, "1080p"},
		{"www.example.org - Quiet.Harbor.FRENCH.1080p.WEB-GRP", "Quiet Harbor", 0, IDs{}, "FRENCH.1080p.WEB-GRP"},
		{"The.French.Window.iTALiAN.EXTENDED.720p", "The French Window", 0, IDs{}, "iTALiAN.EXTENDED.720p"},
		{"THE.FRENCH.WINDOW.720P", "THE FRENCH WINDOW", 0, IDs{}, "720P"},
		{"Back.to.the.Harbor.1990.720p", "Back to the Harbor", 1990, IDs{}, "720p"},
		{"Call.Me.Later.2002.720p", "Call Me Later", 2002, IDs{}, "720p"},
		{"example.com - Call.Me.Later.2002", "Call Me Later", 2002, IDs{}, ""},
		{"Lantern.Bay.S02.MULTi.1080p.WEB-GRP", "Lantern Bay", 0, IDs{}, "S02.MULTi.1080p.WEB-GRP"},
		{"Lantern Bay - Saison 2", "Lantern Bay", 0, IDs{}, "Saison 2"},
		{"Lantern.Bay.S02.2012.1080p.WEB-GRP", "Lantern Bay", 0, IDs{}, "S02.2012.1080p.WEB-GRP"},
		{"Lantern Bay - Saison 6 (2014)", "Lantern Bay", 0, IDs{}, "Saison 6 (2014"},
		{"Lantern.Bay.2010.S02.1080p", "Lantern Bay", 2010, IDs{}, "S02.1080p"},
		{"Lantern Bay S-01 MULTi [1080p]", "Lantern Bay", 0, IDs{}, "S-01 MULTi [1080p"},
	}
	for _, tt := range tests {
		n := parseName(tt.in)
		if n.name != tt.name || n.year != tt.year || n.ids != tt.ids || n.rest != tt.rest {
			t.Errorf("parseName(%q) = %q %d %+v %q, want %q %d %+v %q", tt.in, n.name, n.year, n.ids, n.rest, tt.name, tt.year, tt.ids, tt.rest)
		}
	}
}

func TestKey(t *testing.T) {
	for _, pair := range [][2]string{
		{"Élodie: Part 2", "elodie - part.2"},
		{"Rock & Roll", "rock and roll"},
		{"Director's Cut", "directors cut"},
		{"ÉTÉ", "ete"},
		{"À cœur ouvert", "a coeur ouvert"},
	} {
		if Key(pair[0]) != Key(pair[1]) {
			t.Errorf("Key(%q) = %q, Key(%q) = %q", pair[0], Key(pair[0]), pair[1], Key(pair[1]))
		}
	}
}

func TestFindEpisode(t *testing.T) {
	tests := []struct {
		in       string
		inSeason bool
		season   int
		episodes []int
	}{
		{"Lantern Bay - S01E02 - Pilot WEBDL-1080p", false, 1, []int{2}},
		{"lantern.bay.s1e2.720p", false, 1, []int{2}},
		{"Lantern Bay S01.E02", false, 1, []int{2}},
		{"Lantern Bay S01 E02", false, 1, []int{2}},
		{"Lantern Bay - S01E01-E03 - Long Night", false, 1, []int{1, 2, 3}},
		{"Lantern Bay - S01E01E02", false, 1, []int{1, 2}},
		{"Lantern Bay - S01E01-02", false, 1, []int{1, 2}},
		{"Lantern Bay - S01E05 - 1080p", false, 1, []int{5}},
		{"Lantern Bay - S01E05 - 1985", false, 1, []int{5}},
		{"Lantern Bay - S07E14 - 46 Hours", false, 7, []int{14}},
		{"Lantern Bay - S01E01 - E02 - Two Parts", false, 1, []int{1, 2}},
		{"Lantern Bay - S01E01 - S01E02 - Two Parts", false, 1, []int{1, 2}},
		{"Lantern Bay.S01E01-S01E02.720p", false, 1, []int{1, 2}},
		{"Lantern Bay - S01E01 - S02E02", false, 1, []int{1}},
		{"Lantern Bay 1x02", false, 1, []int{2}},
		{"Lantern Bay 1x01-02", false, 1, []int{1, 2}},
		{"Lantern Bay 1x01x02", false, 1, []int{1, 2}},
		{"Lantern Bay Season 2 Episode 7", false, 2, []int{7}},
		{"Lantern Bay Saison 2 Épisode 7", false, 2, []int{7}},
		{"Lantern Bay - Episode 7", false, -1, []int{7}},
		{"Lantern Bay Ep07", false, -1, []int{7}},
		{"Lantern Bay E07", false, -1, []int{7}},
		{"07 - Pilot", true, -1, []int{7}},
		{"Lantern Bay - 07 - Pilot", true, -1, []int{7}},
		{"[GRP] Lantern Bay - 07-08 [1080p]", true, -1, []int{7, 8}},
	}
	for _, tt := range tests {
		m, ok := findEpisode(tt.in, tt.inSeason)
		if !ok || m.season != tt.season || !slices.Equal(m.episodes, tt.episodes) {
			t.Errorf("findEpisode(%q) = %v %d %v, want %d %v", tt.in, ok, m.season, m.episodes, tt.season, tt.episodes)
		}
	}
	for _, in := range []string{"Quiet Harbor (1995) 1920x1080", "Quiet Harbor (1995) Bluray-1080p", "07 - Pilot", "Se7en Hours (1995)"} {
		if m, ok := findEpisode(in, false); ok {
			t.Errorf("findEpisode(%q) = %+v, want none", in, m)
		}
	}
}

func TestSeasonFolder(t *testing.T) {
	for in, want := range map[string]int{
		"Season 1": 1, "Season 01": 1, "season1": 1, "S02": 2, "Saison 3": 3, "Staffel 4": 4,
		"Specials": 0, "Season 0": 0, "Lantern.Bay.S02.1080p.WEB-GRP": 2,
		"Lantern Bay - Saison 2": 2, "Lantern.Bay.Season.3.WEB": 3, "Lantern Bay S-01 MULTi [1080p]": 1,
		"Lantern Bay - Saison 6 (2014)": 6,
	} {
		if n, ok := seasonFolder(in); !ok || n != want {
			t.Errorf("seasonFolder(%q) = %d %v, want %d", in, n, ok, want)
		}
	}
	for in, want := range map[string]ExtraKind{"Extras & Bonus": OtherExtra, "Trailers and Teasers": Trailer, "behind.the.scenes": BehindTheScene} {
		if k, ok := extraFolder(in); !ok || k != want {
			t.Errorf("extraFolder(%q) = %q %v, want %q", in, k, ok, want)
		}
	}
	for _, in := range []string{"Season of Storms (2011)", "Lantern Bay (2018)", "Lantern.Bay.S02E01.1080p"} {
		if n, ok := seasonFolder(in); ok {
			t.Errorf("seasonFolder(%q) = %d, want none", in, n)
		}
	}
}

func TestSubtitleTags(t *testing.T) {
	tests := []struct {
		in   string
		want Subtitle
	}{
		{".en", Subtitle{Language: "en"}},
		{".MX.es", Subtitle{Language: "es-MX"}},
		{".es-MX.hi", Subtitle{Language: "es-MX", HearingImpaired: true}},
		{".hi", Subtitle{Language: "hi"}},
		{".fre.forced", Subtitle{Language: "fr", Forced: true}},
		{".English.sdh", Subtitle{Language: "en", HearingImpaired: true}},
		{"2_English", Subtitle{Language: "en"}},
		{".pt_br", Subtitle{Language: "pt-BR"}},
		{"", Subtitle{}},
	}
	for _, tt := range tests {
		if got := subtitleTags(tt.in); got != tt.want {
			t.Errorf("subtitleTags(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestExtraName(t *testing.T) {
	tests := []struct {
		in     string
		kind   ExtraKind
		before string
		ok     bool
	}{
		{"Quiet Harbor (1995)-trailer", Trailer, "Quiet Harbor (1995)", true},
		{"Quiet Harbor (1995) - featurette2", Featurette, "Quiet Harbor (1995)", true},
		{"Quiet Harbor (1995)_behindthescenes", BehindTheScene, "Quiet Harbor (1995)", true},
		{"quiet.harbor.1995.sample", Sample, "quiet.harbor.1995", true},
		{"trailer", Trailer, "", true},
		{"The Interview", "", "", false},
		{"the.trailer.1080p", "", "", false},
	}
	for _, tt := range tests {
		kind, before, ok := extraName(tt.in)
		if kind != tt.kind || before != tt.before || ok != tt.ok {
			t.Errorf("extraName(%q) = %q %q %v, want %q %q %v", tt.in, kind, before, ok, tt.kind, tt.before, tt.ok)
		}
	}
}

// analyseTitles runs Analyse on paths, all of size 1.
func analyseTitles(t *testing.T, paths ...string) Result {
	t.Helper()
	var entries []Entry
	for _, p := range paths {
		entries = append(entries, Entry{Path: p, Size: 1})
	}
	return Analyse(entries)
}

func TestReleaseFolderInSeason(t *testing.T) {
	res := analyseTitles(t, "Lantern Bay (2018)/Season 1/Lantern.Bay.S01E02.1080p.WEB-GRP/lantern.bay.s01e02.1080p.web-grp.mkv")
	if len(res.Titles) != 1 || res.Titles[0].Folder != "Lantern Bay (2018)" || res.Titles[0].Videos[0].Season != 1 {
		t.Errorf("titles: %+v", res.Titles[0])
	}
}

// Downloads as they are: season packs and release folders in a folder
// of many titles, one wrapped in a copy's folder.
func TestDownloads(t *testing.T) {
	res := analyseTitles(t,
		"complete/Lantern.Bay.S01.1080p.WEB-GRP/Lantern.Bay.S01E01.1080p.WEB-GRP.mkv",
		"complete/Lantern.Bay.S01.1080p.WEB-GRP/Lantern.Bay.S01E02.1080p.WEB-GRP.mkv",
		"complete/Lantern Bay - Saison 2/Lantern.Bay.S02E01.FRENCH.720p.mkv",
		"complete/Quiet Harbor S-01 MULTi [1080p]/S01E01 - Pilot.mkv",
		"complete/Quiet Harbor S-01 MULTi [1080p]/S01E02 - Storm.mkv",
		"complete/Quiet Harbor S-01 MULTi [1080p]/0103 Quiet Harbor S01E03 - Tide.mkv",
		"complete/Quiet Harbor S-01 MULTi [1080p]/Quiet Harbor 104 MULTI Calm.mkv",
		"complete/Brass.Kite.2002.1080p.BluRay-GRP/Brass.Kite.2002.1080p.BluRay-GRP.mkv",
		"complete/Amber.Rail.S03E04.720p.WEB-GRP.mkv",
		"complete/Copper.Sky.1998.720p.mkv",
		"copies/Teal.Rail.S01.WEB-GRP--1a2b3c/Teal.Rail.S01.WEB-GRP/Teal.Rail.S01E01.WEB-GRP.mkv",
		"copies/Teal.Rail.S02E01.WEB-GRP--4d5e6f/Teal.Rail.S02E01.WEB-GRP/Teal.Rail.S02E01.WEB-GRP.mkv",
		"copies/Long.Tale.2011.720p-GRP.mkv",
	)
	got := map[string]int{}
	for _, ti := range res.Titles {
		got[fmt.Sprintf("%s %s %d", ti.Kind, ti.Name, ti.Year)] += len(ti.Videos)
	}
	want := map[string]int{
		"series Lantern Bay 0": 3, "series Quiet Harbor 0": 4, "film Brass Kite 2002": 1,
		"series Amber Rail 0": 1, "film Copper Sky 1998": 1, "series Teal Rail 0": 2, "film Long Tale 2011": 1,
	}
	if !maps.Equal(got, want) {
		t.Errorf("titles: %v, want %v", got, want)
	}
}

func TestFilms(t *testing.T) {
	res := analyseTitles(t,
		"Quiet Harbor (1995) [tmdbid-4821]/Quiet Harbor (1995) Bluray-2160p.mkv",
		"Quiet Harbor (1995) [tmdbid-4821]/Quiet Harbor (1995) - Director's Cut.mkv",
		"Quiet Harbor (1995) [tmdbid-4821]/Quiet Harbor (1995) Bluray-2160p.en.srt",
		"Quiet Harbor (1995) [tmdbid-4821]/Quiet Harbor (1995)-trailer.mkv",
		"Quiet Harbor (1995) [tmdbid-4821]/Behind The Scenes/Making of.mkv",
		"Quiet Harbor (1995) [tmdbid-4821]/poster.jpg",
		"Brass Kite (2002)/Brass.Kite.2002.1080p.BluRay-GRP/Brass.Kite.2002.1080p.BluRay-GRP.mkv",
		"Loose Ends.2011.720p.WEB.mkv",
		"Action/Amber Rail (2004).mkv",
		"Action/Teal Rail (2006).mkv",
		"Long Tale (1990)/Long Tale (1990) cd1.avi",
		"Long Tale (1990)/Long Tale (1990) cd2.avi",
	)
	if res.Library != Films {
		t.Errorf("library = %s, want films", res.Library)
	}
	if len(res.Titles) != 6 {
		for _, ti := range res.Titles {
			t.Logf("%+v", *ti)
		}
		t.Fatalf("%d titles, want 6", len(res.Titles))
	}
	byName := map[string]*Title{}
	for _, ti := range res.Titles {
		byName[ti.Name] = ti
	}
	q := byName["Quiet Harbor"]
	if q == nil || q.Year != 1995 || q.IDs.TMDB != "4821" || len(q.Videos) != 2 || len(q.Extras) != 2 {
		t.Fatalf("Quiet Harbor: %+v", q)
	}
	if vs := q.Versions(); len(vs) != 1 || len(vs[0]) != 2 {
		t.Errorf("Quiet Harbor versions: %v", vs)
	}
	// In path order: " - Director's Cut" comes before " Bluray-2160p".
	if q.Videos[0].Version != "Director's Cut" || q.Videos[1].Version != "Bluray-2160p" {
		t.Errorf("versions: %q %q", q.Videos[0].Version, q.Videos[1].Version)
	}
	if subs := q.Videos[1].Subtitles; len(subs) != 1 || subs[0].Language != "en" {
		t.Errorf("subtitles: %+v", subs)
	}
	if c := byName["Brass Kite"]; c == nil || c.Folder != "Brass Kite (2002)" || c.Year != 2002 {
		t.Errorf("Brass Kite: %+v", c)
	}
	if l := byName["Loose Ends"]; l == nil || l.Year != 2011 || l.Folder != "" {
		t.Errorf("Loose Ends: %+v", l)
	}
	if r := byName["Amber Rail"]; r == nil || r.Year != 2004 || r.Folder != "" {
		t.Errorf("Amber Rail: %+v", r)
	}
	if l := byName["Long Tale"]; l == nil || len(l.Versions()) != 2 {
		t.Errorf("Long Tale parts: %+v", l)
	}
	if len(res.Ignored) != 1 {
		t.Errorf("ignored: %v", res.Ignored)
	}
}

func TestSeries(t *testing.T) {
	res := analyseTitles(t,
		"Lantern Bay (2018) [tmdbid-79]/Season 1/Lantern Bay - S01E01 - Pilot WEBDL-1080p.mkv",
		"Lantern Bay (2018) [tmdbid-79]/Season 1/Lantern Bay - S01E02-E03 - Storm WEBDL-1080p.mkv",
		"Lantern Bay (2018) [tmdbid-79]/Season 1/Lantern Bay - S01E02-E03 - Storm WEBDL-1080p.MX.es.srt",
		"Lantern Bay (2018) [tmdbid-79]/Season 1/Lantern Bay - S01E01 - Pilot Bluray-2160p.mkv",
		"Lantern Bay (2018) [tmdbid-79]/Saison 2/04 - Return.mkv",
		"Lantern Bay (2018) [tmdbid-79]/Specials/Lantern Bay - S00E01.mkv",
		"Lantern Bay (2018) [tmdbid-79]/Extras/Cast Interview.mkv",
		"Lantern Bay (2018) [tmdbid-79]/Season 2/Lantern Bay - S02E05 - Interview.mkv",
		"Moth Hours/moth.hours.s02e05.720p.hdtv.x264-grp.mkv",
		"Flat Show 3x04 Something.mkv",
		"Flat Show 3x05 Other.mkv",
	)
	if res.Library != Series {
		t.Errorf("library = %s, want series", res.Library)
	}
	if len(res.Titles) != 3 {
		for _, ti := range res.Titles {
			t.Logf("%+v", *ti)
		}
		t.Fatalf("%d titles, want 3", len(res.Titles))
	}
	byName := map[string]*Title{}
	for _, ti := range res.Titles {
		byName[ti.Name] = ti
	}
	lb := byName["Lantern Bay"]
	if lb == nil || lb.Kind != Show || lb.IDs.TMDB != "79" || len(lb.Videos) != 6 || len(lb.Extras) != 1 {
		t.Fatalf("Lantern Bay: %+v", lb)
	}
	type ep struct {
		season   int
		episodes []int
	}
	var got []ep
	for _, v := range lb.Videos {
		got = append(got, ep{v.Season, v.Episodes})
	}
	want := []ep{{0, []int{1}}, {1, []int{1}}, {1, []int{1}}, {1, []int{2, 3}}, {2, []int{4}}, {2, []int{5}}}
	if !slices.EqualFunc(got, want, func(a, b ep) bool { return a.season == b.season && slices.Equal(a.episodes, b.episodes) }) {
		t.Errorf("episodes = %v, want %v", got, want)
	}
	if vs := lb.Versions(); len(vs) != 5 {
		t.Errorf("Lantern Bay: %d episodes with versions, want 4", len(vs))
	}
	if subs := lb.Videos[3].Subtitles; len(subs) != 1 || subs[0].Language != "es-MX" {
		t.Errorf("subtitles: %+v", subs)
	}
	if ns := byName["Moth Hours"]; ns == nil || ns.Videos[0].Season != 2 || ns.Videos[0].Episodes[0] != 5 {
		t.Errorf("Moth Hours: %+v", ns)
	}
	if fs := byName["Flat Show"]; fs == nil || len(fs.Videos) != 2 || fs.Folder != "" {
		t.Errorf("Flat Show: %+v", fs)
	}
}

func TestMixed(t *testing.T) {
	res := analyseTitles(t, "Quiet Harbor (1995)/Quiet Harbor (1995).mkv", "Lantern Bay/Season 1/Lantern Bay S01E01.mkv")
	if res.Library != Mixed {
		t.Errorf("library = %s, want mixed", res.Library)
	}
	if res := analyseTitles(t, "readme.txt"); res.Library != Empty {
		t.Errorf("library = %s, want empty", res.Library)
	}
}

func TestParseNameExported(t *testing.T) {
	tests := []struct {
		in   string
		name string
		year int
	}{
		{"Quiet.Harbor.1998.1080p.BluRay.x264-GRP", "Quiet Harbor", 1998},
		{"Quiet Harbor (1998)", "Quiet Harbor", 1998},
		{"Northern.Line.S01E02.The.Signal.720p.WEB", "Northern Line", 0},
		{"Northern Line - 1x02 - The Signal", "Northern Line", 0},
		{"The Copper Lantern", "The Copper Lantern", 0},
		{"  ", "", 0},
	}
	for _, tt := range tests {
		name, year, _ := ParseName(tt.in)
		if name != tt.name || year != tt.year {
			t.Errorf("ParseName(%q) = %q, %d; want %q, %d", tt.in, name, year, tt.name, tt.year)
		}
	}
}

func TestEpisodeNamesAndNumbers(t *testing.T) {
	// The episode's name after its numbers, a number right after the
	// show's name in a season pack, and an absolute number before it.
	res := Analyse([]Entry{
		{Path: "Lantern.Bay.S02.1080p.WEB-GRP/Lantern.Bay.S02E03.The.Long.Night.1080p.WEB-GRP.mkv"},
		{Path: "Lantern.Bay.S02.1080p.WEB-GRP/Lantern.Bay.S02E04.1080p.WEB-GRP.mkv"},
		{Path: "Iron Lantern S19 MULTi 1080p - GRP/Iron Lantern 783 MULTi 'Name' 1080p 2017.mkv"},
		{Path: "Iron Lantern S19 MULTi 1080p - GRP/Iron Lantern 2017 MULTi 1080p.mkv"},
		{Path: "Iron Lantern S18 MULTi 1080p - GRP/0749 Iron Lantern S18E03 Multi 'Name' 1080p.mkv"},
	})
	got := map[string]string{}
	for _, title := range res.Titles {
		for _, v := range title.Videos {
			got[v.Path] = fmt.Sprintf("S%dE%v %q abs %d", v.Season, v.Episodes, v.Name, v.Absolute)
		}
	}
	for p, want := range map[string]string{
		"Lantern.Bay.S02.1080p.WEB-GRP/Lantern.Bay.S02E03.The.Long.Night.1080p.WEB-GRP.mkv":  `S2E[3] "The Long Night" abs 0`,
		"Lantern.Bay.S02.1080p.WEB-GRP/Lantern.Bay.S02E04.1080p.WEB-GRP.mkv":                 `S2E[4] "" abs 0`,
		"Iron Lantern S19 MULTi 1080p - GRP/Iron Lantern 783 MULTi 'Name' 1080p 2017.mkv":    `S19E[783] "" abs 0`,
		"Iron Lantern S19 MULTi 1080p - GRP/Iron Lantern 2017 MULTi 1080p.mkv":               `S19E[] "" abs 0`,
		"Iron Lantern S18 MULTi 1080p - GRP/0749 Iron Lantern S18E03 Multi 'Name' 1080p.mkv": `S18E[3] "" abs 749`,
	} {
		if got[p] != want {
			t.Errorf("%s: got %s, want %s", p, got[p], want)
		}
	}
}
