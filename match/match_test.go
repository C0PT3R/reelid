package match

import (
	"context"
	"testing"

	"github.com/C0PT3R/reelid/analyse"
	"github.com/C0PT3R/reelid/inside"
	"github.com/C0PT3R/reelid/tmdb"
)

// fake is a made-up TMDB: its titles, searched by their names' keys.
type fake struct {
	titles   []tmdb.Details
	seasons  map[[2]int][]tmdb.Episode // by series ID and season number
	orders   map[int][]tmdb.Order      // by series ID
	groups   map[string][]tmdb.OrderSeason
	searches int
	details  int
}

func (f *fake) Search(_ context.Context, kind tmdb.Kind, query string, year int) ([]tmdb.Found, error) {
	f.searches++
	var out []tmdb.Found
	for _, t := range f.titles {
		if t.Kind != kind || year > 0 && t.Year != year {
			continue
		}
		for _, n := range t.Names() {
			if k := analyse.Key(n); k != "" && (k == query || contains(k, query)) {
				out = append(out, t.Found)
				break
			}
		}
	}
	return out, nil
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (s[:len(sub)] == sub || contains(s[1:], sub))
}

func (f *fake) Details(_ context.Context, kind tmdb.Kind, id int) (*tmdb.Details, error) {
	f.details++
	for _, t := range f.titles {
		if t.Kind == kind && t.ID == id {
			d := t
			return &d, nil
		}
	}
	return nil, tmdb.ErrNotFound
}

func (f *fake) Season(_ context.Context, id, number int) ([]tmdb.Episode, error) {
	eps, ok := f.seasons[[2]int{id, number}]
	if !ok {
		return nil, tmdb.ErrNotFound
	}
	return eps, nil
}

func (f *fake) FindIMDb(_ context.Context, imdb string) ([]tmdb.Found, error) {
	var out []tmdb.Found
	for _, t := range f.titles {
		if t.IMDB == imdb {
			out = append(out, t.Found)
		}
	}
	return out, nil
}

func (f *fake) Orders(_ context.Context, id int) ([]tmdb.Order, error) {
	return f.orders[id], nil
}

func (f *fake) OrderSeasons(_ context.Context, order string) ([]tmdb.OrderSeason, error) {
	g, ok := f.groups[order]
	if !ok {
		return nil, tmdb.ErrNotFound
	}
	return g, nil
}

func film(id int, name string, year int, popularity float64, names ...string) tmdb.Details {
	return tmdb.Details{Found: tmdb.Found{ID: id, Kind: tmdb.Movie, Name: name, OriginalName: name, Year: year, Popularity: popularity},
		Translations: names}
}

func series(id int, name string, year int, popularity float64, seasons ...int) tmdb.Details {
	d := tmdb.Details{Found: tmdb.Found{ID: id, Kind: tmdb.TV, Name: name, OriginalName: name, Year: year, Popularity: popularity}}
	for i, n := range seasons {
		d.Seasons = append(d.Seasons, tmdb.Season{Number: i + 1, Episodes: n})
	}
	return d
}

var library = &fake{titles: []tmdb.Details{
	film(1, "Quiet Harbor", 1998, 20),
	film(2, "Quiet Harbor", 2019, 30), // a remake
	film(3, "Quiet Harbor 2", 2001, 10),
	film(4, "The Copper Lantern", 2005, 5, "La Lanterne de cuivre"),
	film(5, "Seven Bridges", 2012, 3),
	series(10, "Northern Line", 2010, 40, 10, 10, 8),
	series(11, "Northern Line", 2021, 50, 6), // a reboot
	series(12, "Lantern Keepers", 2015, 2, 12),
}}

func TestMatch(t *testing.T) {
	tests := []struct {
		name string
		q    Query
		id   int
		conf Confidence
	}{
		{"name and year", Query{Kind: analyse.Film, Name: "Quiet Harbor", Year: 1998}, 1, Sure},
		{"the remake by its year", Query{Kind: analyse.Film, Name: "Quiet Harbor", Year: 2019}, 2, Sure},
		{"same name, no year: the most popular, to check", Query{Kind: analyse.Film, Name: "Quiet Harbor"}, 2, Check},
		{"sequel number", Query{Kind: analyse.Film, Name: "Quiet Harbor 2", Year: 2001}, 3, Sure},
		{"year one off", Query{Kind: analyse.Film, Name: "Seven Bridges", Year: 2013}, 5, Sure},
		{"without its article", Query{Kind: analyse.Film, Name: "Copper Lantern", Year: 2005}, 4, Sure},
		{"a translation", Query{Kind: analyse.Film, Name: "La lanterne de cuivre", Year: 2005}, 4, Sure},
		{"punctuation and case", Query{Kind: analyse.Film, Name: "seven.bridges", Year: 2012}, 5, Sure},
		{"series with its year", Query{Kind: analyse.Show, Name: "Northern Line", Year: 2010}, 10, Sure},
		{"series by its seasons", Query{Kind: analyse.Show, Name: "Northern Line", Episodes: map[int]int{1: 10, 3: 8}}, 10, Sure},
		{"reboot by its seasons", Query{Kind: analyse.Show, Name: "Northern Line", Episodes: map[int]int{1: 6}}, 11, Check},
		{"a country after the name", Query{Kind: analyse.Show, Name: "Lantern Keepers (US)", Year: 2015}, 12, Sure},
		{"seasons numbered on, still a series", Query{Kind: analyse.Show, Name: "Lantern Keepers", Year: 2015, Episodes: map[int]int{1: 40}}, 12, Sure},
		{"kind unknown", Query{Name: "Lantern Keepers"}, 12, Sure},
		{"wrong kind from the paths", Query{Kind: analyse.Film, Name: "Lantern Keepers", Year: 2015}, 12, Sure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Match(context.Background(), library, tt.q)
			if err != nil {
				t.Fatal(err)
			}
			if res.Best == nil || res.Best.ID != tt.id || res.Confidence != tt.conf {
				t.Errorf("got %+v (%s), want %d (%s)", res.Best, res.Confidence, tt.id, tt.conf)
			}
			if res.Confidence == Sure && len(res.Others) > 0 {
				t.Errorf("sure, with others to check: %+v", res.Others)
			}
		})
	}
}

func TestNoMatch(t *testing.T) {
	for _, q := range []Query{{Name: ""}, {Name: "!!"}, {Kind: analyse.Film, Name: "Nothing Like It", Year: 2000}} {
		res, err := Match(context.Background(), library, q)
		if err != nil {
			t.Fatal(err)
		}
		if res.Confidence != None || res.Best != nil {
			t.Errorf("%+v: got %+v (%s), want none", q, res.Best, res.Confidence)
		}
	}
}

func TestCloseCandidatesToCheck(t *testing.T) {
	res, err := Match(context.Background(), library, Query{Kind: analyse.Film, Name: "Quiet Harbor"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Others) == 0 || res.Others[0].ID != 1 {
		t.Errorf("others: got %+v, want the 1998 film first", res.Others)
	}
}

func TestFewDetails(t *testing.T) {
	f := &fake{titles: library.titles}
	if _, err := Match(context.Background(), f, Query{Kind: analyse.Film, Name: "Quiet Harbor"}); err != nil {
		t.Fatal(err)
	}
	if f.details > weighed {
		t.Errorf("%d details fetched, want at most %d", f.details, weighed)
	}
	f.searches = 0
	if _, err := Match(context.Background(), f, Query{Kind: analyse.Film, Name: "Quiet Harbor", Year: 1998}); err != nil {
		t.Fatal(err)
	}
	if f.searches != 1 {
		t.Errorf("%d searches, want 1: the year found the name", f.searches)
	}
}

func TestSimilarity(t *testing.T) {
	tests := []struct {
		a, b string
		want float64
	}{
		{"quiet harbor", "quiet harbor", 1},
		{"spider man", "spiderman", 0.95},
		{"the copper lantern", "copper lantern", 0.9},
		{"part ii", "part 2", 1},
		{"quiet harbor", "quiet harbor 2", 0.6},
		{"quiet harbor", "loud harbor", 0.25},
		{"one", "two", 0},
	}
	for _, tt := range tests {
		if got := similarity(tt.a, tt.b); got != tt.want {
			t.Errorf("similarity(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSeasonsFit(t *testing.T) {
	seasons := []tmdb.Season{{Number: 0, Episodes: 2}, {Number: 1, Episodes: 10}, {Number: 2, Episodes: 8}}
	tests := []struct {
		disk map[int]int
		want int
	}{
		{map[int]int{1: 10, 2: 8}, fitScore},
		{map[int]int{0: 5, 1: 3}, fitScore},
		{map[int]int{1: 12}, overScore},
		{map[int]int{3: 1}, misfitScore},
		{map[int]int{0: 1}, 0},
	}
	for _, tt := range tests {
		if got, _ := seasonsFit(tt.disk, seasons); got != tt.want {
			t.Errorf("seasonsFit(%v) = %d, want %d", tt.disk, got, tt.want)
		}
	}
}

func TestQueryOf(t *testing.T) {
	title := &analyse.Title{Kind: analyse.Show, Name: "Northern Line", Videos: []*analyse.Video{
		{Season: 1, Episodes: []int{1}}, {Season: 1, Episodes: []int{9, 10}}, {Season: 2, Episodes: []int{3}}, {Season: -1},
	}}
	q := QueryOf(title)
	if q.Episodes[1] != 10 || q.Episodes[2] != 3 || len(q.Episodes) != 2 {
		t.Errorf("episodes: got %v", q.Episodes)
	}
}

// insideLibrary is a made-up TMDB for the files' own signals: two films
// of one name (lengths, languages and an IMDb ID apart), and two series
// of one name whose episodes' lengths differ.
func insideLibrary() *fake {
	a := film(21, "Glass Orchard", 1987, 10)
	a.Runtime, a.Language, a.IMDB = 95, "fr", "tt0000021"
	b := film(25, "Glass Orchard", 2016, 30)
	b.Runtime, b.Language = 128, "en"
	c := film(23, "Unlisted Picture", 2003, 1) // a name no search finds
	c.Name, c.OriginalName = "Something Else Entirely", "Something Else Entirely"
	s1 := series(31, "Harbor Lights", 1999, 5, 6)
	s2 := series(32, "Harbor Lights", 2022, 20, 6)
	return &fake{
		titles: []tmdb.Details{a, b, c, s1, s2},
		seasons: map[[2]int][]tmdb.Episode{
			{31, 1}: episodes(6, 50),
			{32, 1}: episodes(6, 24),
		},
	}
}

// episodes are a season's n episodes, each of runtime minutes.
func episodes(n, runtime int) []tmdb.Episode {
	var out []tmdb.Episode
	for i := 1; i <= n; i++ {
		out = append(out, tmdb.Episode{Number: i, Runtime: runtime})
	}
	return out
}

func TestMatchInside(t *testing.T) {
	short := map[Episode]float64{{1, 1}: 23 * 60, {1, 2}: 24 * 60, {1, 3}: 25 * 60}
	tests := []struct {
		name string
		q    Query
		id   int
	}{
		{"same name, no year: the length", Query{Kind: analyse.Film, Name: "Glass Orchard", Runtime: 96 * 60}, 21},
		{"same name, no year: the tag's year", Query{Kind: analyse.Film, Name: "Glass Orchard", Tagged: []Named{{"Glass Orchard", 1987}}}, 21},
		{"a name no search finds, by its tagged ID", Query{Kind: analyse.Film, Name: "Unlisted Picture", TMDB: 23, TMDBKind: tmdb.Movie}, 23},
		{"by its IMDb ID", Query{Kind: analyse.Film, Name: "Glass Orchard", IMDB: "tt0000021"}, 21},
		{"a wrong tag loses to the paths' name and year", Query{Kind: analyse.Film, Name: "Glass Orchard", Year: 2016, Tagged: []Named{{"Harbor Lights", 1999}}}, 25},
		{"series of one name, by episodes' lengths", Query{Kind: analyse.Show, Name: "Harbor Lights", Episodes: map[int]int{1: 3}, Lengths: short}, 32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Match(context.Background(), insideLibrary(), tt.q)
			if err != nil {
				t.Fatal(err)
			}
			if res.Best == nil || res.Best.ID != tt.id {
				t.Errorf("got %+v, want %d", res.Best, tt.id)
			}
		})
	}
}

func TestAddInside(t *testing.T) {
	title := &analyse.Title{Kind: analyse.Show, Name: "Harbor Lights", Videos: []*analyse.Video{
		{Path: "a", Season: 1, Episodes: []int{1}}, {Path: "b", Season: 1, Episodes: []int{2}},
		{Path: "c", Season: 1, Episodes: []int{3, 4}}, {Path: "d", Season: 1, Episodes: []int{5}},
	}}
	files := map[string]inside.File{
		"a": {Duration: 1400, Title: "Harbor.Lights.S01E01.720p.WEB-GRP", Language: "en", TMDB: 32, TMDBKind: "tv"},
		"b": {Duration: 1450, Title: "The Second One", Language: "en"},
		"c": {Duration: 2900, Title: "Harbor.Lights.S01E03E04.720p.WEB-GRP", Language: "en"},
		"d": {Duration: 1420, Title: "Harbor.Lights.S01E05.720p.WEB-GRP", Language: "fr"},
	}
	q := QueryOf(title)
	AddInside(&q, title, func(p string) (inside.File, bool) { f, ok := files[p]; return f, ok })
	if q.TMDB != 32 || q.TMDBKind != tmdb.TV {
		t.Errorf("ID: got %d %q", q.TMDB, q.TMDBKind)
	}
	if len(q.Tagged) != 1 || q.Tagged[0] != (Named{"Harbor Lights", 0}) {
		t.Errorf("tagged: got %+v, want the release name most episodes share", q.Tagged)
	}
	if len(q.Lengths) != 3 || q.Lengths[Episode{1, 2}] != 1450 {
		t.Errorf("lengths: got %v, want the three one-episode files", q.Lengths)
	}
}

func TestRuntimeFit(t *testing.T) {
	for _, tt := range []struct {
		minutes float64
		runtime int
		want    int
	}{{95, 95, runtimeScore}, {102, 95, runtimeScore}, {110, 95, 0}, {130, 95, runtimeOffScore}, {95, 0, 0}, {0, 95, 0}} {
		if got, _ := runtimeFit(tt.minutes*60, tt.runtime); got != tt.want {
			t.Errorf("runtimeFit(%v min, %d) = %d, want %d", tt.minutes, tt.runtime, got, tt.want)
		}
	}
}
