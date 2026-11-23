// Package inside reads what is inside a video file, as ffprobe tells it
// (`ffprobe -print_format json -show_format -show_streams -show_chapters`),
// for what helps say which title the file is: the database IDs and the
// title its tags carry, its length, and its main audio track's language.
//
// None of it is needed: every field is empty when the file doesn't say
// it, and matching works without any.
package inside

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// File is what one video file says of itself.
type File struct {
	// Duration is the file's length in seconds; 0 when unknown.
	Duration float64
	// TMDB is the TMDB ID its tags give, with its kind when they say it
	// ("movie", "tv"; "" when they don't); 0 for none.
	TMDB     int
	TMDBKind string
	// IMDB is the IMDb ID its tags give ("tt7654321"); "" for none.
	IMDB string
	// Title and Show are its title and show tags as written: often the
	// release's name, sometimes an episode's name.
	Title, Show string
	// Language is its main audio track's language, in two letters
	// ("fr"); "" when unknown.
	Language string
	// Chapters is how many chapters it has.
	Chapters int
}

// probe is the part of ffprobe's JSON that is read.
type probe struct {
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		Type        string            `json:"codec_type"`
		Tags        map[string]string `json:"tags"`
		Disposition struct {
			Default int `json:"default"`
		} `json:"disposition"`
	} `json:"streams"`
	Chapters []json.RawMessage `json:"chapters"`
}

// Parse reads ffprobe's JSON of one file.
func Parse(data []byte) (File, error) {
	var p probe
	if err := json.Unmarshal(data, &p); err != nil {
		return File{}, err
	}
	var f File
	f.Duration, _ = strconv.ParseFloat(p.Format.Duration, 64)
	f.Chapters = len(p.Chapters)
	for k, v := range p.Format.Tags {
		v = strings.TrimSpace(v)
		switch strings.ToLower(k) {
		case "title":
			f.Title = v
		case "show":
			f.Show = v
		case "tmdb", "tmdb_id", "tmdbid":
			f.TMDBKind, f.TMDB = tmdbID(v)
		case "imdb", "imdb_id", "imdbid":
			if m := imdbID.FindString(strings.ToLower(v)); m != "" {
				f.IMDB = m
			}
		}
	}

	// The main audio track: the default one, or else the first.
	main := -1
	for i, s := range p.Streams {
		if s.Type != "audio" {
			continue
		}
		if main < 0 {
			main = i
		}
		if s.Disposition.Default == 1 {
			main = i
			break
		}
	}
	if main >= 0 {
		f.Language = Language(p.Streams[main].Tags["language"])
	}
	return f, nil
}

// imdbID is an IMDb ID.
var imdbID = regexp.MustCompile(`tt\d{5,10}`)

// tmdbTag is a TMDB tag's value: "movie/4821", "tv/4821", or "4821".
var tmdbTag = regexp.MustCompile(`^(?:(movie|tv)/)?(\d{1,9})$`)

// tmdbID reads a TMDB tag's value.
func tmdbID(v string) (string, int) {
	m := tmdbTag.FindStringSubmatch(strings.ToLower(v))
	if m == nil {
		return "", 0
	}
	id, _ := strconv.Atoi(m[2])
	return m[1], id
}

// Language gives the two-letter code (ISO 639-1) of a track's language
// as files write it, mostly in three letters (ISO 639-2: "fre", "fra",
// "eng"); "" when unknown or undetermined.
func Language(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if len(code) == 2 {
		return code
	}
	return threeLetters[code]
}

// threeLetters are the commonest languages' three-letter codes, both
// forms where there are two (bibliographic "fre", terminological "fra").
var threeLetters = map[string]string{
	"eng": "en", "fre": "fr", "fra": "fr", "ger": "de", "deu": "de", "spa": "es", "ita": "it",
	"por": "pt", "jpn": "ja", "kor": "ko", "chi": "zh", "zho": "zh", "rus": "ru", "dut": "nl",
	"nld": "nl", "swe": "sv", "nor": "no", "nob": "nb", "dan": "da", "fin": "fi", "pol": "pl",
	"tur": "tr", "ara": "ar", "hin": "hi", "ind": "id", "tha": "th", "vie": "vi", "heb": "he",
	"gre": "el", "ell": "el", "cze": "cs", "ces": "cs", "hun": "hu", "rum": "ro", "ron": "ro",
	"ukr": "uk", "per": "fa", "fas": "fa", "ice": "is", "isl": "is", "cat": "ca", "tam": "ta",
	"tel": "te", "mal": "ml", "may": "ms", "msa": "ms", "fil": "tl", "tgl": "tl",
}

// probeLine is one line of a probes' file: a video's path and ffprobe's
// JSON of it.
type probeLine struct {
	Path  string          `json:"path"`
	Probe json.RawMessage `json:"probe"`
}

// ReadProbes reads a probes' file: one JSON line per video,
// {"path": …, "probe": ffprobe's JSON}, gzipped when its name ends in
// .gz. It reads a line at a time and keeps only what each file says, by
// path, not ffprobe's whole answer.
func ReadProbes(name string) (map[string]File, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(name, ".gz") {
		z, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer z.Close()
		r = z
	}
	out := map[string]File{}
	dec := json.NewDecoder(r)
	for {
		var line probeLine
		if err := dec.Decode(&line); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		file, err := Parse(line.Probe)
		if err != nil {
			return nil, fmt.Errorf("%s: a probe: %w", name, err)
		}
		out[line.Path] = file
	}
	return out, nil
}
