# Building the pg Book

The book builds into a single, self-contained HTML file, and into a
paginated PDF (cover, table of contents with page numbers, running
headers and footers, chapter openers) laid out by the vendored
[Paged.js](https://pagedjs.org) polyfill inside a headless Chromium
browser. No pandoc, no LaTeX.

## Requirements

- Go 1.27+
- Google Chrome or Microsoft Edge (any Chromium browser; override the
  autodetection with the `PG_BOOK_BROWSER` environment variable). Only
  needed for `-format pdf`, `-format cover` and `-format brand`; HTML
  output needs no browser.

## Build

```sh
cd book
go run .                 # PDF (default): output/pg-book.pdf
go run . -format html    # HTML only:     output/pg-book.html
go run . -format cover   # cover image:   cover.png (1600x2560)
go run . -format brand   # brand rasters: output/brand/
go run . -format all     # all of the above
go run . -chapters 3     # debug aid: build only the first 3 present spine files
```

The HTML embeds the stylesheet and syntax-highlighted code always, and
the Inter fonts and Paged.js when `assets/` has them (see "Vendored
assets" below), so the file is portable on its own and shows what the
PDF will contain. The brand mark, the cover hero and the cover
background art go in as literal inline SVG rather than as data URIs or
linked images, which is what keeps them vector all the way into the
PDF: the cover page carries no raster images at all.

`-format cover` screenshots the book's own cover page onto a fixed
800x1280 canvas at device scale 2. It is the same markup and stylesheet
as the PDF's first page (one `.cover` rule set, sized by `--cover-w` and
`--cover-h`), so the published cover and the printed one cannot drift
apart. Write it elsewhere with `-cover PATH`.

`-format brand` renders the mark and hero art defined in `brand.go` to
the PNG and ICO exports under `output/brand/`. Unlike iris-book's brand
kit, pg has no separate, repository-level brand directory: the art is
literal SVG in Go source, not a hand-authored file, so there is nothing
to vendor for this step either. The mark has a full cut and a small cut,
and `favicon.ico` carries both: see `BRAND.md` for which is used where,
and for the rules the art has to keep to so it stays inlinable.

## Vendored assets (you must add these yourself)

`assets/` starts with only a `.gitkeep`. Two things are not committed to
this repository and must be added by a maintainer before a full,
paginated PDF build:

| File | Get it from | Used for |
| --- | --- | --- |
| `assets/paged.polyfill.js` | [pagedjs.org](https://pagedjs.org) / the [pagedjs/pagedjs](https://github.com/pagedjs/pagedjs) releases (the browser bundle, e.g. `paged.polyfill.js`) | Pagination: page numbers, running headers, the table of contents' page-number leaders, chapter breaks |
| `assets/inter-roman-latin.woff2`, `assets/inter-italic-latin.woff2` | The [Inter](https://rsms.me/inter/) type family (Google Fonts or the project's own releases), subset to Latin if you want a smaller file | The embedded body and heading font |

Both are optional for `-format html`: the generator checks for each one,
and if it is missing, prints a warning to stderr and degrades rather
than failing.

- Without `paged.polyfill.js`, the HTML renders as one long, readable,
  unpaginated document: no page numbers, no chapter-opener breaks, no
  resolved table-of-contents page leaders. The content itself is
  complete.
- Without the Inter font files, the page falls back to the system font
  stack already declared in `styles.css` (`"Segoe UI", sans-serif`).

`-format pdf` (and the pdf half of `-format all`) is stricter: it
refuses to run, with an actionable error naming this file, when
`assets/paged.polyfill.js` is missing, because pagination is the whole
point of a PDF. It does not hard-require the fonts; a PDF built without
them just prints in a fallback font.

## Layout of this directory

| Path | Purpose |
| --- | --- |
| `README.md` | The book's preface (first unit of the spine) |
| `NN-*.md` | The 16 chapters, in reading order |
| `epilogue.md` | The closing chapter (last unit of the spine, unnumbered) |
| `metadata.yml` | Title, subtitle, author, edition, description |
| `BRAND.md` | The brand kit: the mark, its two size cuts, the palette |
| `generate-ebook.go`, `template.go`, `brand.go` | The generator (its own Go module) |
| `styles.css` | The print stylesheet (pg's elephant-blue design tokens) |
| `assets/` | Vendored Inter fonts and Paged.js (see above), plus `diagrams/` for chapter SVGs |
| `cover.png` | The published cover, built with `-format cover` (committed once built) |
| `output/` | Build products (committed, so a clone always has a readable book) |

## How it works

1. Each chapter's markdown is converted with gomarkdown; the inline
   "Table of Contents" sections and "Next Chapter" footers are
   web-reading aids and are stripped from the book build.
2. Code fences are highlighted with chroma, one block element per source
   line, so the paginator can break long listings across pages.
3. Heading anchors get a per-chapter prefix (`ch07-...`), the global
   table of contents is generated from the chapter titles and `##`
   sections, and Paged.js resolves its page numbers at layout time.
4. A chapter's printed number comes from its filename prefix
   (`04-repositories-and-crud.md` is Chapter 4), not from its position in
   the list of files the generator actually finds. Chapters are written
   concurrently by different authors, so the spine in
   `generate-ebook.go` can and normally does list files that do not
   exist yet; the generator skips each one with a warning and renders
   whatever is present, rather than failing the build.
5. For `-format pdf`, the generator drives the browser over the
   DevTools protocol (chromedp): it navigates to the HTML, waits until
   Paged.js reports the completed layout (`window.__pagedDone`), then
   prints to PDF with CSS-defined page size. Full-length layout takes a
   few minutes.

Styling caution: never use `break-after: avoid` / `break-before: avoid`,
and never `overflow: hidden` on breakable blocks. Both can leave the
paginator with no way to make progress, which aborts the book.
`break-inside` is a different case: the vendored Paged.js never reads it,
so it is inert rather than dangerous. That is why diagrams are kept
whole by the `onOverflow` handler in `template.go` instead.

## Diagrams

Chapter diagrams are hand-authored SVG files in `assets/diagrams/`,
referenced from the markdown as ordinary images:

```markdown
![One sentence stating what the diagram shows.](assets/diagrams/04-repository-crud-flow.svg)
```

On GitHub the relative path resolves to the committed file. For the book
the generator replaces the `<img>` with the file's own markup, inlined,
because an `<img>` is an isolated document that cannot see the embedded
Inter: a linked diagram would print its labels in a fallback font. The
alt text becomes the printed caption, so write it as a sentence, not as
"Diagram of ...".

Authoring rules, all of them load-bearing:

- **A `viewBox` is required**, and the convention is `0 0 1000 H`.
  Without one the inlined SVG has no intrinsic ratio and prints 150px
  tall. Keep `width`/`height` attributes too, because GitHub wants an
  intrinsic size.
- **Nothing may print taller than 205mm** (`H` under about 1205). The
  generator refuses anything taller, because Paged.js cannot place an
  oversized figure and drops it into an off-sheet column *without
  failing the build*. Split at a phase boundary instead of shrinking the
  type.
- **ASCII text only.** The embedded Inter is a Latin subset, so a `·`,
  `→` or box-drawing character becomes tofu in the PDF.
- **Presentation attributes only**: no `<style>` (an inline SVG's
  `<style>` is document-scoped and would restyle the whole book), no CSS
  classes, no `var()`, no `filter` (Chrome rasterizes filtered subtrees
  when printing, which loses the vector text), no gradients, no
  `<script>`.
- **One `<text>` per line**, never `<tspan>` + `dy`: Paged.js drops
  whitespace-only text nodes, so the two renderings would diverge.
- **Prefix every id with the diagram's filename stem.** All diagrams
  land in one document, so an unprefixed `marker` id would bind every
  arrowhead in the book to whichever file was inlined first. The
  generator fails the build on a duplicate.

A quick pre-commit check:

```sh
grep -l '<style\|filter=\|var(\|<tspan\|dominant-baseline' assets/diagrams/*.svg   # empty
grep -lP '[^\x00-\x7F]' assets/diagrams/*.svg                                      # empty
grep -h 'id="' assets/diagrams/*.svg | grep -v 'id="d[0-9a-z-]*-'                  # empty
```

The brand mark, the cover hero and the cover background art are not
files under `assets/diagrams/`: they are literal SVG constants in
`brand.go`, built from flat-colour primitives with no ids at all, so
they never touch the duplicate-id guard above.

## Style notes for chapter authors

- One `# Chapter N: Title` heading per file; `##` sections (max depth
  `###`); an inline `## Table of Contents`; a `## Summary`; and a
  `---` + `**Next Chapter**: [...](file.md)` footer.
- Go code in ` ```go ` fences, four-space indentation, lines under 80
  columns.
- Every pg symbol must be verified against the library source (the
  parent directory of `book/`) before it appears in a chapter. See
  `BOOK-CONVENTIONS.md` (under `.superpowers/sdd/` at the repository
  root) for the full authoring spec: voice, banned AI-writing tells,
  admonition-free formatting, and the exact chapter skeleton the
  generator's regexes depend on.
