# pg brand kit

One mark, cut two ways, plus a hero. Everything is defined in `brand.go` as
literal SVG and rendered from there. There are no hand-authored `.svg` files
to keep in sync, and no repository-level `brand/` directory.

**The mark** is a database cylinder that is also a gopher. The rim ellipse,
the straight sides and the swelling bottom are the datastore glyph every
developer already reads; the ears, the eyes, the two front teeth and the
amber feet make it the Go mascot. One silhouette carries both readings at
once, which is what keeps it a mark rather than an illustration with a badge
glued on.

The gopher is an original drawing in this library's palette, not a trace of
Renée French's Go gopher, and it is deliberately not an elephant. The book
ships inside the library's own repository rather than on a standalone site,
so the mark must not read as official PostgreSQL or Go artwork.

**The hero** is that same mark, scaled into a scene: two connection lines
ending in dots on one side, three short streaks on the other. The lines are
the pooled client connections a Go program keeps open against the database;
the streaks are the throughput coming back out. The streaks are also the
only place the brand says *fast*. There is room for it at 840 px and none at
16, so the hero carries the signal the mark cannot.

## The two cuts

| Cut | Source | Used at |
| --- | --- | --- |
| Full | `gopherArt` | 48 px and above |
| Small | `gopherSmallArt` | below `smallCutBelow` (48 px) |

The boundary is not a preference. Below 48 px the pupil highlights, the nose
and the gap between the two teeth are all sub-pixel and smear into one grey
mass. The small cut drops those three, thickens the teeth and the ears, and
flattens the rim so the eyes clear its front lip.

The decisive difference is the eyes: the small cut's are wider and their
pupils proportionally smaller. What dies first at 16 px is the white ring
between iris and pupil, and once it drops below a pixel the face collapses
into a bar. That is why the small cut's coordinates are not the full mark's
scaled down.

`markSource` applies the rule, so both cuts land in one `favicon.ico`: 48 px
full, 32 and 16 px small.

## Palette

The exact values in `styles.css`. The mark uses five of them.

| | Hex | Where |
| --- | --- | --- |
| Postgres | `#336791` | Ears, the shaded side. `--brand-1` |
| Mid | `#4F8FC0` | The cylinder body. `--brand-3` |
| Sky | `#7FB2DD` | The rim disc, connection lines. `--accent` |
| Amber | `#F0A94E` | Feet, two of the three hero streaks. `--amber` |
| Ink | `#0C1B2A` | Pupils and nose only |
| Field | `#0A1520` | The dark background the exports are checked against. `--cover-bg` |

Amber is the only warm value in the system and the only one that is not a
blue. Spend it on the feet and the streaks; it stops being an accent the
moment it appears anywhere else.

## Files

| File | Use |
| --- | --- |
| `output/brand/pg-mark-512.png`, `-256`, `-128` | The mark, transparent |
| `output/brand/pg-hero-840.png`, `-420` | The hero, transparent |
| `output/brand/favicon.ico` | 48 / 32 / 16, both cuts |
| `cover.png` | The published cover, 1600x2560, hero inlined as vector |

`pg-mark-256.png` is the repository's logo: the root `README.md` floats it
left of the intro paragraph at 80 px, well under a third of its pixel size,
so it stays sharp on a retina screen.

80 is sized against the shortest the paragraph ever gets. On a wide screen
it collapses to three lines of GitHub's 16px/1.5 body text (72 px) and a
floated logo taller than that hangs past the paragraph and pushes the
following blockquote's first line sideways. At 80 it ends level; at narrower
widths the text simply wraps under it, which is what a float is for. The
`<br clear="left"/>` after the paragraph is the second half of that fix and
is not optional.

That also makes these filenames part of the repository's public surface:
renaming one breaks the front page, and no build step will tell you.

Every PNG is transparent, and Chrome renders each one from the vector at its
final size rather than downsampling a large image, which is why 16 px still
has clean edges.

## Clear space and minimum size

Keep clear space equal to one foot's width on every side of the mark. The
ears already carry their own breathing room at the top; do not crowd the
feet to compensate.

The mark holds down to 16 px, but only as the small cut, and only as a
silhouette: at that size a viewer reads *blue cylinder, two eyes, orange
feet* and nothing more. That is enough for a favicon and not enough for
anything that has to be recognised in isolation. Below 16 px, use no mark.

The hero needs about 260 px of width before the face reads. Below that it is
the mark's job.

## Rules

Do not add `<style>`, CSS classes, or `var()` to the art. The book inlines
it into a single-document HTML build, where an SVG `<style>` block is
document-scoped and would restyle the whole book. Presentation attributes
only, hex values hardcoded, and this file is where they reconcile.

Do not apply a CSS `filter` to the inlined art either. Chrome rasterises
filtered subtrees when printing, which would undo the reason the art is
vector in the first place. See `README_EBOOK.md`.

The art deliberately uses flat colours and no gradients, so it contains no
`<defs>` and no ids. That is what makes it safe to inline alongside
`coverArtSVG` and the chapter diagrams without any chance of an id
collision.

`heroSVG` embeds `gopherArt` rather than repeating it. Edit the mark and the
hero follows; there is no second copy to forget.

## Rebuilding

```sh
cd book
go run . -format brand   # output/brand/
go run . -format cover   # cover.png
```

Outputs are committed, so a clone always has them. Rebuild both after any
edit to `gopherArt`: the mark exports and the cover both derive from it.
Editing `gopherSmallArt` alone only affects `-format brand`.
