package inside

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestParse(t *testing.T) {
	data := []byte(`{
		"format": {"duration": "5432.5", "tags": {"title": "Quiet.Harbor.1998.1080p.BluRay-GRP", "TMDB": "movie/4821", "IMDB": "tt0004821"}},
		"streams": [
			{"codec_type": "video", "tags": {"language": "eng"}},
			{"codec_type": "audio", "tags": {"language": "eng"}, "disposition": {"default": 0}},
			{"codec_type": "audio", "tags": {"language": "fre"}, "disposition": {"default": 1}}
		],
		"chapters": [{}, {}, {}]
	}`)
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	want := File{Duration: 5432.5, TMDB: 4821, TMDBKind: "movie", IMDB: "tt0004821",
		Title: "Quiet.Harbor.1998.1080p.BluRay-GRP", Language: "fr", Chapters: 3}
	if f != want {
		t.Errorf("got %+v\nwant %+v", f, want)
	}
}

func TestParseBare(t *testing.T) {
	// Nothing said: every field empty; the first audio track when none is
	// the default.
	f, err := Parse([]byte(`{"format": {}, "streams": [{"codec_type": "audio", "tags": {"language": "jpn"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if f != (File{Language: "ja"}) {
		t.Errorf("got %+v", f)
	}
}

func TestTMDBTag(t *testing.T) {
	for _, tt := range []struct {
		in   string
		kind string
		id   int
	}{{"tv/123", "tv", 123}, {"4821", "", 4821}, {"Movie/79", "movie", 79}, {"collection/9", "", 0}, {"", "", 0}} {
		kind, id := tmdbID(tt.in)
		if kind != tt.kind || id != tt.id {
			t.Errorf("tmdbID(%q) = %q, %d; want %q, %d", tt.in, kind, id, tt.kind, tt.id)
		}
	}
}

func TestLanguage(t *testing.T) {
	for in, want := range map[string]string{"fre": "fr", "FRA": "fr", "en": "en", "und": "", "": "", "xyz": ""} {
		if got := Language(in); got != want {
			t.Errorf("Language(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadProbes(t *testing.T) {
	name := filepath.Join(t.TempDir(), "probes.jsonl.gz")
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	z := gzip.NewWriter(f)
	z.Write([]byte(`{"path":"Films/A/A.mkv","size":1,"probe":{"format":{"duration":"60"}}}` + "\n" +
		`{"path":"Films/B/B.mkv","probe":{"format":{"tags":{"title":"B"}}}}` + "\n"))
	z.Close()
	f.Close()
	probes, err := ReadProbes(name)
	if err != nil {
		t.Fatal(err)
	}
	if len(probes) != 2 || probes["Films/A/A.mkv"].Duration != 60 || probes["Films/B/B.mkv"].Title != "B" {
		t.Errorf("got %+v", probes)
	}
}
