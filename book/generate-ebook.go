// Command generate-ebook builds the pg book as a single, self-contained
// HTML file, and prints it to PDF through a headless Chromium browser
// (Chrome or Edge). Pagination (cover, table of contents with page
// numbers, running headers and footers, chapter openers) is produced by
// the vendored Paged.js polyfill (assets/paged.polyfill.js), which is not
// part of this repository; see README_EBOOK.md for where to get it. The
// HTML still renders without it, as one long unpaginated document.
//
// Usage:
//
//	go run . [-format pdf|html|cover|brand|all] [-output DIR] [-cover PATH]
//
// The HTML output embeds the stylesheet, syntax-highlighted code and (when
// present in assets/) the Inter fonts and Paged.js as data URIs or inline
// script, so the file is portable on its own. The brand mark, the cover
// hero and the cover background art are literal inline SVG, built from
// plain primitives directly in brand.go rather than read from vendored
// files, so the cover carries no raster images at all. -format cover
// screenshots the book's own cover page to cover.png at 1600x2560; it is
// the same markup and stylesheet as the PDF's first page, so the two
// cannot drift apart. -format brand renders the mark and hero art to the
// PNG and ICO exports under output/brand.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	chroma "github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/ast"
	mdhtml "github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
	"gopkg.in/yaml.v3"
)

// Metadata is the book front matter, read from metadata.yml.
type Metadata struct {
	Title       string   `yaml:"title"`
	Subtitle    string   `yaml:"subtitle"`
	Author      string   `yaml:"author"`
	Credit      []string `yaml:"credit"`
	Edition     string   `yaml:"edition"`
	Website     string   `yaml:"website"`
	Company     string   `yaml:"company"`
	Repository  string   `yaml:"repository"`
	License     string   `yaml:"license"`
	Description string   `yaml:"description"`
}

// Chapter is one rendered book unit.
type Chapter struct {
	File     string
	Number   int // 0 = preface or epilogue (unnumbered).
	ID       string
	Title    string
	Sections []Section
	Body     template.HTML
}

// Section is an H2 entry used by the table of contents.
type Section struct {
	ID    string
	Title string
}

// chapterFiles is the book spine, in reading order. README.md renders as
// the unnumbered preface and epilogue.md as unnumbered back matter.
var chapterFiles = []string{
	"README.md",
	"01-getting-started.md",
	"02-schema-and-struct-tags.md",
	"03-connections-and-configuration.md",
	"04-repositories-and-crud.md",
	"05-querying-and-scanning.md",
	"06-filtering-and-pagination.md",
	"07-writing-data.md",
	"08-bulk-loading-and-streaming.md",
	"09-transactions.md",
	"10-errors.md",
	"11-schema-management-and-migrations.md",
	"12-listen-notify.md",
	"13-introspection-and-code-generation.md",
	"14-security.md",
	"15-observability-and-operations.md",
	"16-testing.md",
	"epilogue.md",
}

var (
	reNextFooter  = regexp.MustCompile(`(?ms)^---\s*\n\s*\*\*Next( Chapter)?\*\*.*\z`)
	reH1          = regexp.MustCompile(`(?m)^# (.+)$`)
	reChapterFile = regexp.MustCompile(`^(\d+)-`)
)

// stripChapterTOC removes the chapter's inline "## Table of Contents"
// section (a web-reading aid): everything from that heading up to, but not
// including, the next "## " heading.
func stripChapterTOC(source []byte) []byte {
	const heading = "## Table of Contents"

	start := bytes.Index(source, []byte("\n"+heading))
	if start < 0 {
		if bytes.HasPrefix(source, []byte(heading)) {
			start = 0
		} else {
			return source
		}
	} else {
		start++ // keep the preceding newline.
	}

	rest := source[start+len(heading):]
	next := bytes.Index(rest, []byte("\n## "))
	if next < 0 {
		return source[:start]
	}

	out := make([]byte, 0, len(source))
	out = append(out, source[:start]...)
	out = append(out, rest[next+1:]...)
	return out
}

func main() {
	format := flag.String("format", "pdf", "output format: pdf, html, cover, brand or all")
	outputDir := flag.String("output", "output", "output directory")
	coverPath := flag.String("cover", "cover.png", "cover image path, for -format cover")
	limit := flag.Int("chapters", 0, "limit the spine to the first N present files (debug aid)")
	flag.Parse()

	mode := strings.ToLower(*format)
	switch mode {
	case "html", "pdf", "cover", "brand", "all":
	default:
		log.Fatalf("unknown format %q (want pdf, html, cover, brand or all)", *format)
	}

	meta, err := loadMetadata("metadata.yml")
	if err != nil {
		log.Fatalf("metadata: %v", err)
	}

	if err = os.MkdirAll(*outputDir, 0o755); err != nil {
		log.Fatalf("output dir: %v", err)
	}

	if mode == "brand" || mode == "all" {
		if err = buildBrand(*outputDir); err != nil {
			log.Fatalf("brand: %v", err)
		}
		if mode == "brand" {
			return
		}
	}

	if mode == "cover" || mode == "all" {
		if err = buildCover(meta, *outputDir, *coverPath); err != nil {
			log.Fatalf("cover: %v", err)
		}
		if mode == "cover" {
			return
		}
	}

	chapters, err := loadChapters(chapterFiles, *limit)
	if err != nil {
		log.Fatalf("chapters: %v", err)
	}
	if len(chapters) == 0 {
		log.Fatalf("chapters: none of the spine files in chapterFiles were found")
	}

	page, pagedOK, err := renderBook(meta, chapters)
	if err != nil {
		log.Fatalf("render: %v", err)
	}

	htmlPath := filepath.Join(*outputDir, "pg-book.html")
	if err = os.WriteFile(htmlPath, page, 0o644); err != nil {
		log.Fatalf("write html: %v", err)
	}
	fmt.Printf("HTML: %s (%.1f MB)\n", htmlPath, float64(len(page))/1e6)

	if mode == "html" {
		return
	}

	// Paged.js is what turns this document into a paginated one, and the
	// PDF step below waits on the completion flag it sets. Without it
	// there is nothing to wait for and no page count to print, so PDF
	// output is refused with an actionable error rather than silently
	// printing one giant unpaginated page.
	if !pagedOK {
		log.Fatalf("pdf: assets/paged.polyfill.js is required to paginate the " +
			"PDF but was not found; see README_EBOOK.md for where to get it, " +
			"or use -format html, which renders without it")
	}

	pdfPath := filepath.Join(*outputDir, "pg-book.pdf")
	if err = printToPDF(htmlPath, pdfPath); err != nil {
		log.Fatalf("pdf: %v", err)
	}
	info, _ := os.Stat(pdfPath)
	fmt.Printf("PDF:  %s (%.1f MB)\n", pdfPath, float64(info.Size())/1e6)
}

// buildCover renders the standalone cover page into the output directory and
// screenshots it to pngPath. The PNG does not live in the output directory:
// like the rest of the book's committed deliverables, it is meant to be
// tracked rather than treated as disposable build output.
func buildCover(meta Metadata, outputDir, pngPath string) error {
	page, err := renderCoverPage(meta)
	if err != nil {
		return err
	}

	htmlPath := filepath.Join(outputDir, "pg-cover.html")
	if err = os.WriteFile(htmlPath, page, 0o644); err != nil {
		return err
	}
	if err = captureCover(htmlPath, pngPath); err != nil {
		return err
	}

	fmt.Printf("Cover: %s (%dx%d)\n", pngPath,
		coverWidth*coverScale, coverHeight*coverScale)
	return nil
}

func loadMetadata(filename string) (Metadata, error) {
	var meta Metadata
	data, err := os.ReadFile(filename)
	if err != nil {
		return meta, err
	}
	return meta, yaml.Unmarshal(data, &meta)
}

// chapterNumber derives a spine file's chapter number from its filename
// prefix (e.g. "04-repositories-and-crud.md" -> 4) rather than from its
// position in the (possibly gappy) list of files actually present on disk.
// That keeps a chapter's printed number and its "Chapter N:" title stable
// while sibling chapters are still being written and are not yet present.
func chapterNumber(file string) int {
	m := reChapterFile.FindStringSubmatch(file)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// loadChapters reads every file in files that exists, in order, and skips
// the rest with a warning: other spine files are written concurrently by
// other authors, and a book with a still-growing table of contents should
// build every time, not only once every chapter exists. limit, if positive,
// caps how many *present* files are rendered, as a fast debug loop.
func loadChapters(files []string, limit int) ([]*Chapter, error) {
	chapters := make([]*Chapter, 0, len(files))
	for _, file := range files {
		if limit > 0 && len(chapters) >= limit {
			break
		}

		data, err := os.ReadFile(file)
		if err != nil {
			if os.IsNotExist(err) {
				log.Printf("skipping %s: not written yet", file)
				continue
			}
			return nil, fmt.Errorf("%s: %w", file, err)
		}

		ch := &Chapter{File: file}
		switch file {
		case "README.md": // front matter: unnumbered.
			ch.Number = 0
			ch.ID = "preface"
		case "epilogue.md": // back matter: unnumbered.
			ch.Number = 0
			ch.ID = "epilogue"
		default:
			ch.Number = chapterNumber(file)
			ch.ID = fmt.Sprintf("ch%02d", ch.Number)
		}

		if err = ch.render(data); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		chapters = append(chapters, ch)
	}
	return chapters, nil
}

// render converts one chapter's markdown to HTML: the inline chapter TOC
// and the next-chapter footer are web-reading aids and are dropped from the
// book, heading IDs get a per-chapter prefix so anchors never collide, and
// fenced code blocks are syntax-highlighted with chroma.
func (ch *Chapter) render(source []byte) error {
	source = stripChapterTOC(source)
	source = reNextFooter.ReplaceAll(source, nil)

	if m := reH1.FindSubmatch(source); m != nil {
		ch.Title = cleanTitle(string(m[1]), ch.Number)
	} else {
		ch.Title = ch.File
	}
	// The H1 is re-rendered by the chapter-opener template.
	source = reH1.ReplaceAll(source, nil)

	p := parser.NewWithExtensions(parser.CommonExtensions |
		parser.AutoHeadingIDs | parser.NoEmptyLineBeforeBlock)
	doc := p.Parse(source)

	prefix := ch.ID + "-"
	ast.WalkFunc(doc, func(node ast.Node, entering bool) ast.WalkStatus {
		if h, ok := node.(*ast.Heading); ok && entering && h.Level == 2 {
			ch.Sections = append(ch.Sections, Section{
				ID:    prefix + h.HeadingID,
				Title: nodeText(h),
			})
		}
		return ast.GoToNext
	})

	// No LazyLoadImages: every image in the book is a diagram and is inlined
	// as markup, so the flag has nothing left to act on. A deferred
	// loading="lazy" image would never satisfy Paged.js's image wait, which
	// resolves only on complete/onload/onerror.
	opts := mdhtml.RendererOptions{
		Flags:           mdhtml.CommonFlags,
		HeadingIDPrefix: prefix,
		RenderNodeHook:  renderNodeHook,
	}
	renderer := mdhtml.NewRenderer(opts)

	diagramErr = nil
	ch.Body = template.HTML(markdown.Render(doc, renderer))
	return diagramErr
}

// cleanTitle strips a leading "Chapter N:" from the H1; the opener template
// renders the number itself.
func cleanTitle(title string, number int) string {
	title = strings.TrimSpace(title)
	prefix := fmt.Sprintf("Chapter %d:", number)
	if strings.HasPrefix(title, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(title, prefix))
	}
	return title
}

// nodeText is a node's plain text, gathered from its leaves: a heading's
// title, an image's alt text.
func nodeText(node ast.Node) string {
	var sb strings.Builder
	ast.WalkFunc(node, func(n ast.Node, entering bool) ast.WalkStatus {
		if leaf := n.AsLeaf(); leaf != nil && entering {
			sb.Write(leaf.Literal)
		}
		return ast.GoToNext
	})
	return sb.String()
}

// renderNodeHook is the renderer's single node hook, so everything the book
// renders for itself dispatches from here.
func renderNodeHook(w io.Writer, node ast.Node, entering bool) (ast.WalkStatus, bool) {
	switch n := node.(type) {
	case *ast.CodeBlock:
		return renderCodeBlock(w, n)
	case *ast.Image:
		return renderDiagram(w, n, entering)
	case *ast.Paragraph:
		return unwrapDiagramParagraph(n)
	}
	return ast.GoToNext, false
}

// renderCodeBlock renders fenced code blocks through chroma with CSS
// classes, wrapped in a figure that carries a language badge.
func renderCodeBlock(w io.Writer, cb *ast.CodeBlock) (ast.WalkStatus, bool) {
	lang := string(cb.Info)
	if i := strings.IndexAny(lang, " \t"); i >= 0 {
		lang = lang[:i]
	}

	// Blocks near or above one page in height must be allowed to fragment,
	// otherwise Paged.js cannot place them and aborts the whole layout.
	class := "code"
	if bytes.Count(cb.Literal, []byte("\n")) > 28 {
		class = "code tall"
	}

	fmt.Fprintf(w, `<figure class="%s"%s>`, class, langAttr(lang))
	if lang != "" && lang != "text" {
		fmt.Fprintf(w, `<figcaption>%s</figcaption>`, template.HTMLEscapeString(lang))
	}
	if err := highlight(w, lang, string(cb.Literal)); err != nil {
		fmt.Fprintf(w, "<pre><code>%s</code></pre>",
			template.HTMLEscapeString(string(cb.Literal)))
	}
	io.WriteString(w, `</figure>`)
	return ast.GoToNext, true
}

// Diagram inlining. Chapter diagrams are hand-authored SVG files under
// assets/diagrams, referenced from the markdown as ordinary images so they
// render on GitHub too. Here they are inlined as literal markup: an <img>
// is an isolated document that cannot see this page's embedded Inter, so a
// linked diagram would print its labels in a fallback font, while an inline
// <svg> inherits the document's fonts and stays vector at print resolution.
var (
	// diagramErr carries the first failure out of the render hook, whose
	// signature cannot return one. A diagram that silently vanished from the
	// book would ship unnoticed.
	diagramErr error

	// diagramIDs maps every id declared inside an inlined diagram to the file
	// that declared it. All diagrams land in one HTML document, so a marker,
	// gradient or filter id used by two files would bind every reference to
	// whichever was inlined first.
	diagramIDs = map[string]string{}

	reSVGID    = regexp.MustCompile(`\sid\s*=\s*["']([^"']+)["']`)
	reSVGBox   = regexp.MustCompile(`viewBox\s*=\s*["']\s*[-\d.]+\s+[-\d.]+\s+([\d.]+)\s+([\d.]+)`)
	reHTMLWrap = regexp.MustCompile(`\s+`)
)

func isDiagram(dest string) bool {
	return strings.HasSuffix(strings.ToLower(dest), ".svg")
}

func renderDiagram(w io.Writer, img *ast.Image, entering bool) (ast.WalkStatus, bool) {
	dest := string(img.Destination)
	if !isDiagram(dest) {
		return ast.GoToNext, false // any other image: the default renderer.
	}
	// ast.Image is a container, so Walk visits it again on the way out even
	// though its children were skipped. Swallow that visit, or the default
	// renderer closes a tag this hook never opened.
	if !entering {
		return ast.GoToNext, true
	}

	svg, err := readDiagram(dest)
	if err != nil {
		if diagramErr == nil {
			diagramErr = fmt.Errorf("diagram %s: %w", dest, err)
		}
		return ast.SkipChildren, true
	}

	fmt.Fprint(w, `<figure class="diagram">`)
	io.WriteString(w, svg)
	// The alt text doubles as the caption, so the two never disagree.
	if caption := strings.TrimSpace(reHTMLWrap.ReplaceAllString(nodeText(img), " ")); caption != "" {
		fmt.Fprintf(w, `<figcaption>%s</figcaption>`,
			template.HTMLEscapeString(caption))
	}
	io.WriteString(w, `</figure>`)
	return ast.SkipChildren, true
}

// unwrapDiagramParagraph drops the <p> around a lone diagram. A standalone
// image line parses as a paragraph containing an image, and CommonExtensions
// has no figure extension, so the default output would be
// <p><figure>...</figure></p>. The browser repairs that nesting by leaving an
// empty <p> behind, and stray nodes are what stalls Paged.js.
func unwrapDiagramParagraph(p *ast.Paragraph) (ast.WalkStatus, bool) {
	var img *ast.Image
	for _, child := range p.GetChildren() {
		switch n := child.(type) {
		case *ast.Image:
			if img != nil {
				return ast.GoToNext, false
			}
			img = n
		case *ast.Text:
			if len(bytes.TrimSpace(n.Literal)) > 0 {
				return ast.GoToNext, false
			}
		default:
			return ast.GoToNext, false
		}
	}
	if img == nil || !isDiagram(string(img.Destination)) {
		return ast.GoToNext, false
	}
	// Handled: emit no tags, but keep walking so the image still renders.
	return ast.GoToNext, true
}

// svgRoot trims an SVG file down to its root <svg> element. Anything before
// it (an XML prolog, a DOCTYPE, an authoring comment) is dropped, since
// none of it belongs in the middle of an HTML document. Every diagram this
// program inlines from disk goes through here.
func svgRoot(data []byte) (string, error) {
	svg := string(data)
	i := strings.Index(svg, "<svg")
	if i < 0 {
		return "", fmt.Errorf("no <svg> root element")
	}
	svg = strings.TrimSpace(svg[i:])
	if !reSVGBox.MatchString(svg) {
		return "", fmt.Errorf("no viewBox on the root <svg>: without one the " +
			"inline SVG has no intrinsic ratio and lays out at the default " +
			"object size")
	}
	return svg, nil
}

// readDiagram loads a chapter diagram for inlining, and enforces the two
// rules that would otherwise fail silently: unique ids and a printable height.
func readDiagram(dest string) (string, error) {
	data, err := os.ReadFile(filepath.FromSlash(dest))
	if err != nil {
		return "", err
	}

	svg, err := svgRoot(data)
	if err != nil {
		return "", err
	}

	for _, m := range reSVGID.FindAllStringSubmatch(svg, -1) {
		if owner, taken := diagramIDs[m[1]]; taken {
			return "", fmt.Errorf(`id %q is already declared by %s; `+
				`every diagram is inlined into one document, so prefix ids `+
				`with the diagram's own name`, m[1], owner)
		}
		diagramIDs[m[1]] = dest
	}
	if err = checkDiagramBox(svg); err != nil {
		return "", err
	}
	return svg, nil
}

// maxDiagramHeight is the tallest a diagram may print, in millimetres. The
// A4 text block is 251mm; the margin below that is for the caption and for
// the fact that a figure rarely starts at the top of a page.
const maxDiagramHeight = 205

// checkDiagramBox is a build error and not a warning, because both ways a
// diagram fails here are silent:
//
//   - An unusable viewBox. svgRoot has already rejected a missing one, since
//     without it the inline <svg> has no intrinsic ratio and lays out at the
//     default object size; this catches a present but unparseable box.
//   - Too tall to place. Paged.js logs "Unable to layout item" and moves on:
//     the page content box is a multi-column container with no overflow
//     clipping, so the figure is pushed into an off-sheet column. It vanishes
//     from the PDF while the build still reports success.
//
// There is deliberately no CSS break control to pair with this. In the
// vendored Paged.js (0.4.3) `break-inside: avoid` is inert (avoidBreakInside
// is never called and the Breaks handler never captures the property), and
// `break-before`/`break-after: avoid` are worse than useless: they are
// captured, and hoisting a break can leave the layout with no way to make
// progress, which aborts the book. Diagrams are kept whole by the
// onOverflow handler in the page template instead.
func checkDiagramBox(svg string) error {
	m := reSVGBox.FindStringSubmatch(svg)
	if m == nil {
		return fmt.Errorf("no viewBox on the root <svg>: without one the " +
			"inlined diagram has no intrinsic aspect ratio and prints at a " +
			"default 150px height")
	}
	width, errW := strconv.ParseFloat(m[1], 64)
	height, errH := strconv.ParseFloat(m[2], 64)
	if errW != nil || errH != nil || width <= 0 || height <= 0 {
		return fmt.Errorf("unreadable viewBox %q %q", m[1], m[2])
	}

	// The SVG scales to the 170mm text column: A4 less its 20mm side margins.
	if printed := 170 * height / width; printed > maxDiagramHeight {
		return fmt.Errorf("would print %.0fmm tall, over the %dmm limit; "+
			"split it at a phase boundary rather than shrinking the type",
			printed, maxDiagramHeight)
	}
	return nil
}

func langAttr(lang string) string {
	if lang == "" {
		return ""
	}
	return fmt.Sprintf(` data-lang=%q`, lang)
}

// highlight renders code with chroma, one block-level element per source
// line. Block children give Paged.js clean fragmentation points; a single
// monolithic <pre> text run cannot be split and aborts pagination.
func highlight(w io.Writer, lang, code string) error {
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return err
	}

	lines := chroma.SplitTokensIntoLines(iterator.Tokens())

	io.WriteString(w, `<pre class="chroma"><code>`)
	for _, line := range lines {
		// The block-level div provides the line break itself.
		if n := len(line); n > 0 {
			last := line[n-1]
			last.Value = strings.TrimRight(last.Value, "\n")
			line[n-1] = last
		}

		io.WriteString(w, `<div class="cl">`)
		if isBlankLine(line) {
			io.WriteString(w, "&#8203;") // keep the empty line's height.
		} else {
			writeTokens(w, line)
		}
		io.WriteString(w, `</div>`)
	}
	io.WriteString(w, `</code></pre>`)
	return nil
}

// writeTokens emits every token wrapped in a span, including pure
// whitespace tokens. Paged.js drops bare whitespace-only text nodes while
// paginating, which would glue `func main` into `funcmain`; wrapping each
// token in an element makes the whitespace survive.
func writeTokens(w io.Writer, line []chroma.Token) {
	for _, tok := range line {
		if tok.Value == "" {
			continue
		}
		if cls := tokenClass(tok.Type); cls != "" {
			fmt.Fprintf(w, `<span class="%s">%s</span>`,
				cls, template.HTMLEscapeString(tok.Value))
		} else {
			fmt.Fprintf(w, "<span>%s</span>",
				template.HTMLEscapeString(tok.Value))
		}
	}
}

// tokenClass resolves the chroma CSS class of a token type, walking up the
// token category hierarchy exactly like chroma's own HTML formatter.
func tokenClass(tt chroma.TokenType) string {
	for t := tt; t != 0; t = t.Parent() {
		if cls, ok := chroma.StandardTypes[t]; ok && cls != "" {
			return cls
		}
	}
	return ""
}

func isBlankLine(line []chroma.Token) bool {
	for _, tok := range line {
		if strings.TrimSpace(tok.Value) != "" {
			return false
		}
	}
	return true
}

// chromaStyle is the syntax-highlight palette; "github" is a light scheme
// that sits well on the brand's #f5f7f8 code background.
func chromaStyle() *chroma.Style {
	return chromastyles.Get("github")
}

// chromaCSS returns the highlight stylesheet for the chosen chroma style.
func chromaCSS() (string, error) {
	var buf bytes.Buffer
	formatter := chromahtml.New(chromahtml.WithClasses(true))
	if err := formatter.WriteCSS(&buf, chromaStyle()); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// dataURI embeds a local asset file as a data: URI.
func dataURI(path, mime string) (template.URL, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return template.URL("data:" + mime + ";base64," +
		base64.StdEncoding.EncodeToString(data)), nil
}

// loadFont embeds one vendored font file as a data: URI. Unlike the other
// assets this generator reads, a missing font is not an error: it is
// reported once as a warning and the caller falls back to the system
// sans-serif stack already declared in styles.css, so an HTML build never
// fails only because nobody has vendored Inter yet.
func loadFont(path string) (template.URL, bool) {
	uri, err := dataURI(path, "font/woff2")
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("warning: %s: %v", path, err)
		}
		return "", false
	}
	return uri, true
}

// loadPagedJS reads the vendored Paged.js polyfill (assets/paged.polyfill.js).
// It is optional for HTML output (the page degrades to one long, readable,
// unpaginated document without it) but required for PDF, which main
// enforces once it knows whether this call found the file.
func loadPagedJS() ([]byte, bool, error) {
	data, err := os.ReadFile(filepath.Join("assets", "paged.polyfill.js"))
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("warning: assets/paged.polyfill.js not found; HTML will " +
				"render without pagination (no page numbers, no chapter " +
				"breaks). See README_EBOOK.md for where to get it.")
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

// shared holds the ingredients both HTML outputs inline: the stylesheet
// with the embedded fonts (when present) and highlight rules, and the
// cover hero and cover art as literal SVG markup. Inlining the art rather
// than linking it as an image is what keeps the whole cover page vector
// all the way into the PDF.
type shared struct {
	CSS      template.CSS
	Hero     template.HTML
	CoverArt template.HTML
}

func inlineAssets() (shared, error) {
	var s shared

	css, err := os.ReadFile("styles.css")
	if err != nil {
		return s, fmt.Errorf("styles.css: %w", err)
	}
	highlightCSS, err := chromaCSS()
	if err != nil {
		return s, err
	}

	var fonts string
	romanURI, romanOK := loadFont(filepath.Join("assets", "inter-roman-latin.woff2"))
	italicURI, italicOK := loadFont(filepath.Join("assets", "inter-italic-latin.woff2"))
	if romanOK && italicOK {
		fonts = fmt.Sprintf(`
@font-face {
  font-family: "Inter";
  font-style: normal;
  font-weight: 100 900;
  font-display: block;
  src: url(%s) format("woff2");
}
@font-face {
  font-family: "Inter";
  font-style: italic;
  font-weight: 100 900;
  font-display: block;
  src: url(%s) format("woff2");
}`, romanURI, italicURI)
	} else {
		log.Printf("warning: Inter font files not found in assets/; falling " +
			"back to system fonts. See README_EBOOK.md for where to get them.")
	}

	s.CSS = template.CSS(fonts + "\n" + string(css) + "\n" + highlightCSS)
	s.Hero = template.HTML(heroSVG)
	s.CoverArt = template.HTML(coverArtSVG)
	return s, nil
}

// siteHost is the website URL without its scheme; the cover prints the bare
// host rather than a URL.
func siteHost(website string) string {
	host := strings.TrimPrefix(website, "https://")
	host = strings.TrimPrefix(host, "http://")
	return strings.TrimSuffix(host, "/")
}

// renderBook returns the rendered HTML and whether Paged.js was found and
// embedded (pagedOK); main refuses -format pdf when it is false.
func renderBook(meta Metadata, chapters []*Chapter) ([]byte, bool, error) {
	assets, err := inlineAssets()
	if err != nil {
		return nil, false, err
	}
	pagedJS, pagedOK, err := loadPagedJS()
	if err != nil {
		return nil, false, err
	}

	data := map[string]any{
		"Meta":        meta,
		"Chapters":    chapters,
		"CSS":         assets.CSS,
		"PagedJS":     template.JS(pagedJS),
		"Hero":        assets.Hero,
		"CoverArt":    assets.CoverArt,
		"CoverClass":  "cover--print",
		"SiteHost":    siteHost(meta.Website),
		"CompanyHost": siteHost(meta.Company),
		"Date":        time.Now().Format("January 2006"),
		"ExamplesURL": "https://github.com/kataras/pg/tree/main/_examples",
	}

	var buf bytes.Buffer
	if err := bookTemplate.Execute(&buf, data); err != nil {
		return nil, false, err
	}
	return buf.Bytes(), pagedOK, nil
}

// renderCoverPage builds the standalone cover: the book's own cover markup
// and stylesheet on a fixed canvas, without Paged.js.
func renderCoverPage(meta Metadata) ([]byte, error) {
	assets, err := inlineAssets()
	if err != nil {
		return nil, err
	}

	data := map[string]any{
		"Meta":        meta,
		"CSS":         assets.CSS,
		"Hero":        assets.Hero,
		"CoverArt":    assets.CoverArt,
		"CoverClass":  "cover--png",
		"SiteHost":    siteHost(meta.Website),
		"CompanyHost": siteHost(meta.Company),
	}

	var buf bytes.Buffer
	if err := coverTemplate.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// printToPDF renders the HTML with a headless Chromium browser driven over
// the DevTools protocol: navigate, wait until Paged.js reports that the
// full layout is complete (window.__pagedDone, set by the template's
// PagedConfig.after hook), then print. A fixed --virtual-time-budget would
// print mid-layout on a book this size.
func printToPDF(htmlPath, pdfPath string) error {
	browser, err := findBrowser()
	if err != nil {
		return err
	}

	absHTML, err := filepath.Abs(htmlPath)
	if err != nil {
		return err
	}

	fmt.Printf("Printing with %s ...\n", browser)

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browser),
		chromedp.Flag("headless", "new"),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	// Full-length layout takes real minutes; poll the completion flag.
	runCtx, cancelRun := context.WithTimeout(browserCtx, 20*time.Minute)
	defer cancelRun()

	var pages int
	var pdf []byte
	err = chromedp.Run(runCtx,
		chromedp.Navigate("file:///"+filepath.ToSlash(absHTML)),
		chromedp.Poll("window.__pagedDone", &pages,
			chromedp.WithPollingInterval(time.Second)),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var errAct error
			pdf, _, errAct = page.PrintToPDF().
				WithPrintBackground(true).
				WithPreferCSSPageSize(true).
				WithDisplayHeaderFooter(false).
				Do(ctx)
			return errAct
		}),
	)
	if err != nil {
		return fmt.Errorf("%s: %w", browser, err)
	}

	fmt.Printf("Paged.js laid out %d pages.\n", pages)
	return os.WriteFile(pdfPath, pdf, 0o644)
}

// The cover canvas, in CSS pixels, and the device scale it is captured at:
// 800x1280 at 2x is 1600x2560, a standard ebook cover size at the same
// 1:1.6 proportion as the cover published alongside this book. The CSS
// side of this lives in the .cover--png rule.
const (
	coverWidth  = 800
	coverHeight = 1280
	coverScale  = 2
)

// captureCover screenshots the standalone cover page. It waits for
// window.__coverReady (set by the page once the embedded fonts have loaded
// and the browser has painted twice) because capturing earlier catches
// fallback glyphs mid-swap.
func captureCover(htmlPath, pngPath string) error {
	browser, err := findBrowser()
	if err != nil {
		return err
	}

	absHTML, err := filepath.Abs(htmlPath)
	if err != nil {
		return err
	}

	fmt.Printf("Capturing the cover with %s ...\n", browser)

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browser),
		chromedp.Flag("headless", "new"),
		chromedp.Flag("hide-scrollbars", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	runCtx, cancelRun := context.WithTimeout(browserCtx, 2*time.Minute)
	defer cancelRun()

	var ready bool
	var png []byte
	err = chromedp.Run(runCtx,
		chromedp.EmulateViewport(coverWidth, coverHeight,
			chromedp.EmulateScale(coverScale)),
		chromedp.Navigate("file:///"+filepath.ToSlash(absHTML)),
		chromedp.Poll("window.__coverReady", &ready,
			chromedp.WithPollingInterval(100*time.Millisecond)),
		chromedp.CaptureScreenshot(&png),
	)
	if err != nil {
		return fmt.Errorf("%s: %w", browser, err)
	}

	return os.WriteFile(pngPath, png, 0o644)
}

// findBrowser locates Chrome or Edge. Override with PG_BOOK_BROWSER.
func findBrowser() (string, error) {
	if env := os.Getenv("PG_BOOK_BROWSER"); env != "" {
		return env, nil
	}

	candidates := []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		"/usr/bin/google-chrome",
		"/usr/bin/chromium",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	for _, name := range []string{"chrome", "google-chrome", "chromium", "msedge"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no Chrome/Edge found; set PG_BOOK_BROWSER to a Chromium browser executable")
}
