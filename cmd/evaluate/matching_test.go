package main

import (
	"testing"

	"github.com/C0PT3R/reelid/analyse"
	"github.com/C0PT3R/reelid/match"
	"github.com/C0PT3R/reelid/tmdb"
)

func TestTranslate(t *testing.T) {
	q := &title{index: 1, lib: "Films", name: "Quiet Harbor", year: 1998, idKind: "tmdbid", id: "1"}
	none := &title{index: 2, lib: "Films", name: "Seven Bridges", year: 2012, idKind: "tmdbid", id: "5"}
	m := &matcher{truth: map[*title]*tmdb.Details{
		q:    {Translations: []string{"Quiet Harbor", "Тихая гавань", "Le Port: tranquille"}},
		none: {Translations: []string{"Seven bridges", "七つの橋"}},
	}}
	m.translate([]*title{q, none})
	if q.translated != "Le Port - tranquille" {
		t.Errorf("got %q, want the Latin translation, path-safe", q.translated)
	}
	if none.translated != "" {
		t.Errorf("got %q, want none: the only Latin one is its name", none.translated)
	}
}

func TestHardPattern(t *testing.T) {
	truth := &tmdb.Details{Found: tmdb.Found{ID: 1, Kind: tmdb.Movie, Name: "Quiet Harbor", Year: 1998}}
	key := &title{lib: "Films", name: "Quiet Harbor", year: 1998, idKind: "tmdbid", id: "1"}
	got := func(id int, name string, year int) match.Result {
		c := match.Candidate{ID: id, Kind: tmdb.Movie, Name: name, Year: year}
		t := match.Candidate{ID: 1, Kind: tmdb.Movie, Name: "Quiet Harbor", Year: 1998}
		return match.Result{Best: &c, Confidence: match.Check, All: []match.Candidate{c, t}}
	}
	tests := []struct {
		q    match.Query
		res  match.Result
		want string
	}{
		{match.Query{Kind: analyse.Film, Name: "Quiet Harbor"}, got(2, "Quiet Harbor", 2019), "same name, no year"},
		{match.Query{Kind: analyse.Film, Name: "Quiet Harbor", Year: 1998}, got(2, "Quiet Harbor", 2019), "same name, other year (remake, reboot)"},
		{match.Query{Kind: analyse.Film, Name: "Quiet Harbor", Year: 1998}, got(3, "Quiet Harbor 2", 2001), "sequel number"},
		{match.Query{Kind: analyse.Show, Name: "Quiet Harbor"}, got(2, "Quiet Harbor", 2019), "kind wrong from the paths"},
		{match.Query{Kind: analyse.Film, Name: ""}, match.Result{}, "no name from the paths"},
	}
	for _, tt := range tests {
		if p := hardPattern(key, tt.q, tt.res, truth); p != tt.want {
			t.Errorf("%+v: got %q, want %q", tt.q, p, tt.want)
		}
	}
}
