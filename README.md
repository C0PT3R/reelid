# reelid

reelid tells you what the video files of a media library are: which film
or which episode each one is, with its TMDB ID.

It reads the paths first (folder and file names), then, if you give it
them, what the files say about themselves (length, tags). It's a Go
library with a small command-line tool, MIT-licensed, and independent of
any media server.

## What it finds

For each video:

- **film or episode**, and the **title** it belongs to, with its **TMDB ID**
  and how sure the match is: `sure`, or `to check` with the other
  candidates and why (closed codes such as `close`, `year-off`,
  `runtime-off`, for you to word in your own language);
- for an episode, its **season and episode numbers**, multi-episode files
  included, placed in **TMDB's order** even when the file is numbered
  another way (absolute numbers, a DVD order, a season split differently);
- **versions** of one title (`2160p`, `Director's Cut`), **parts** (`cd1`),
  **extras** (trailers, featurettes…) and **subtitles** with their language.

It copes with release names (`Name.2002.1080p.BluRay.x264-GRP`), season
packs, download folders, missing years, titles in another language, and
ID tags when they're there (`[tmdbid-4821]`, `{tmdb-4821}`,
`[imdbid-tt7654321]`).

## How well it does

Measured on a real downloads folder of 17,312 videos (films and series,
release names as downloaded), against the library they were filed into:

|                                         | reelid | Jellyfin 12.2's matching* |
|-----------------------------------------|-------:|--------------------------:|
| films identified                        | 98.8%  | 98.0% |
| episodes identified                     | 99.2%  | 84.9% |
| series identified                       | 98.5%  | 92.3% |
| episodes placed right (series + number) | 98.7%  | 80.7% |

Matches marked `sure` were wrong 0.15% of the time.

\* A re-creation of Jellyfin's rules run on the same files with the same
TMDB answers, not Jellyfin itself. How the measuring works:
[docs/evaluation.md](docs/evaluation.md).

## Try it

You need Go and, to match titles, a TMDB API read access token in a file.

```sh
go install github.com/C0PT3R/reelid/cmd/analyse@latest

# list a library: type, size, path
find /media/films -printf "%y\t%s\t%p\n" > films.tsv

# what the paths say (no network)
analyse -root /media/films -summary films.tsv

# matched to TMDB too
analyse -root /media/films -tmdb-token ~/.config/tmdb-token \
        -tmdb-cache ~/.cache/reelid-tmdb films.tsv
```

It prints JSON: every title, its files, and its match.

## Use it from Go

```go
entries := []analyse.Entry{{Path: "Heat (1995)/Heat.1995.1080p.mkv", Size: 9_400_000_000}}
res := analyse.Analyse(entries) // paths only, no network

client := tmdb.NewClient(token, cacheDir, 10) // at most 10 requests a second
for _, t := range res.Titles {
	m, err := match.Match(ctx, client, match.QueryOf(t))
	if err == nil && m.Best != nil {
		fmt.Println(t.Name, "→ TMDB", m.Best.ID, m.Confidence)
	}
}
```

- `analyse`: what the paths say. A pure function: nothing read, nothing asked.
- `match`: the TMDB match (`Match`), what the files say (`AddInside`, from
  ffprobe's JSON), the episodes in TMDB's order (`Places`).
- `tmdb`: TMDB's API behind a disk cache and a rate limit.
- `inside`: reading ffprobe's JSON.

reelid has no TMDB key of its own. Pass yours to `tmdb.NewClient`, or
implement `tmdb.Source` (six methods: search, details, season, find by
IMDb ID, episode orders, an order's seasons) over your own TMDB client,
so your key, cache and pace toward TMDB stay in one place.

How each step decides: [docs/how-it-works.md](docs/how-it-works.md).

## Being kind to TMDB

TMDB is a free service. reelid keeps every answer in a cache on disk, so
a request is made once ever, spaces its requests (10 a second by default,
`-rate`), and waits as long as TMDB asks when it's told to slow down. The
token is only sent in the requests' header. The cache holds the names you
searched for, so keep it private.

## How it's made

reelid is designed by its maintainer, who makes the architecture, security and product decisions, and written by Claude (Anthropic's AI); Claude's commits carry a `Co-Authored-By: Claude` trailer. The code is checked by its unit tests and by the evaluator above; it hasn't had an independent security review yet.
