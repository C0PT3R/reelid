// Command evaluate scores the analyser against an answer key: a listing
// of a real library whose names carry the truth (ID tags, seasons and
// episodes). It damages the paths in named ways, one at a time and
// combined, analyses each damaged library, and says how much of the
// truth the analyser still finds: per damage, per field, and the failures
// that happen most, as anonymous patterns.
//
//	evaluate -key ~/analyser-corpus/library.tsv
//
// The key is private: the report never shows a title or a path from it,
// only counts, the shapes of paths with the names masked ("<Title>"), and
// traits of the names ("dash", "year-like"). -debug writes the failures
// with their real paths to a file, which must stay out of the repository.
package main

import (
	"bufio"
	"cmp"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// damages are the runs: the clean key, each damage alone, then some
// together.
var damages = []style{
	{name: "baseline"},
	{name: "ids-stripped", noIDs: true},
	{name: "release-files", releaseFiles: true},
	{name: "release-all", releaseFiles: true, releaseFolders: true},
	{name: "separators", separators: true},
	{name: "case", letterCase: true},
	{name: "year-removed", noYear: true},
	{name: "season-renamed", seasonRenamed: true},
	{name: "season-removed", noSeason: true},
	{name: "flat", flat: true},
	{name: "notation", notation: true},
	{name: "nest-release", nestRelease: true},
	{name: "nest-group", nestGroup: true},
	{name: "extras", extras: true},
	{name: "versions", versions: true},
	{name: "punctuation", punct: true},
	{name: "year-written", yearWritten: true},
	{name: "scene-tags", releaseFiles: true, sceneTags: true},
	{name: "obfuscated-films", obfuscated: true},
	{name: "bare-episodes", bareEpisodes: true},
	{name: "flat-in-one-folder", flat: true, oneFolder: true},
	{name: "mixed-library", mixed: true},
	{name: "anime-names", anime: true},
	{name: "no-ids+no-year", noIDs: true, noYear: true},
	{name: "no-ids+notation+season-renamed", noIDs: true, notation: true, seasonRenamed: true},
	{name: "release-all+flat", releaseFiles: true, releaseFolders: true, flat: true},
	{name: "flat+no-ids+no-year+notation", flat: true, noIDs: true, noYear: true, notation: true},
	{name: "no-ids+separators+case+punct", noIDs: true, separators: true, letterCase: true, punct: true},
	{name: "scene-tags+no-year", releaseFiles: true, sceneTags: true, noYear: true},
	{name: "scene-tags+flat", releaseFiles: true, sceneTags: true, flat: true},
	{name: "scene-tags+flat+no-year", releaseFiles: true, sceneTags: true, flat: true, noYear: true},
	{name: "flat+release+notation+case+punct", flat: true, releaseFiles: true, sceneTags: true, notation: true, letterCase: true, punct: true},
	{name: "one-folder+no-ids+notation", flat: true, oneFolder: true, noIDs: true, notation: true},
	{name: "mixed+release-all+flat", mixed: true, releaseFiles: true, releaseFolders: true, flat: true},
	{name: "everything-but-flat", noIDs: true, separators: true, letterCase: true, punct: true, seasonRenamed: true,
		notation: true, nestGroup: true, extras: true, versions: true},
}

func main() {
	key := flag.String("key", os.Getenv("HOME")+"/analyser-corpus/library.tsv", "the answer key: a TSV of type, size and path")
	only := flag.String("only", "", "run only the damages whose name contains this")
	top := flag.Int("patterns", 30, "how many of the commonest failure patterns to print")
	samples := flag.Int("samples", 0, "print this many anonymous path patterns per damage, to see what it does")
	debug := flag.String("debug", "", "write failures with their REAL paths to this file (keep it private, outside the repository)")
	flag.Parse()

	c, err := readCorpus(*key)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var dbg *bufio.Writer
	if *debug != "" {
		if err := outsideRepository(*debug); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		out, err := os.Create(*debug)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer out.Close()
		dbg = bufio.NewWriter(out)
		defer dbg.Flush()
	}

	films, series := 0, 0
	for _, t := range c.titles {
		if t.lib == "Films" {
			films++
		} else {
			series++
		}
	}
	fmt.Printf("key: %d files read (%d films, %d series), %d left out (not in the clean layout)\n\n", len(c.files), films, series, c.skipped)

	languages := map[int]string{}
	var runs []*run
	for _, s := range damages {
		if *only != "" && !strings.Contains(s.name, *only) && s.name != "baseline" {
			continue
		}
		r := &run{style: s, fields: map[string]*tally{}, failures: map[failure]int{}, traits: map[failure]map[string]int{}}
		files := append(slices.Clip(c.files), added(c, s)...)
		printSamples(s, files, *samples)
		libs := []string{"Films", "Series"}
		if s.mixed {
			libs = []string{""}
		}
		for _, lib := range libs {
			score(r, lib, files, languages, s.name == "baseline")
		}
		runs = append(runs, r)
		if dbg != nil {
			for _, line := range r.debug {
				fmt.Fprintf(dbg, "%s\t%s\n", s.name, line)
			}
		}
	}
	report(runs, *top)
}

// added are the files a damage adds: extras, other versions.
func added(c *corpus, s style) []*file {
	var out []*file
	next := len(c.files)
	add := func(f *file) {
		f.index = next
		next++
		out = append(out, f)
	}
	extras := []struct{ kind, name string }{
		{"trailer", "Official Trailer"}, {"featurette", "On Set"}, {"behind-the-scenes", "Making Of"},
		{"deleted-scene", "Deleted Scene 1"}, {"interview", "Cast Interview"}, {"extra", "Bonus Reel"},
	}
	labels := []string{" - Director's Cut", " - Extended Edition", " [Remastered]", " - Theatrical", " Bluray-1080p", " WEBDL-720p"}
	for _, t := range c.titles {
		if len(t.videos) == 0 {
			continue
		}
		tkey := strconv.Itoa(t.index)
		if s.extras && s.pick(tkey+"extras", 4) == 0 {
			n := 1 + s.pick(tkey+"extracount", 2)
			for i := range n {
				e := extras[s.pick(tkey+"extra"+strconv.Itoa(i), len(extras))]
				styles := 6
				if t.lib == "Series" {
					styles = 4
				}
				add(&file{title: t, extra: e.kind, extraName: e.name, extraStyle: s.pick(tkey+"style"+strconv.Itoa(i), styles), ext: "mkv", size: 50 << 20})
			}
		}
		if !s.versions {
			continue
		}
		if t.lib == "Films" && s.pick(tkey+"version", 4) == 0 {
			v := *t.videos[0]
			v.original = t.videos[0]
			v.version = labels[s.pick(tkey+"label", len(labels))]
			if l := strings.TrimSpace(v.version); !strings.ContainsAny(l, "-[") || strings.HasPrefix(l, "Bluray") || strings.HasPrefix(l, "WEBDL") {
				v.quality = l
			}
			add(&v)
		}
		if t.lib == "Series" {
			for _, ep := range t.videos {
				if s.pick(strconv.Itoa(ep.index)+"version", 20) != 0 {
					continue
				}
				v := *ep
				v.original = ep
				v.quality = "HDTV-720p"
				if ep.quality == v.quality {
					v.quality = "WEBDL-1080p"
				}
				add(&v)
			}
		}
	}
	return out
}

// printSamples prints the patterns of n files spread over the key, and
// of the files the damage added.
func printSamples(s style, files []*file, n int) {
	if n <= 0 {
		return
	}
	fmt.Printf("%s:\n", s.name)
	step := max(1, len(files)/n)
	for i := 0; i < len(files); i += step {
		fmt.Printf("  %s\n", render(files[i], s, pattern))
	}
	shown := 0
	for _, f := range files {
		if (f.extra != "" || f.original != nil) && shown < 3 {
			fmt.Printf("  + %s\n", render(f, s, pattern))
			shown++
		}
	}
}

// report prints the scores per damage and field, then the commonest
// failures.
func report(runs []*run, top int) {
	fmt.Printf("%-34s %7s", "damage", "videos")
	for _, f := range fieldOrder {
		fmt.Printf(" %9s", f)
	}
	fmt.Println()
	for _, r := range runs {
		fmt.Printf("%-34s %7d", r.style.name, r.files)
		for _, f := range fieldOrder {
			if t := r.fields[f]; t != nil && t.n > 0 {
				fmt.Printf(" %8.2f%%", t.percent())
			} else {
				fmt.Printf(" %9s", "-")
			}
		}
		fmt.Println()
	}
	fmt.Println("\n(percent of the checks right; \"-\": nothing to check, e.g. no year left to find)")

	type counted struct {
		damage string
		f      failure
		n      int
		traits string
	}
	var all []counted
	for _, r := range runs {
		for f, n := range r.failures {
			if f.field == fAll {
				continue
			}
			all = append(all, counted{r.style.name, f, n, commonest(r.traits[f], 2)})
		}
	}
	slices.SortFunc(all, func(a, b counted) int {
		return cmp.Or(b.n-a.n, strings.Compare(a.damage, b.damage), strings.Compare(a.f.pattern, b.f.pattern))
	})
	fmt.Printf("\nCommonest failures (names masked; the commonest traits of the titles' names):\n")
	for i, c := range all {
		if i == top {
			break
		}
		fmt.Printf("%6d  %-30s %-9s %-20s %s\n        %s\n", c.n, c.damage, c.f.field, c.f.how, c.traits, c.f.pattern)
	}
}

// commonest gives the n commonest traits with their counts.
func commonest(counts map[string]int, n int) string {
	type kv struct {
		k string
		v int
	}
	var list []kv
	for k, v := range counts {
		list = append(list, kv{k, v})
	}
	slices.SortFunc(list, func(a, b kv) int { return cmp.Or(b.v-a.v, strings.Compare(a.k, b.k)) })
	var out []string
	for i, e := range list {
		if i == n {
			out = append(out, "…")
			break
		}
		out = append(out, fmt.Sprintf("[%s]×%d", e.k, e.v))
	}
	return strings.Join(out, " ")
}

// outsideRepository refuses a debug file inside the repository the
// command runs in: real names must not land where they could be committed.
func outsideRepository(name string) error {
	abs, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			if rel, err := filepath.Rel(d, abs); err == nil && !strings.HasPrefix(rel, "..") {
				return fmt.Errorf("-debug %s: inside the repository %s; write it somewhere private", name, d)
			}
			return nil
		}
		if d == filepath.Dir(d) {
			return nil
		}
	}
}
