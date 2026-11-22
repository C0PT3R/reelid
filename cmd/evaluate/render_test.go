package main

import (
	"strings"
	"testing"
)

// Made-up titles: the key's real ones never appear in tests.
func synthetic() (*file, *file, *file) {
	film := &title{index: 0, lib: "Films", name: "Quiet Harbor", year: 1995, idKind: "tmdbid", id: "4821", quality: "Bluray-2160p"}
	show := &title{index: 1, lib: "Series", name: "Lantern Bay", year: 2018, idKind: "tmdbid", id: "79", quality: "WEBDL-1080p"}
	v := &file{index: 0, title: film, quality: "Bluray-2160p", ext: "mkv"}
	sub := &file{index: 1, title: film, quality: "Bluray-2160p", side: ".es-MX", ext: "srt", video: v}
	ep := &file{index: 2, title: show, show: "Lantern Bay", season: 1, episodes: []int{2, 3}, epName: "Storm", quality: "WEBDL-1080p", ext: "mkv"}
	film.videos, show.videos = []*file{v}, []*file{ep}
	return v, sub, ep
}

func TestRenderClean(t *testing.T) {
	v, sub, ep := synthetic()
	for f, want := range map[*file]string{
		v:   "Quiet Harbor (1995) [tmdbid-4821]/Quiet Harbor (1995) Bluray-2160p.mkv",
		sub: "Quiet Harbor (1995) [tmdbid-4821]/Quiet Harbor (1995) Bluray-2160p.es-MX.srt",
		ep:  "Lantern Bay (2018) [tmdbid-79]/Season 1/Lantern Bay - S01E02-E03 - Storm WEBDL-1080p.mkv",
	} {
		if got := render(f, style{}, plain); got != want {
			t.Errorf("render = %q, want %q", got, want)
		}
	}
}

// Patterns must never carry a title's or an episode's name, nor an ID.
func TestPatternsAreAnonymous(t *testing.T) {
	v, sub, ep := synthetic()
	for _, s := range damages {
		for _, f := range []*file{v, sub, ep} {
			p := strings.ToLower(render(f, s, pattern))
			for _, secret := range []string{"quiet", "harbor", "lantern", "storm", "4821", "1995", "2018"} {
				if strings.Contains(p, secret) {
					t.Errorf("%s: pattern %q says %q", s.name, p, secret)
				}
			}
		}
	}
}

func TestDamages(t *testing.T) {
	v, _, ep := synthetic()
	for _, tt := range []struct {
		s    style
		f    *file
		want string
	}{
		{style{name: "x", noIDs: true, noYear: true}, v, "Quiet Harbor/Quiet Harbor Bluray-2160p.mkv"},
		{style{name: "x", flat: true}, ep, "Lantern Bay - S01E02-E03 - Storm WEBDL-1080p.mkv"},
		{style{name: "x", noSeason: true}, ep, "Lantern Bay (2018) [tmdbid-79]/Lantern Bay - S01E02-E03 - Storm WEBDL-1080p.mkv"},
	} {
		if got := render(tt.f, tt.s, plain); got != tt.want {
			t.Errorf("render = %q, want %q", got, tt.want)
		}
	}
}
