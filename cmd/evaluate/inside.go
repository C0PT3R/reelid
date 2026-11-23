package main

import (
	"cmp"
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/C0PT3R/reelid/inside"
	"github.com/C0PT3R/reelid/match"
	"github.com/C0PT3R/reelid/tmdb"
)

// Stage 3: matching with what the files say of themselves (their tags,
// lengths and audio languages, from ffprobe), beside what the paths say.
// Each run damages the paths as a stage 2 run does, and what is inside
// the files in named ways too; every title is matched twice, by its
// paths alone (stage 2) and with its files (stage 3), and the two are
// scored side by side.

// insideDamage is a damage to what the files say.
type insideDamage struct {
	name        string
	noTags      bool // the title, show and ID tags stripped
	wrongTags   bool // every file's title and show tags taken from another title's files
	noDurations bool // the lengths unknown
}

// stage3Run is a stage 3 run: a stage 2 run's path damages, and an
// inside damage.
type stage3Run struct {
	paths  string // the name of one of matchRuns
	inside insideDamage
}

var (
	intact      = insideDamage{name: "intact"}
	tagsGone    = insideDamage{name: "tags-stripped", noTags: true}
	wrongTitle  = insideDamage{name: "wrong-title", wrongTags: true}
	noDurations = insideDamage{name: "no-durations", noDurations: true}
)

// stage3Runs are every stage 2 run with the files intact, then the main
// ones with each inside damage.
func stage3Runs() []stage3Run {
	var out []stage3Run
	for _, s := range matchRuns {
		out = append(out, stage3Run{s.name, intact})
	}
	for _, p := range []string{"match:ids-stripped", "match:no-ids+no-year", "match:translated", "match:translated+no-year"} {
		for _, d := range []insideDamage{tagsGone, wrongTitle, noDurations} {
			out = append(out, stage3Run{p, d})
		}
	}
	return out
}

// keyPath is a key file's own path ("Films/…"); another version's is
// the video it copies (the same film).
func keyPath(f *file) string {
	if f.original != nil {
		f = f.original
	}
	return f.title.lib + "/" + render(f, style{}, plain)
}

// looker gives what a key file says, damaged as d says. A title's wrong
// tags are all from one other title (its files in turn), as when a whole
// title was tagged wrong.
func looker(probes map[string]inside.File, titles []*title, d insideDamage) func(*file) (inside.File, bool) {
	// The titles whose files have a title or show tag, to take wrong
	// tags from.
	var donors []*title
	for _, t := range titles {
		if slices.ContainsFunc(t.videos, func(f *file) bool {
			p := probes[keyPath(f)]
			return p.Title != "" || p.Show != ""
		}) {
			donors = append(donors, t)
		}
	}
	donorOf := func(t *title) *title {
		if len(donors) < 2 {
			return nil
		}
		h := fnv.New32a()
		h.Write([]byte(strconv.Itoa(t.index) + "donor"))
		for i := int(h.Sum32() % uint32(len(donors))); ; i = (i + 1) % len(donors) {
			if donors[i] != t {
				return donors[i]
			}
		}
	}
	return func(f *file) (inside.File, bool) {
		if f.extra != "" || f.isSubtitle() {
			return inside.File{}, false
		}
		p, ok := probes[keyPath(f)]
		if !ok {
			return p, false
		}
		if d.noTags {
			p.Title, p.Show, p.TMDB, p.TMDBKind, p.IMDB = "", "", 0, "", ""
		}
		if d.wrongTags {
			p.Title, p.Show = "", ""
			if donor := donorOf(f.title); donor != nil {
				i := slices.Index(f.title.videos, f.original)
				if i < 0 {
					i = slices.Index(f.title.videos, f)
				}
				o := probes[keyPath(donor.videos[max(0, i)%len(donor.videos)])]
				p.Title, p.Show = o.Title, o.Show
			}
		}
		if d.noDurations {
			p.Duration = 0
		}
		return p, true
	}
}

// stage3 runs stage 3's runs whose names contain only ("" for all).
func stage3(ctx context.Context, c *corpus, src tmdb.Source, probeFile string, workers int, only string, top int, dbg func(string)) error {
	probes, err := inside.ReadProbes(probeFile)
	if err != nil {
		return err
	}
	known := 0
	for _, f := range c.files {
		if _, ok := probes[keyPath(f)]; ok && !f.isSubtitle() {
			known++
		}
	}
	fmt.Fprintf(os.Stderr, "stage 3: %d probes read, %d of the key's videos among them\n", len(probes), known)

	m := &matcher{src: src, workers: workers, matches: map[string]match.Result{}}
	titles := matchable(c)
	if err := m.loadTruth(ctx, titles); err != nil {
		return err
	}
	m.translate(titles)
	styles := map[string]style{}
	for _, s := range matchRuns {
		styles[s.name] = s
	}
	var pairs [][2]*matchRun
	for _, run := range stage3Runs() {
		name := run.paths + " / " + run.inside.name
		if only != "" && !strings.Contains(name, only) {
			continue
		}
		fmt.Fprintf(os.Stderr, "stage 3: %s\n", name)
		s := styles[run.paths]
		r2, r3, err := m.runMatch(ctx, c, s, looker(probes, c.titles, run.inside))
		if err != nil {
			return err
		}
		r2.name, r3.name = name, name
		pairs = append(pairs, [2]*matchRun{r2, r3})
		if dbg != nil {
			for _, line := range r3.debug {
				dbg("stage3\t" + name + "\t" + line)
			}
		}
	}
	reportStage3(pairs, top)
	return nil
}

// reportStage3 prints stage 2 and stage 3 side by side for each run,
// then how the stage 2 failures' patterns fared in stage 3.
func reportStage3(pairs [][2]*matchRun, top int) {
	fmt.Printf("%-46s %15s %15s %13s %13s %13s %5s\n", "run (paths / inside)", "films 2→3", "series 2→3", "sure wrong", "check right", "listed", "none")
	for _, p := range pairs {
		a, b := p[0], p[1]
		fmt.Printf("%-46s %6.2f→%6.2f%% %6.2f→%6.2f%% %4.1f→%4.1f%% %5.1f→%5.1f%% %5.1f→%5.1f%% %2d→%-2d\n", a.name,
			pct(a.filmsOK, a.films), pct(b.filmsOK, b.films), pct(a.seriesOK, a.series), pct(b.seriesOK, b.series),
			pct(a.sureWrong, a.sure), pct(b.sureWrong, b.sure),
			pct(a.checkRight, a.check), pct(b.checkRight, b.check),
			pct(a.checkListed, a.check-a.checkRight), pct(b.checkListed, b.check-b.checkRight), a.none, b.none)
	}
	fmt.Println("\n(stage 2 → stage 3 on the same damaged paths. films, series: TMDB IDs right; sure wrong: of the")
	fmt.Println(" matches said sure, how many were wrong; check right: of those given to check, how many were right;")
	fmt.Println(" listed: of the wrong ones to check, how many had the truth beside them; none: nothing found)")

	// Sure and to check, each stage: how many, to see the calibration.
	fmt.Printf("\n%-46s %13s %13s\n", "run", "sure 2→3", "check 2→3")
	for _, p := range pairs {
		fmt.Printf("%-46s %6d→%-6d %6d→%-6d\n", p[0].name, p[0].sure, p[1].sure, p[0].check, p[1].check)
	}

	type counted struct {
		run, pattern       string
		wrong2, fixed, new int
	}
	var all []counted
	for _, p := range pairs {
		by := map[string]*counted{}
		get := func(pattern string) *counted {
			if by[pattern] == nil {
				by[pattern] = &counted{run: p[0].name, pattern: pattern}
			}
			return by[pattern]
		}
		for t, pattern := range p[0].wrong {
			c := get(pattern)
			c.wrong2++
			if _, still := p[1].wrong[t]; !still {
				c.fixed++
			}
		}
		for t, pattern := range p[1].wrong {
			if _, was := p[0].wrong[t]; !was {
				get(pattern).new++
			}
		}
		for _, c := range by {
			all = append(all, *c)
		}
	}
	slices.SortFunc(all, func(a, b counted) int {
		return cmp.Or(b.wrong2+b.new-a.wrong2-a.new, strings.Compare(a.run, b.run), strings.Compare(a.pattern, b.pattern))
	})
	fmt.Printf("\nStage 2's wrong matches by pattern, and stage 3's (wrong in 2, fixed in 3, newly wrong in 3):\n")
	for i, c := range all {
		if i == top {
			break
		}
		fmt.Printf("%5d %5d %5d  %-46s %s\n", c.wrong2, c.fixed, c.new, c.run, c.pattern)
	}
}
