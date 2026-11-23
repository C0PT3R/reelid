# How reelid works

The details behind the [README](../README.md).

## What the paths say (stage 1)

For every video of a library it says:

- whether it is a **film** or an **episode**, and which **title** it belongs
  to: the title's name and year as the names write them, and the **ID tags**
  found in folder and file names (`[tmdbid-4821]`, `{tmdb-4821}`,
  `[imdbid-tt7654321]`, `[tvdbid-54321]`…); a series is named by its
  files when its folder names it otherwise and says nothing more (a
  downloads folder, `Show 01 MULTi` for `Show - S01E01`), and a folder
  whose episodes name several series is split;
- an episode's **season and episode numbers**, multi-episode files included
  (`S01E01-E02`, `S01E01E02`, `S01E01-02`, `S01E01-S01E02`, `1x01-02`, `1x01x02`), from the
  common notations (`S01E02`, `s1e2`, `S01.E02`, `1x02`,
  `Season 1 Episode 2`, `E02` or `Episode 2` or a bare `02 - Name` in a
  season folder) and season folders (`Season 1`, `S01`, `Saison 1`,
  `Staffel 1`, `Specials`, a release's season pack `Show.S01.1080p…`,
  `Show - Saison 2`, `Show S-01 MULTi`; a season pack in a folder of
  other titles, as downloads are, is its own title, named by the pack);
  in a pack, a number right after the show's name (`Show 783 MULTi`);
  the **episode's name** as the file writes it after the numbers, and an
  absolute number written before them (`0749 Show S18E03`);
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

## Matching to TMDB (stage 2)

`match.Match` says which TMDB title an identified title is. It searches
TMDB (`search/movie`, `search/tv`) by the name stage 1 found, with its
year (`first_air_date_year` for series) when the paths say one, and again
without the year when the year finds nothing of that name. The results
are weighed:

- the **name**: TMDB's, the original title, and for the three best
  results their **translations and alternative titles** too (fetched for
  those three only); the same name counts most, then the name written
  together or apart, with or without its article, Roman numerals as
  digits; a name that starts the other, or shares words, counts less,
  and so does a long name a letter or two off (`Earths` for `Earth`);
- the **year**: the same, one off (close), or further (against);
- the **kind**: when the paths said a film and nothing fits, series are
  searched too (and the other way), a little behind;
- for a **series**, whether the seasons on disk are TMDB's and no episode
  number is beyond TMDB's count (from `tv/{id}`);
- **popularity** only breaks ties.

When no result is named so at all, the name is searched again as it
would be without a common slip (a season's number after it, a plural's
`s`). When the best few leave the match unsure, more results are
weighed closely (up to ten, those of the year said and those of the
name first): a title named in another language, or one of several of
one name, is only known by its details.

The best is taken, **sure** when it scores well with a clear margin over
the next, and nothing says it isn't the one (a length far from TMDB's
or seasons TMDB doesn't have, beside another title of its name; a name
only near the paths'); otherwise **to check**, with the close candidates
beside it.

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



## Placing the episodes in TMDB's order

Once a series is matched, `match.Places` says which of TMDB's episodes
each of its files is, in TMDB's **default order** (season and episode
numbers, multi-episode files as several). The numbers the names write
are most often TMDB's, and they are kept when they fit: every one is in
TMDB's season, and the files' lengths and names agree with its
episodes'. When they don't fit (a number beyond the season's count, a
season TMDB doesn't have, lengths far from TMDB's), other ways are tried
on that season's files, cheapest first:

- **absolute numbers** written beside the season's (`0749 Show S18E03`),
  or numbers counted from the series' first episode (`Show 783` in a
  pack of season 19, `Show E05` with no season): through the season that
  lists that number when TMDB numbers a season on from the one before
  (`S21` listing 892 to 1088), else through the seasons' counts;
- **places in a season** TMDB numbers on from the one before (`S21E02`
  is its second episode);
- **lengths**: the files, in the order of their numbers, aligned with
  the season's episodes by their lengths, so that a file may be two or
  three of TMDB's episodes (segments shown in one broadcast, TMDB listing
  each eleven-minute cartoon) or two files one episode (a double episode
  released in two parts), with episodes missing where the numbers skip;
- **another order** (TMDB's episode groups: a DVD's, a streaming
  service's, a production order…), fetched only now: its season of that
  number, by its name ("Part 4") or its place.

A way is taken when it places every file and leaves at most half the
disagreements of the numbers as written (lengths within 4 minutes or
15%; names, counting twice, only when they are TMDB's names of the
series' episodes, so that a translated name or a release's words say
nothing); with nothing to weigh it by, only when the numbers as written
can't be TMDB's. Otherwise the numbers stay as written (`unplaced`
when they don't fit). Specials are kept as written. The season details
are fetched once ever each, like every answer; episode groups only for
seasons that don't fit.
