package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

// gopherArt is the pg mark, drawn to fill a 200x200 frame: a database
// cylinder that is also a gopher. The rim ellipse, the straight sides and
// the swelling bottom are the datastore glyph every developer already
// reads; the ears, the eyes, the two front teeth and the amber feet make it
// the Go mascot. One silhouette carries both readings at once, which is
// what keeps it a mark rather than an illustration with a badge glued on.
//
// The gopher is an original drawing in this library's own palette, not a
// trace of Renée French's Go gopher, and it is deliberately not an
// elephant: the book ships inside the library's own repository rather than
// on a standalone site, so the mark must not read as official PostgreSQL or
// Go artwork.
//
// It is the inner markup only, with no <svg> wrapper, because both markSVG
// and heroSVG embed it. The hero is this same drawing scaled into a larger
// scene, so the two cannot drift apart.
//
// Flat colours only, no gradients, no filters, no ids. The same markup is
// reused at every export size and inlined into the book's single-document
// HTML build, where a <defs> block would be document-scoped and an id could
// collide with a chapter diagram's.
const gopherArt = `<ellipse cx="36" cy="30" rx="22" ry="16.5" transform="rotate(-25 36 30)" fill="#336791"/>` +
	`<ellipse cx="164" cy="30" rx="22" ry="16.5" transform="rotate(25 164 30)" fill="#336791"/>` +
	`<ellipse cx="74" cy="176" rx="24" ry="12" fill="#F0A94E"/>` +
	`<ellipse cx="126" cy="176" rx="24" ry="12" fill="#F0A94E"/>` +
	`<path d="M28 50A72 24 0 0 1 172 50V142A72 24 0 0 1 28 142Z" fill="#4F8FC0"/>` +
	`<path d="M172 50V142A72 24 0 0 1 100 166V26A72 24 0 0 1 172 50Z" fill="#336791" opacity="0.18"/>` +
	`<ellipse cx="100" cy="50" rx="72" ry="24" fill="#7FB2DD"/>` +
	`<circle cx="76" cy="102" r="29" fill="#FFFFFF"/>` +
	`<circle cx="124" cy="102" r="29" fill="#FFFFFF"/>` +
	`<circle cx="84" cy="107" r="12" fill="#0C1B2A"/>` +
	`<circle cx="116" cy="107" r="12" fill="#0C1B2A"/>` +
	`<circle cx="88.5" cy="102" r="4.5" fill="#FFFFFF"/>` +
	`<circle cx="120.5" cy="102" r="4.5" fill="#FFFFFF"/>` +
	`<ellipse cx="100" cy="131" rx="10" ry="7.5" fill="#0C1B2A"/>` +
	`<path d="M92 137H99.2V156A3.6 3.6 0 0 1 92 156Z" fill="#FFFFFF"/>` +
	`<path d="M100.8 137H108V156A3.6 3.6 0 0 1 100.8 156Z" fill="#FFFFFF"/>`

// gopherSmallArt is the small-size cut, used below smallCutBelow. It is the
// same animal with the detail that closes up first taken out: the pupil
// highlights and the nose are gone, the teeth and the ears are heavier, and
// the rim is shallower so the eyes clear its front lip instead of crowding
// it. A mascot cannot survive a favicon unretouched; this is the retouch.
//
// The eyes carry that retouch. They are wider than the full mark's and
// their pupils are proportionally smaller, because what dies first at 16px
// is the white ring between the two: once it drops below a pixel the face
// collapses into one grey bar. Widening the ring is what keeps a face there
// at all, and it is why these coordinates are not just the mark's scaled.
const gopherSmallArt = `<ellipse cx="34" cy="32" rx="25" ry="19" transform="rotate(-22 34 32)" fill="#336791"/>` +
	`<ellipse cx="166" cy="32" rx="25" ry="19" transform="rotate(22 166 32)" fill="#336791"/>` +
	`<ellipse cx="70" cy="176" rx="27" ry="13" fill="#F0A94E"/>` +
	`<ellipse cx="130" cy="176" rx="27" ry="13" fill="#F0A94E"/>` +
	`<path d="M24 52A76 22 0 0 1 176 52V142A76 22 0 0 1 24 142Z" fill="#4F8FC0"/>` +
	`<ellipse cx="100" cy="52" rx="76" ry="22" fill="#7FB2DD"/>` +
	`<circle cx="65" cy="106" r="34" fill="#FFFFFF"/>` +
	`<circle cx="135" cy="106" r="34" fill="#FFFFFF"/>` +
	`<circle cx="73" cy="109" r="13" fill="#0C1B2A"/>` +
	`<circle cx="127" cy="109" r="13" fill="#0C1B2A"/>` +
	`<path d="M86 140H98.6V158A6.3 6.3 0 0 1 86 158Z" fill="#FFFFFF"/>` +
	`<path d="M101.4 140H114V158A6.3 6.3 0 0 1 101.4 158Z" fill="#FFFFFF"/>`

const (
	markSVG      = `<svg viewBox="0 0 200 200" xmlns="http://www.w3.org/2000/svg">` + gopherArt + `</svg>`
	markSmallSVG = `<svg viewBox="0 0 200 200" xmlns="http://www.w3.org/2000/svg">` + gopherSmallArt + `</svg>`
)

// heroSVG is the cover's larger centrepiece: the mark itself, ringed by two
// connection lines ending in small dots and trailed on the other side by
// three short streaks. The lines are the pooled client connections a Go
// program keeps open against the database, and the streaks are the
// throughput coming back out: connections in on the left, rows out on the
// right. The streaks are also where the hero says "fast", which the mark on
// its own does not: there is room for it here and none at 16px.
//
// Everything is still flat colour and pure primitives, so nothing here
// needs a raster fallback and it stays vector into the printed PDF.
const heroSVG = `<svg viewBox="0 0 420 300" xmlns="http://www.w3.org/2000/svg">` +
	`<circle cx="210" cy="150" r="140" fill="none" stroke="#4F8FC0" stroke-width="2" stroke-opacity="0.35"/>` +
	`<path d="M210 150L70 70" stroke="#7FB2DD" stroke-width="3" stroke-linecap="round"/>` +
	`<path d="M210 150L60 220" stroke="#7FB2DD" stroke-width="3" stroke-linecap="round"/>` +
	`<circle cx="70" cy="70" r="10" fill="#7FB2DD"/>` +
	`<circle cx="60" cy="220" r="10" fill="#7FB2DD"/>` +
	`<g stroke-linecap="round">` +
	`<path d="M296 112H342" stroke="#F0A94E" stroke-width="6" opacity="0.9"/>` +
	`<path d="M304 150H348" stroke="#7FB2DD" stroke-width="7" opacity="0.8"/>` +
	`<path d="M298 188H338" stroke="#F0A94E" stroke-width="5" opacity="0.6"/>` +
	`</g>` +
	`<g transform="translate(125 63) scale(0.85)">` + gopherArt + `</g>` +
	`</svg>`

// coverArtSVG is the faint field behind the cover (drawn at 50% opacity by
// the .cover-art rule in styles.css): a sparse scatter of nodes and edges,
// the same client/cluster motif as the hero, thinned out to a background
// texture. The viewBox is close to the A4 ratio the print cover uses, and
// every coordinate is a literal number rather than a generated pattern, so
// the file stays inspectable at a glance.
const coverArtSVG = `<svg viewBox="0 0 743 1052" xmlns="http://www.w3.org/2000/svg">` +
	`<g fill="#7fb2dd">` +
	`<circle cx="620" cy="90" r="5"/>` +
	`<circle cx="560" cy="150" r="3"/>` +
	`<circle cx="670" cy="180" r="4"/>` +
	`<circle cx="520" cy="240" r="3"/>` +
	`<circle cx="90" cy="860" r="5"/>` +
	`<circle cx="150" cy="920" r="3"/>` +
	`<circle cx="60" cy="960" r="4"/>` +
	`<circle cx="180" cy="990" r="3"/>` +
	`<circle cx="360" cy="40" r="3"/>` +
	`<circle cx="700" cy="400" r="3"/>` +
	`<circle cx="40" cy="600" r="3"/>` +
	`<circle cx="380" cy="1010" r="4"/>` +
	`</g>` +
	`<g stroke="#4f8fc0" stroke-width="1.2" stroke-opacity="0.6">` +
	`<path d="M620 90 L560 150"/>` +
	`<path d="M560 150 L670 180"/>` +
	`<path d="M560 150 L520 240"/>` +
	`<path d="M90 860 L150 920"/>` +
	`<path d="M150 920 L60 960"/>` +
	`<path d="M150 920 L180 990"/>` +
	`<path d="M360 40 L620 90"/>` +
	`<path d="M700 400 L670 180"/>` +
	`<path d="M40 600 L90 860"/>` +
	`<path d="M380 1010 L180 990"/>` +
	`</g>` +
	`</svg>`

// brandArt maps an exportJob's source key to its literal SVG markup. There
// is deliberately no file on disk behind any of the three names: unlike the
// iris-book brand kit, which ships as hand-authored files under a
// repository-level brand/ directory, pg keeps the source of truth in Go
// source and lets every consumer read the rendered exports instead.
var brandArt = map[string]string{
	"mark":       markSVG,
	"mark-small": markSmallSVG,
	"hero":       heroSVG,
}

// pngExports are written as files. icoSizes are rendered in memory and
// packed into favicon.ico.
var (
	pngExports = []int{512, 256, 128}
	icoSizes   = []int{48, 32, 16}
)

// smallCutBelow is the size at which the full mark stops holding together:
// the pupil highlights, the nose and the gap between the two teeth are all
// sub-pixel by then and smear into one grey mass. Square exports smaller
// than this render gopherSmallArt instead. 48px still resolves every
// detail, so the boundary sits there rather than lower.
const smallCutBelow = 48

// markSource picks the art for a square export of the given size.
func markSource(size int) string {
	if size < smallCutBelow {
		return "mark-small"
	}
	return "mark"
}

// heroExports are the landscape sizes useful in a README or a site header.
var heroExports = []struct{ w, h int }{{840, 600}, {420, 300}}

// buildBrand renders the brand exports into outputDir/brand. pg has no
// separate, repository-level brand directory, so they live under the book's
// own output directory.
//
// These paths are load-bearing outside this module: the repository's root
// README embeds book/output/brand/pg-mark-256.png as the project logo. Do
// not rename or relocate an export without updating that reference. A
// broken image there is the first thing a visitor sees, and nothing in this
// module's build will catch it.
func buildBrand(outputDir string) error {
	exportDir := filepath.Join(outputDir, "brand")
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		return err
	}

	jobs := make([]exportJob, 0, len(pngExports)+len(icoSizes)+len(heroExports))
	for _, s := range append(append([]int{}, pngExports...), icoSizes...) {
		jobs = append(jobs, exportJob{
			key: fmt.Sprintf("mark-%d", s), w: s, h: s, source: markSource(s)})
	}
	for _, h := range heroExports {
		jobs = append(jobs, exportJob{
			key: fmt.Sprintf("hero-%d", h.w), w: h.w, h: h.h, source: "hero"})
	}

	shots, err := capture(jobs)
	if err != nil {
		return err
	}

	for _, size := range pngExports {
		name := fmt.Sprintf("pg-mark-%d.png", size)
		key := fmt.Sprintf("mark-%d", size)
		if err = os.WriteFile(filepath.Join(exportDir, name), shots[key], 0o644); err != nil {
			return err
		}
		fmt.Printf("Brand: %s (%dx%d)\n", filepath.Join(exportDir, name), size, size)
	}

	for _, h := range heroExports {
		name := fmt.Sprintf("pg-hero-%d.png", h.w)
		key := fmt.Sprintf("hero-%d", h.w)
		if err = os.WriteFile(filepath.Join(exportDir, name), shots[key], 0o644); err != nil {
			return err
		}
		fmt.Printf("Brand: %s (%dx%d)\n", filepath.Join(exportDir, name), h.w, h.h)
	}

	ico := make([][]byte, 0, len(icoSizes))
	for _, size := range icoSizes {
		ico = append(ico, shots[fmt.Sprintf("mark-%d", size)])
	}
	icoPath := filepath.Join(exportDir, "favicon.ico")
	if err = os.WriteFile(icoPath, encodeICO(icoSizes, ico), 0o644); err != nil {
		return err
	}
	fmt.Printf("Brand: %s (%v)\n", icoPath, icoSizes)
	return nil
}

// exportJob is one raster to render: a brandArt entry at a given pixel size.
type exportJob struct {
	key    string
	w, h   int
	source string
}

// capture screenshots every job in one browser session, and returns the PNG
// bytes keyed by job.
//
// Two details make the difference between a usable export and a silently
// wrong one. The page carries no stylesheet: styles.css sets an opaque body
// background, which the transparency override would then faithfully preserve.
// And the default background colour is overridden per page, because
// chromedp.CaptureScreenshot captures from the surface, which composites the
// frame's background. Without the override every PNG comes out opaque white.
func capture(jobs []exportJob) (map[string][]byte, error) {
	browser, err := findBrowser()
	if err != nil {
		return nil, err
	}

	fmt.Printf("Rendering the brand exports with %s ...\n", browser)

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browser),
		chromedp.Flag("headless", "new"),
		chromedp.Flag("hide-scrollbars", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	runCtx, cancelRun := context.WithTimeout(browserCtx, 3*time.Minute)
	defer cancelRun()

	transparent := chromedp.ActionFunc(func(ctx context.Context) error {
		return emulation.SetDefaultBackgroundColorOverride().
			WithColor(&cdp.RGBA{R: 0, G: 0, B: 0, A: 0}).Do(ctx)
	})

	out := make(map[string][]byte, len(jobs))
	for _, job := range jobs {
		art, ok := brandArt[job.source]
		if !ok {
			return nil, fmt.Errorf("no brand art registered for %q", job.source)
		}

		var page bytes.Buffer
		data := map[string]any{"Mark": template.HTML(art)}
		if err = markTemplate.Execute(&page, data); err != nil {
			return nil, err
		}
		url := "data:text/html;base64," +
			base64.StdEncoding.EncodeToString(page.Bytes())

		var ready bool
		var png []byte
		err = chromedp.Run(runCtx,
			chromedp.EmulateViewport(int64(job.w), int64(job.h)),
			chromedp.Navigate(url),
			transparent,
			chromedp.Poll("window.__markReady", &ready,
				chromedp.WithPollingInterval(50*time.Millisecond)),
			chromedp.CaptureScreenshot(&png),
		)
		if err != nil {
			return nil, fmt.Errorf("%s at %dx%d: %w", browser, job.w, job.h, err)
		}
		out[job.key] = png
	}
	return out, nil
}

// encodeICO packs PNG payloads into an .ico. Every modern browser and every
// Windows since Vista reads PNG-compressed icon entries, so the images go in
// verbatim rather than being re-encoded as BMP.
func encodeICO(sizes []int, images [][]byte) []byte {
	const dirEntry = 16
	var buf bytes.Buffer

	// ICONDIR: reserved, type 1 (icon), image count.
	binary.Write(&buf, binary.LittleEndian, uint16(0))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(len(images)))

	offset := uint32(6 + dirEntry*len(images))
	for i, size := range sizes {
		// 256 is written as 0: the field is a single byte.
		dim := byte(size)
		if size >= 256 {
			dim = 0
		}
		buf.WriteByte(dim)                                  // width
		buf.WriteByte(dim)                                  // height
		buf.WriteByte(0)                                    // palette size, 0 for truecolour
		buf.WriteByte(0)                                    // reserved
		binary.Write(&buf, binary.LittleEndian, uint16(1))  // colour planes
		binary.Write(&buf, binary.LittleEndian, uint16(32)) // bits per pixel
		binary.Write(&buf, binary.LittleEndian, uint32(len(images[i])))
		binary.Write(&buf, binary.LittleEndian, offset)
		offset += uint32(len(images[i]))
	}
	for _, img := range images {
		buf.Write(img)
	}
	return buf.Bytes()
}
