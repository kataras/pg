# book/

A separate Go module (`book/go.mod`) holding a long-form book about the
library, built to one self-contained HTML file and a paginated PDF by a
generator that drives headless Chrome. No pandoc, no LaTeX.

`README_EBOOK.md` is the authoritative build document and `BRAND.md` the
authoritative brand one. Read the relevant one before a non-trivial change;
this page is orientation and the traps, not a replacement.

## Layout

| Path | What it is |
| --- | --- |
| `README.md` | The preface: first unit of the spine, not a readme |
| `NN-*.md` | The 16 chapters, in reading order |
| `epilogue.md` | Closing chapter, last in the spine, unnumbered |
| `generate-ebook.go` | The generator: spine, markdown, chapters, PDF, cover |
| `template.go` | The HTML shells (book, cover, brand export page) |
| `brand.go` | The mark, the hero and the cover art, as literal SVG |
| `styles.css` | Print stylesheet and the design tokens |
| `metadata.yml` | Title, author, edition, description |
| `assets/` | Vendored Inter + Paged.js, and `diagrams/` for chapter SVGs |
| `output/`, `cover.png` | Build products, committed |

## Building

```sh
cd book
go run .                 # PDF (default) -> output/pg-book.pdf
go run . -format html    # HTML only, no browser needed
go run . -format cover   # cover.png, 1600x2560
go run . -format brand   # output/brand/
go run . -format all
go run . -chapters 3     # first 3 present spine files (debug aid)
```

Everything except `html` needs Chrome or Edge (`PG_BOOK_BROWSER` overrides
autodetection). A full PDF layout takes a few minutes.

`assets/paged.polyfill.js` and the two Inter `.woff2` files are not
committed. A maintainer adds them locally, and they are untracked rather
than ignored, so `git status` will show them. Both are optional: without
them `-format html` still works and the PDF simply comes out unpaginated and
in a fallback font. `README_EBOOK.md` says where to get each.

## Things that break silently

These fail by producing a wrong book rather than an error, so they are worth
knowing before editing rather than after.

- **A chapter's number comes from its filename prefix**, not its position.
  The spine in `generate-ebook.go` lists files that may not exist yet; the
  generator warns and skips rather than failing, so a typo'd filename
  removes a chapter from the book without stopping the build.
- **Never use `break-after: avoid`, `break-before: avoid`, or
  `overflow: hidden` on a breakable block** in `styles.css`. Each can leave
  the paginator with no way to progress, which aborts the book.
  `break-inside` is merely inert. The vendored Paged.js never reads it.
- **Diagrams need a `viewBox`** (convention `0 0 1000 H`) and must print no
  taller than 205 mm. An oversized figure is dropped into an off-sheet
  column *without failing the build*.
- **Diagrams are ASCII-only.** The embedded Inter is a Latin subset, so `·`
  or `→` becomes tofu in the PDF.
- **Prefix every diagram id with the filename stem.** All diagrams land in
  one document; an unprefixed `marker` id binds every arrowhead in the book
  to whichever file was inlined first. This one does fail the build.
- **No `<style>`, CSS class, `var()`, `filter`, gradient or `<script>` in
  any inlined SVG** (diagrams and brand art alike). An inline `<style>` is
  document-scoped and would restyle the whole book; a `filter` makes Chrome
  rasterise the subtree when printing, losing the vector text.

`README_EBOOK.md` carries a grep-based pre-commit check for the diagram
rules. Run it after adding one.

## Brand art

`brand.go` holds `gopherArt` (the mark), `gopherSmallArt` (its small-size
cut), `heroSVG` and `coverArtSVG` as literal SVG strings. They are not files
under `assets/diagrams/` and have no ids at all.

Two facts that decide what to rebuild after an edit:

- `heroSVG` embeds `gopherArt`, and the cover inlines `heroSVG`. Editing the
  mark means rerunning **both** `-format brand` and `-format cover`.
- `gopherSmallArt` is used only below `smallCutBelow` (48 px), so editing it
  affects `-format brand` alone.

`markSource` picks the cut per export size. Do not "simplify" it into always
using the full mark: below 48 px the pupil highlights, the nose and the gap
between the teeth are sub-pixel and the face collapses into a grey bar.
`BRAND.md` has the palette and the reasoning.

## Prose

Chapters are verified against the library source, not against each other.
Nothing compiles a chapter's prose, so a wrong signature ships silently:
check `api-map.md`, and the `.go` file over it if the two disagree, before
writing that an API exists. Treat this page and the chapter text as claims
to check, not as sources.

The inline "Table of Contents" sections and
"Next Chapter" footers in each chapter are web-reading aids; the book build
strips them, so keep them for GitHub readers.
