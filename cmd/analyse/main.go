// Command analyse says what the files of a library are, from a listing of
// its paths. The listing is a TSV of type, size and path, one file per
// line, as find writes it:
//
//	find /media/films -printf "%y\t%s\t%p\n" > films.tsv
//	analyse -root /media/films films.tsv
//
// It prints the analysis as JSON, or a summary with -summary.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/overfin/analyser/analyse"
)

func main() {
	root := flag.String("root", "", "the library's folder (default: the folder all paths share)")
	summary := flag.Bool("summary", false, "print counts instead of the JSON")
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

	if *summary {
		printSummary(res)
		return
	}
	out := json.NewEncoder(os.Stdout)
	out.SetIndent("", "  ")
	if err := out.Encode(res); err != nil {
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
