# Evaluating reelid

How reelid is measured. Back to the [README](../README.md).

## Damaged paths (stage 1)

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
go run ./cmd/evaluate -key ~/reelid-private/library.tsv
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

## Matching (stage 2)

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
    -key ~/reelid-private/library.tsv -tmdb-cache ~/reelid-private/tmdb-cache
go run ./cmd/evaluate -stage 2 -offline -only translated \
    -key ~/reelid-private/library.tsv -tmdb-cache ~/reelid-private/tmdb-cache  # from the cache
```

## What the files say (stage 3)

`-stage 3` reads the probes (`-probes`, JSON lines of `path` and `probe`,
gzipped or not, read a line at a time) and runs every stage 2 run with
the files intact, then the main ones with the inside damaged:
`tags-stripped` (title, show and ID tags gone), `wrong-title` (every
file of a title carries the title tags of another title's files),
`no-durations`. Each title is matched by its paths alone and with its
files: the report puts stage 2 and stage 3 side by side per run (films,
series, the confidence's calibration), then stage 2's failure patterns
with how many stage 3 fixed, and stage 3's new failures.

```sh
go run ./cmd/evaluate -stage 3 -offline -key ~/reelid-private/library.tsv \
    -tmdb-cache ~/reelid-private/tmdb-cache -probes ~/reelid-private/probe.jsonl.gz
```

## The real-world evaluation

The damages above are made by the evaluator itself. `-real` scores the
analyser on a **downloads folder as it is** (release names and folders,
season packs, copies wrapped in other folders) against the clean library
the downloads were put in **as hardlinks**: a download and its library
file share an inode, and the library file's path, read by stage 1, is
the download's truth (its TMDB ID, season and episodes). Downloads
without a library twin are counted apart, not scored.

The library's numbers are its manager's (TVDB's order), mostly TMDB's
default but not always; for the episodes **in TMDB's order** the truth
is found by the library file's episode name: kept where TMDB's episode
of that number has that name, moved where TMDB has that name once
elsewhere (two parts of one episode become that one), unconfirmed where
TMDB has it nowhere, unknown where TMDB's season has no such number. A
truth is contradicted where the library file's length is far from its
episodes' (a 22-minute file named as one 11-minute segment), or where
the download's own name is TMDB's name of another episode: the library
is wrong there. The report gives the numbers as read and as placed
(`match.Places`, in the series stage 3 found, when right), by kind of
file and of truth; unknown and contradicted truths are counted apart.

`-real-walk` makes the listing: one walk of `Films`, `Series` and
`downloads` below `-real-root`, stat only (size, device, inode, path),
nothing read. `-real` then analyses the downloads as one library and
matches each title found (stage 2, and stage 3 with `-probes`, a
download's probe being its twin's). It prints stage 1's fields (kind,
season, episodes; single, multi-episode and season-pack files; extras),
the TMDB IDs right per video and per title with the confidence's
calibration, stage 2 beside stage 3, and the failures as patterns of
the paths' traits ("season pack folder", "bare number", "no year"...),
never a name. The listing and the `-debug` file hold real paths: keep
them private, outside the repository.

```sh
go run ./cmd/evaluate -real-walk ~/reelid-private/realworld.tsv -real-root /mnt/media
go run ./cmd/evaluate -real ~/reelid-private/realworld.tsv -tmdb-token ~/.config/tmdb-token \
    -tmdb-cache ~/reelid-private/tmdb-cache
```
