# Overfin analyser

Overfin's own file analyser: it says what the files of a media library
are. Stage 1 is **identification from paths alone**: no file is opened
and no database asked. Stage 2 **matches each title to its TMDB ID**.

For every video of a library it says:

- whether it is a **film** or an **episode**, and which **title** it belongs
  to: the title's name and year as the names write them, and the **ID tags**
  found in folder and file names (`[tmdbid-4821]`, `{tmdb-4821}`,
  `[imdbid-tt7654321]`, `[tvdbid-54321]`…);
- an episode's **season and episode numbers**, multi-episode files included
  (`S01E01-E02`, `S01E01E02`, `S01E01-02`, `1x01-02`, `1x01x02`), from the
  common notations (`S01E02`, `s1e2`, `S01.E02`, `1x02`,
  `Season 1 Episode 2`, `E02` or `Episode 2` or a bare `02 - Name` in a
  season folder) and season folders (`Season 1`, `S01`, `Saison 1`,
  `Staffel 1`, `Specials`, a release's season pack `Show.S01.1080p…`);
- which files are **versions** of one title (same title folder, another
  quality or edition: `Bluray-2160p`, `Director's Cut`), with the label that
  tells them apart, and **parts** (`cd1`, `part 2`);
- **extras**: videos in `Extras`, `Featurettes`, `Trailers`,
  `Behind The Scenes`, `Deleted Scenes`, `Interviews`… folders, or named with
  a suffix (`-trailer`, `-featurette`, `.sample`…);
- **subtitles** (`.srt`, `.ass`, `.sub`…) attached to their video, with their
  language (`en`, `es-MX`), forced and hearing-impaired flags;
- the **library's kind** (films, series, mixed), from how its files are laid
  out, never from a configured type.

## Layout

- `analyse/`: the package. `analyse.Analyse(entries)` is a pure function
  from a list of paths (relative to the library's folder) and sizes to a
  `Result`.
- `cmd/analyse/`: a small command that reads a listing and prints the
  result as JSON (or a summary with `-summary`).
- `match/`: stage 2, `match.Match(ctx, source, query)`: a title's TMDB
  ID; `tmdb/`: TMDB's API behind a cache and a rate limit.
- `cmd/evaluate/`: the evaluator, see below.

```sh
find /media/films -printf "%y\t%s\t%p\n" > films.tsv
go run ./cmd/analyse -root /media/films -summary films.tsv
```

## The evaluator

`cmd/evaluate` scores the analyser against an **answer key**: a listing
(same TSV) of a real library laid out the clean way, whose names carry the
truth: `Films/Name (Year) [tmdbid-N]/Name (Year) Quality.mkv` and
`Series/Name (Year) [tmdbid-N]/Season N/Show - S01E02 - Episode Quality.mkv`.
Files that don't follow that layout are left out and counted.

It then **damages** the paths in named ways, one at a time and combined,
analyses each damaged library and checks every field against the truth:

| damage | what it does |
|---|---|
| `ids-stripped` | ID tags removed (the truth is then name, year, season, episode) |
| `release-files`, `release-all` | file names (and folders) as releases write them: `Name.2002.1080p.BluRay.x264-GRP` |
| `scene-tags` | releases with languages, editions, a site before the name, a bracketed group |
| `separators`, `case` | dots, underscores or hyphens for spaces; lower or upper case |
| `year-removed`, `year-written` | years gone, or written `[2002]`, ` 2002`, `.2002` |
| `season-renamed`, `season-removed` | `S01`, `Saison 1`, `season1`…; or no season folders |
| `flat`, `flat-in-one-folder` | every file in the library's folder, or in one folder below it |
| `notation`, `anime-names`, `bare-episodes` | other episode notations; `[GRP] Show - 05 [1080p]`; `S01E02.mkv` |
| `nest-release`, `nest-group` | a release folder below the title's; a folder of several titles above it |
| `extras`, `versions` | extras added (folders under many names, suffixes); other versions added |
| `punctuation` | colons for ` - `, accents, apostrophes and commas gone, `and` for `&` |
| `obfuscated-films` | films' files named by a hash |
| `mixed-library` | films and series analysed as one library |

A field is only checked when the damaged path still says it (no year is
expected once years are removed). Titles a damage makes look the same (two
films of one name once years and IDs are gone) may be merged without fault.

```sh
go run ./cmd/evaluate -key ~/analyser-corpus/library.tsv
go run ./cmd/evaluate -only flat -samples 5    # see what a damage does
```

It prints the share of right checks per damage and per field (library,
kind, id, name, year, season, episodes, grouping, all fields of a video,
subtitle, subtitle language, extra, version), then the commonest failures.

**The key is private.** The report prints counts, path patterns with the
names masked (`<Title> (####)/Season #/<Title> - S##E## - <Episode> <Quality>.mkv`)
and traits of the names (`year-like`, `dash`, `5+ words`), never a title or
a path. `-debug file` writes the failures with their real paths, for
looking into them; it refuses a file inside the repository. The tests use
made-up names only.

## Stage 2: matching to TMDB

`match.Match` says which TMDB title an identified title is. It searches
TMDB (`search/movie`, `search/tv`) by the name stage 1 found, with its
year (`first_air_date_year` for series) when the paths say one, and again
without the year when the year finds nothing of that name. The results
are weighed:

- the **name**: TMDB's, the original title, and for the three best
  results their **translations and alternative titles** too (fetched for
  those three only); the same name counts most, then the name written
  together or apart, with or without its article, Roman numerals as
  digits; a name that starts the other, or shares words, counts less;
- the **year**: the same, one off (close), or further (against);
- the **kind**: when the paths said a film and nothing fits, series are
  searched too (and the other way), a little behind;
- for a **series**, whether the seasons on disk are TMDB's and no episode
  number is beyond TMDB's count (from `tv/{id}`);
- **popularity** only breaks ties.

The best is taken, **sure** when it scores well with a clear margin over
the next; otherwise **to check**, with the close candidates beside it.

TMDB is a free service, so the client (`tmdb/`) is careful with it:

- every answer (404s too) is kept in a **cache on disk**, one file per
  request, so a request is made once ever; `-offline` answers from the
  cache alone;
- requests are spaced (at most 10 a second by default, 15 in the
  evaluator, `-rate`), and a 429 pauses them all for as long as TMDB
  asks, with retries that wait longer each time.

The token is TMDB's "API read access token", read at run time from a file
(`-tmdb-token`, or `TMDB_TOKEN_FILE`); it goes nowhere but the requests'
header. The cache holds the names searched for: keep it private, outside
the repository.

```sh
export TMDB_TOKEN_FILE=~/.config/tmdb-token        # a file holding the token, mode 600
go run ./cmd/analyse -tmdb-cache ~/analyser-corpus/tmdb-cache -root /media/films films.tsv
```

### Its evaluation

`-stage 2` runs the evaluator's stage 2: every run strips the ID tags,
alone or with other damages (years removed, release names, separators,
case and punctuation, flat, a mixed library…), and the **translated**
damage names titles by one of their TMDB translations or alternative
titles (written in Latin letters, chosen from the cache). For each title
of the key tagged with its TMDB ID, it scores whether the ID found is
right (films, series), and the confidence: how many "sure" were wrong,
how many "to check" were right, and of the wrong ones, how often the
truth was listed among those to check. Then the hardest cases, as
patterns ("same name, no year", "same name, other year (remake,
reboot)", "sequel number", "name not TMDB's"…), never a title.

```sh
go run ./cmd/evaluate -stage 2 -tmdb-token ~/.config/tmdb-token \
    -tmdb-cache ~/analyser-corpus/tmdb-cache             # the default cache
go run ./cmd/evaluate -stage 2 -offline -only translated # from the cache
```

## How it's made

The Overfin analyser is designed by its maintainer, who makes the architecture, security and product decisions, and written by Claude (Anthropic's AI); Claude's commits carry a `Co-Authored-By: Claude` trailer. The code is checked by its unit tests and by the evaluator above; it hasn't had an independent security review yet.
