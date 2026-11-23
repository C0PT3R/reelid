package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"

	"github.com/C0PT3R/reelid/analyse"
	"github.com/C0PT3R/reelid/inside"
	"github.com/C0PT3R/reelid/match"
	"github.com/C0PT3R/reelid/tmdb"
)

// The episodes in TMDB's order. The library's numbers are its manager's
// (Sonarr's, TVDB's order), which are mostly TMDB's default order but not
// always. A download's truth in TMDB's order is found by the library
// file's episode name: kept where TMDB's episode of that number has that
// name, moved where TMDB has that name elsewhere in the series (once,
// unambiguously), and kept but unconfirmed where TMDB has it nowhere;
// unknown where TMDB's season has no episode of that number either (a
// season TMDB numbers on from the one before, S21 listing 892 to 1088,
// or one it doesn't have), counted apart. A truth
// whose file's length is far from its episodes' (a 22-minute file named
// as one 11-minute segment), or whose download's own name is one of
// TMDB's names for another episode of the series (and of no other), is
// contradicted: the library itself is wrong there (its manager put the
// file under another numbering), and it is counted apart.

// tmdbTruth is a library episode's place in TMDB's default order.
type tmdbTruth struct {
	season   int
	episodes []int
	how      string // "same number", "moved by name", "unconfirmed", "unknown", "contradicted by length" or "by name"
}

// truthInTMDBOrder finds, for each library episode, its place in TMDB's
// default order (see above). The seasons are fetched once each.
func truthInTMDBOrder(ctx context.Context, src tmdb.Source, m *matcher, truths []*realTruth, minutes func(*realTruth) float64, downloadNames func(*realTruth) []string) (map[*realTruth]*tmdbTruth, error) {
	byID := map[int][]*realTruth{}
	var ids []int
	for _, tr := range truths {
		if !tr.episode() || tr.id == 0 || tr.season < 0 || len(tr.episodes) == 0 {
			continue
		}
		if _, ok := byID[tr.id]; !ok {
			ids = append(ids, tr.id)
		}
		byID[tr.id] = append(byID[tr.id], tr)
	}
	out := map[*realTruth]*tmdbTruth{}
	var mu sync.Mutex
	err := m.each(len(ids), func(i int) error {
		id := ids[i]
		d, err := src.Details(ctx, tmdb.TV, id)
		if errors.Is(err, tmdb.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		counts := map[int]int{}
		for _, s := range d.Seasons {
			counts[s.Number] = s.Episodes
		}
		seasons := map[int][]tmdb.Episode{}
		season := func(s int) ([]tmdb.Episode, error) {
			if eps, ok := seasons[s]; ok {
				return eps, nil
			}
			var eps []tmdb.Episode
			if counts[s] > 0 {
				eps, err = src.Season(ctx, id, s)
				if err != nil && !errors.Is(err, tmdb.ErrNotFound) {
					return nil, err
				}
			}
			seasons[s] = eps
			return eps, nil
		}
		episodeAt := func(s, n int) (tmdb.Episode, error) {
			eps, err := season(s)
			if err != nil {
				return tmdb.Episode{}, err
			}
			for _, e := range eps {
				if e.Number == n {
					return e, nil
				}
			}
			return tmdb.Episode{}, nil
		}
		nameAt := func(s, n int) (string, error) {
			e, err := episodeAt(s, n)
			return e.Name, err
		}
		// everywhere finds a name in all of TMDB's seasons: its one place.
		everywhere := func(name string) (int, int, bool, error) {
			hits := 0
			var s0, n0 int
			for s := range counts {
				eps, err := season(s)
				if err != nil {
					return 0, 0, false, err
				}
				for _, e := range eps {
					if match.SameEpisodeName(name, e.Name) {
						hits++
						s0, n0 = s, e.Number
					}
				}
			}
			return s0, n0, hits == 1, nil
		}
		res := map[*realTruth]*tmdbTruth{}
		for _, tr := range byID[id] {
			names := strings.Split(tr.name, " + ")
			t := &tmdbTruth{season: tr.season, episodes: slices.Clone(tr.episodes), how: "same number"}
			if len(names) == 1 && len(tr.episodes) > 1 {
				// One name for several numbers: the parts of an episode
				// TMDB may have as one ("S06E04-E05 - Niagara"); when its
				// length is the file's, that one.
				s, e, ok, err := everywhere(names[0])
				if err != nil {
					return err
				}
				if ok {
					ep, err := episodeAt(s, e)
					if err != nil {
						return err
					}
					if mins := minutes(tr); ep.Runtime > 0 && mins > 0 && math.Abs(mins-float64(ep.Runtime)) <= max(4, 0.15*float64(ep.Runtime)) {
						t.season, t.episodes, t.how = s, []int{e}, "moved by name"
						res[tr] = t
						continue
					}
				}
			}
			if len(names) != len(tr.episodes) {
				names = nil
			}
			moved := false
			for i, n := range tr.episodes {
				if names == nil || analyse.Key(names[i]) == "" {
					t.how = "unconfirmed"
					break
				}
				at, err := nameAt(tr.season, n)
				if err != nil {
					return err
				}
				if match.SameEpisodeName(names[i], at) {
					continue
				}
				s, e, ok, err := everywhere(names[i])
				if err != nil {
					return err
				}
				if !ok || i > 0 && s != t.season {
					t.how = "unconfirmed"
					t.season, t.episodes = tr.season, slices.Clone(tr.episodes)
					break
				}
				if i == 0 {
					t.season = s
				}
				t.episodes[i] = e
				moved = true
			}
			if moved && t.how != "unconfirmed" {
				t.how = "moved by name"
				t.episodes = slices.Compact(t.episodes) // two parts of one
			}
			if t.how == "unconfirmed" {
				// No episode of that number in TMDB's season (one numbered
				// on from the one before, or a season TMDB doesn't have):
				// its place in TMDB's order is unknown.
				for _, n := range t.episodes {
					if e, err := episodeAt(t.season, n); err != nil {
						return err
					} else if e.Number == 0 {
						t.how = "unknown"
						break
					}
				}
			}
			// The file's length against its episodes'.
			if mins := minutes(tr); mins > 0 {
				sum := 0
				for _, n := range t.episodes {
					e, err := episodeAt(t.season, n)
					if err != nil {
						return err
					}
					if e.Runtime <= 0 {
						sum = 0
						break
					}
					sum += e.Runtime
				}
				if sum > 0 && math.Abs(mins-float64(sum)) > max(8, 0.3*float64(sum)) {
					t.how = "contradicted by length"
				}
			}
			// The downloads' own names of the episode.
			for _, name := range downloadNames(tr) {
				s, e, ok, err := everywhere(name)
				if err != nil {
					return err
				}
				if ok && (s != t.season || !slices.Contains(t.episodes, e)) {
					t.how = "contradicted by name"
				}
			}
			res[tr] = t
		}
		mu.Lock()
		for k, v := range res {
			out[k] = v
		}
		mu.Unlock()
		return nil
	})
	return out, err
}

// placeJob is an analysed series and the TMDB ID it was matched to.
type placeJob struct {
	t  *analyse.Title
	id int
}

// scorePlaces places the episodes of each series matched (by stage 3)
// and scores them against the truth in TMDB's order: as read from the
// paths, and as placed.
func scorePlaces(ctx context.Context, src tmdb.Source, m *matcher, jobs []placeJob, truthOf func(string) *realTruth, inOrder map[*realTruth]*tmdbTruth,
	look func(string) (inside.File, bool), isPack func(string) bool, top int, dbg func(string)) error {
	places := make([]map[string]match.Place, len(jobs))
	if err := m.each(len(jobs), func(i int) error {
		p, err := match.Places(ctx, src, jobs[i].t, jobs[i].id, look)
		places[i] = p
		return err
	}); err != nil {
		return err
	}
	read, placed := map[string]*tally{}, map[string]*tally{}
	count := func(ts map[string]*tally, k string, ok bool) {
		if ts[k] == nil {
			ts[k] = &tally{}
		}
		ts[k].n++
		if ok {
			ts[k].ok++
		}
	}
	hows := map[string]*tally{}
	truthHows := map[string]int{}
	fails := map[string]int{}
	for i, j := range jobs {
		for _, v := range j.t.Videos {
			tr := truthOf(v.Path)
			if tr == nil || !tr.episode() || tr.id == 0 {
				continue
			}
			tt := inOrder[tr]
			if tt == nil {
				continue
			}
			truthHows[tt.how]++
			if tr.id != j.id {
				continue // the series is wrong: scored with the IDs
			}
			pl := places[i][v.Path]
			okRead := v.Season == tt.season && slices.Equal(v.Episodes, tt.episodes)
			okPlaced := pl.Season == tt.season && slices.Equal(pl.Episodes, tt.episodes)
			cat := "single"
			switch {
			case len(tt.episodes) > 1:
				cat = "multi"
			case isPack(v.Path):
				cat = "pack"
			}
			keys := []string{cat, "truth " + tt.how}
			if !strings.HasPrefix(tt.how, "contradicted") && tt.how != "unknown" {
				keys = append(keys, "all")
			}
			for _, k := range keys {
				count(read, k, okRead)
				count(placed, k, okPlaced)
			}
			count(hows, pl.How, okPlaced)
			if !okPlaced {
				k := fmt.Sprintf("%s, read %s, truth %s", pl.How, rightWrong(okRead), tt.how)
				fails[k+" | "+strings.Join(pathTraits(v.Path, tr, isPack(v.Path)), ",")]++
				if dbg != nil {
					dbg(fmt.Sprintf("place\t%s\t%s\t%s\ttruth tv/%d S%d E%v (%s)\tread S%d E%v\tplaced S%d E%v", k, v.Path, tr.path, tr.id, tt.season, tt.episodes, tt.how, v.Season, v.Episodes, pl.Season, pl.Episodes))
				}
			}
		}
	}
	fmt.Printf("\nepisodes in TMDB's order (videos of series matched right; truth by the library's episode names):\n")
	fmt.Printf("  truth: ")
	for _, k := range []string{"same number", "moved by name", "unconfirmed", "unknown", "contradicted by length", "contradicted by name"} {
		fmt.Printf("%s %d; ", k, truthHows[k])
	}
	fmt.Println()
	fmt.Printf("  (all: but the truths unknown or contradicted)\n")
	for _, k := range []string{"all", "single", "pack", "multi", "truth same number", "truth moved by name", "truth unconfirmed", "truth unknown", "truth contradicted by length", "truth contradicted by name"} {
		a, b := read[k], placed[k]
		if a == nil {
			continue
		}
		fmt.Printf("  %-22s read %6.2f%% → placed %6.2f%%   (%d → %d of %d)\n", k, a.percent(), b.percent(), a.ok, b.ok, a.n)
	}
	fmt.Printf("  placed how:")
	for _, k := range []string{match.AsWritten, match.InSeason, match.Absolute, match.ByLengths, match.Ordered, match.Unplaced} {
		if t := hows[k]; t != nil {
			fmt.Printf(" %s %d/%d;", k, t.ok, t.n)
		}
	}
	fmt.Println()
	printCounts("placing failures (how, read, truth | traits of the path):", fails, top)
	return nil
}

func rightWrong(ok bool) string {
	if ok {
		return "right"
	}
	return "wrong"
}
