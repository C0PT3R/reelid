// Command analyse says what the files of a library are, from a listing of
// its paths. The listing is a TSV of type, size and path, one file per
// line, as find writes it:
//
//	find /media/films -printf "%y\t%s\t%p\n" > films.tsv
//	analyse -root /media/films films.tsv
//
// It prints the analysis as JSON, or a summary with -summary. With
// -tmdb-token and -tmdb-cache, each title is also matched to TMDB.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/overfin/analyser/analyse"
	"github.com/overfin/analyser/match"
	"github.com/overfin/analyser/tmdb"
)

func main() {
	root := flag.String("root", "", "the library's folder (default: the folder all paths share)")
	summary := flag.Bool("summary", false, "print counts instead of the JSON")
	tokenFile := flag.String("tmdb-token", os.Getenv("TMDB_TOKEN_FILE"), "match the titles to TMDB, with the API read access token in this file (default $TMDB_TOKEN_FILE; none: no matching)")
	cache := flag.String("tmdb-cache", "", "the folder of TMDB's cached answers (needed for matching)")
	rate := flag.Float64("rate", 10, "at most this many requests to TMDB a second")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: analyse [-root folder] [-summary] [listing.tsv]")
		flag.PrintDefaults()
	}
	flag.Parse()

	in := io.Reader(os.Stdin)
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		in = f
	}
	entries, err := ReadListing(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	entries = relative(entries, *root)
	res := analyse.Analyse(entries)
	var matches []matched
	if *tokenFile != "" {
		if matches, err = matchTitles(res, *tokenFile, *cache, *rate); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	if *summary {
		printSummary(res)
		return
	}
	out := json.NewEncoder(os.Stdout)
	out.SetIndent("", "  ")
	var doc any = res
	if matches != nil {
		doc = struct {
			analyse.Result
			Matches []matched `json:"matches"`
		}{res, matches}
	}
	if err := out.Encode(doc); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ReadListing reads a TSV of "type\tsize\tpath" lines and keeps the files
// (type "f"); folders and links are left out.
func ReadListing(r io.Reader) ([]analyse.Entry, error) {
	var entries []analyse.Entry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for line := 1; sc.Scan(); line++ {
		fields := strings.SplitN(sc.Text(), "\t", 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("line %d: want type, size and path separated by tabs", line)
		}
		if fields[0] != "f" {
			continue
		}
		size, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: size: %w", line, err)
		}
		entries = append(entries, analyse.Entry{Path: fields[2], Size: size})
	}
	return entries, sc.Err()
}

// relative makes the paths relative to root, or to the folder they all
// share when root is "".
func relative(entries []analyse.Entry, root string) []analyse.Entry {
	if root == "" {
		root = sharedFolder(entries)
	}
	root = strings.TrimSuffix(path.Clean(root), "/") + "/"
	if root == "./" {
		return entries
	}
	var out []analyse.Entry
	for _, e := range entries {
		if p, ok := strings.CutPrefix(e.Path, root); ok {
			e.Path = p
			out = append(out, e)
		}
	}
	return out
}

// sharedFolder is the longest folder every entry is in.
func sharedFolder(entries []analyse.Entry) string {
	if len(entries) == 0 {
		return ""
	}
	shared := strings.Split(path.Dir(entries[0].Path), "/")
	for _, e := range entries[1:] {
		parts := strings.Split(path.Dir(e.Path), "/")
		n := 0
		for n < len(shared) && n < len(parts) && shared[n] == parts[n] {
			n++
		}
		shared = shared[:n]
	}
	return strings.Join(shared, "/")
}

// matched is a title's TMDB match, by the title's folder (or first file).
type matched struct {
	Title string `json:"title"`
	match.Result
}

// matchTitles matches each title found to TMDB, through a cache.
func matchTitles(res analyse.Result, tokenFile, cache string, rate float64) ([]matched, error) {
	if cache == "" {
		return nil, fmt.Errorf("-tmdb-cache: say where TMDB's answers are kept")
	}
	token, err := tmdb.ReadToken(tokenFile)
	if err != nil {
		return nil, err
	}
	client := tmdb.NewClient(token, cache, rate)
	out := make([]matched, 0, len(res.Titles))
	for _, t := range res.Titles {
		r, err := match.Match(context.Background(), client, match.QueryOf(t))
		if err != nil {
			return nil, err
		}
		where := t.Folder
		if where == "" && len(t.Videos) > 0 {
			where = t.Videos[0].Path
		}
		out = append(out, matched{Title: where, Result: r})
	}
	fmt.Fprintf(os.Stderr, "TMDB: %d requests made, %d answers from the cache\n", client.Requests(), client.Hits())
	return out, nil
}

func printSummary(res analyse.Result) {
	films, series, episodes, extras, subtitles, versions := 0, 0, 0, 0, 0, 0
	for _, t := range res.Titles {
		if t.Kind == analyse.Show {
			series++
			episodes += len(t.Videos)
		} else {
			films++
		}
		extras += len(t.Extras)
		for _, v := range t.Videos {
			subtitles += len(v.Subtitles)
		}
		for _, group := range t.Versions() {
			if len(group) > 1 {
				versions += len(group) - 1
			}
		}
	}
	fmt.Printf("library:    %s\n", res.Library)
	fmt.Printf("films:      %d\n", films)
	fmt.Printf("series:     %d (%d episode files)\n", series, episodes)
	fmt.Printf("versions:   %d extra versions\n", versions)
	fmt.Printf("extras:     %d\n", extras)
	fmt.Printf("subtitles:  %d attached, %d unattached files\n", subtitles, len(res.Unattached))
	fmt.Printf("ignored:    %d\n", len(res.Ignored))
}
