package match

import (
	"context"
	"fmt"
	"testing"

	"github.com/C0PT3R/reelid/analyse"
	"github.com/C0PT3R/reelid/inside"
	"github.com/C0PT3R/reelid/tmdb"
)

// episodesOf makes a season of n episodes of a runtime, named "<prefix> n".
func episodesOf(n, runtime int, prefix string) []tmdb.Episode {
	var out []tmdb.Episode
	for i := 1; i <= n; i++ {
		out = append(out, tmdb.Episode{Number: i, Runtime: runtime, Name: fmt.Sprintf("%s %d", prefix, i)})
	}
	return out
}

// seasoned is a series with the seasons given (specials included).
func seasoned(id int, name string, year int, seasons []tmdb.Season) tmdb.Details {
	d := series(id, name, year, 1)
	d.Seasons = seasons
	return d
}

// placeAll analyses the paths, places the one title's videos in series
// id, with the lengths given (in minutes, by path), and says each
// video's place as "S1E2+3 how".
func placeAll(t *testing.T, f *fake, id int, paths []string, minutes map[string]float64) map[string]string {
	t.Helper()
	var entries []analyse.Entry
	for _, p := range paths {
		entries = append(entries, analyse.Entry{Path: p, Size: 1})
	}
	res := analyse.Analyse(entries)
	if len(res.Titles) != 1 {
		t.Fatalf("%d titles analysed, want 1", len(res.Titles))
	}
	look := func(p string) (inside.File, bool) {
		m, ok := minutes[p]
		return inside.File{Duration: m * 60}, ok
	}
	places, err := Places(context.Background(), f, res.Titles[0], id, look)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for p, pl := range places {
		s := fmt.Sprintf("S%dE", pl.Season)
		for i, e := range pl.Episodes {
			if i > 0 {
				s += "+"
			}
			s += fmt.Sprint(e)
		}
		out[p] = s + " " + pl.How
	}
	return out
}

func check(t *testing.T, got map[string]string, want map[string]string) {
	t.Helper()
	for p, w := range want {
		if got[p] != w {
			t.Errorf("%s: placed %q, want %q", p, got[p], w)
		}
	}
}

func TestPlaceAsWritten(t *testing.T) {
	f := &fake{
		titles:  []tmdb.Details{seasoned(10, "Brass Kite", 2010, []tmdb.Season{{Number: 1, Episodes: 6}})},
		seasons: map[[2]int][]tmdb.Episode{{10, 1}: episodesOf(6, 30, "Chapter")},
	}
	paths := []string{"Brass.Kite.S01.1080p-GRP/Brass.Kite.S01E01.1080p-GRP.mkv", "Brass.Kite.S01.1080p-GRP/Brass.Kite.S01E02.1080p-GRP.mkv"}
	got := placeAll(t, f, 10, paths, map[string]float64{paths[0]: 31, paths[1]: 29})
	check(t, got, map[string]string{paths[0]: "S1E1 as written", paths[1]: "S1E2 as written"})
}

func TestPlaceBroadcasts(t *testing.T) {
	// Eleven-minute segments, shown two a broadcast, but the fifth alone
	// (a double-length one); the release numbers the broadcasts.
	eps := episodesOf(9, 11, "Segment")
	eps[4].Runtime = 22
	f := &fake{
		titles:  []tmdb.Details{seasoned(20, "Paper Comets", 2015, []tmdb.Season{{Number: 2, Episodes: 9}})},
		seasons: map[[2]int][]tmdb.Episode{{20, 2}: eps},
	}
	dir := "Paper.Comets.S02.1080p.WEB-GRP/"
	paths := []string{dir + "Paper.Comets.S02E01.1080p.WEB-GRP.mkv", dir + "Paper.Comets.S02E02.1080p.WEB-GRP.mkv",
		dir + "Paper.Comets.S02E03.1080p.WEB-GRP.mkv", dir + "Paper.Comets.S02E04.1080p.WEB-GRP.mkv"}
	minutes := map[string]float64{paths[0]: 22.5, paths[1]: 22, paths[2]: 21.5, paths[3]: 22}
	got := placeAll(t, f, 20, paths, minutes)
	check(t, got, map[string]string{paths[0]: "S2E1+2 by lengths", paths[1]: "S2E3+4 by lengths", paths[2]: "S2E5 by lengths", paths[3]: "S2E6+7 by lengths"})
}

func TestPlaceAbsolute(t *testing.T) {
	f := &fake{
		titles: []tmdb.Details{seasoned(30, "Iron Lantern", 2001, []tmdb.Season{{Number: 0, Episodes: 3}, {Number: 1, Episodes: 10}, {Number: 2, Episodes: 5}, {Number: 3, Episodes: 8}})},
	}
	// Numbers from the series' first episode, in a pack of a season TMDB
	// counts otherwise; no lengths to weigh them by.
	dir := "Iron Lantern S03 MULTi 1080p - GRP/"
	paths := []string{dir + "Iron Lantern 014 MULTi 'Name' 1080p.mkv", dir + "Iron Lantern 016 MULTi 'Other' 1080p.mkv"}
	got := placeAll(t, f, 30, paths, nil)
	check(t, got, map[string]string{paths[0]: "S2E4 absolute", paths[1]: "S3E1 absolute"})

	// No season at all: episode 12 is the second season's second.
	loose := []string{"Iron Lantern E12 Name 1080p WEB-DL.mkv", "Iron Lantern E3 Name 1080p WEB-DL.mkv"}
	got = placeAll(t, f, 30, loose, nil)
	check(t, got, map[string]string{loose[0]: "S2E2 absolute", loose[1]: "S1E3 absolute"})
}

func TestPlaceOtherOrder(t *testing.T) {
	// TMDB's default has two seasons; a streaming service's order has
	// three parts, its third being the default's second season's end.
	f := &fake{
		titles:  []tmdb.Details{seasoned(40, "Glass Orchard", 2019, []tmdb.Season{{Number: 1, Episodes: 5}, {Number: 2, Episodes: 6}})},
		seasons: map[[2]int][]tmdb.Episode{{40, 1}: episodesOf(5, 45, "Root"), {40, 2}: episodesOf(6, 45, "Branch")},
		orders: map[int][]tmdb.Order{40: {
			{ID: "story", Name: "Arcs", Type: tmdb.StoryArc},
			{ID: "parts", Name: "Parts", Type: tmdb.Digital},
		}},
		groups: map[string][]tmdb.OrderSeason{
			"story": {{Name: "Arc 3", Order: 3, Episodes: []tmdb.Placed{{Season: 1, Number: 1, Runtime: 45, Name: "Root 1"}}}},
			"parts": {
				{Name: "Part 1", Order: 1, Episodes: []tmdb.Placed{{Season: 1, Number: 1, Runtime: 45, Name: "Root 1"}}},
				{Name: "Part 2", Order: 2},
				{Name: "Part 3", Order: 3, Episodes: []tmdb.Placed{
					{Season: 2, Number: 4, Runtime: 45, Name: "Branch 4"},
					{Season: 2, Number: 5, Runtime: 45, Name: "Branch 5"},
					{Season: 2, Number: 6, Runtime: 45, Name: "Branch 6"},
				}},
			},
		},
	}
	dir := "Glass.Orchard.S03.1080p.WEB-GRP/"
	paths := []string{dir + "Glass.Orchard.S03E01.Branch.4.1080p.WEB-GRP.mkv", dir + "Glass.Orchard.S03E02.Branch.5.1080p.WEB-GRP.mkv"}
	got := placeAll(t, f, 40, paths, map[string]float64{paths[0]: 44, paths[1]: 46})
	check(t, got, map[string]string{paths[0]: "S2E4 order", paths[1]: "S2E5 order"})
}

func TestPlaceUnplaced(t *testing.T) {
	// A season TMDB doesn't have, and no order that has it: as written.
	f := &fake{titles: []tmdb.Details{seasoned(50, "Salt Meadow", 2005, []tmdb.Season{{Number: 1, Episodes: 5}})}}
	paths := []string{"Salt.Meadow.S04.720p-GRP/Salt.Meadow.S04E01.720p-GRP.mkv"}
	got := placeAll(t, f, 50, paths, nil)
	check(t, got, map[string]string{paths[0]: "S4E1 unplaced"})
}

func TestSameEpisodeName(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"The Long Night", "The long night", true},
		{"Mac and Charlie - White Trash", "Mac and Charlie: White Trash", true},
		{"Chapter 1", "Chapter 2", false},
		{"Lords of the Ring", "Lords of the Ring!", true},
		{"Pilot", "The Pilot", true},
		{"Pilot", "Pilot Light Returns", false},
		{"A Very Long Name Indeed", "Part 1 - A Very Long Name Indeed", true},
		{"", "Anything", false},
		{"Harbor Lights (2)", "Harbor Lights", true},
		{"Harbor Lights, Part 1", "Harbor Lights (1)", true},
		{"Part 1", "Part 2", false},
		{"Chapter 1", "Chapter 2", false},
	} {
		if got := SameEpisodeName(c.a, c.b); got != c.want {
			t.Errorf("SameEpisodeName(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestPlaceTwoParts(t *testing.T) {
	// TMDB has one double episode (the third) where the release has two
	// parts, so that its numbers run one ahead after it; the fifth file is
	// missing.
	eps := episodesOf(7, 22, "Night")
	eps[2].Runtime = 44
	f := &fake{
		titles:  []tmdb.Details{seasoned(60, "Copper Moth", 2008, []tmdb.Season{{Number: 3, Episodes: 7}})},
		seasons: map[[2]int][]tmdb.Episode{{60, 3}: eps},
	}
	dir := "Copper.Moth.S03.1080p.BluRay-GRP/"
	var paths []string
	minutes := map[string]float64{}
	for _, n := range []int{1, 2, 3, 4, 6, 7, 8} {
		p := fmt.Sprintf("%sCopper.Moth.S03E%02d.1080p.BluRay-GRP.mkv", dir, n)
		paths = append(paths, p)
		minutes[p] = 22
	}
	got := placeAll(t, f, 60, paths, minutes)
	check(t, got, map[string]string{
		paths[0]: "S3E1 by lengths", paths[1]: "S3E2 by lengths", paths[2]: "S3E3 by lengths", paths[3]: "S3E3 by lengths",
		paths[4]: "S3E5 by lengths", paths[5]: "S3E6 by lengths", paths[6]: "S3E7 by lengths",
	})
}
