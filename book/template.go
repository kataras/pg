package main

import "html/template"

// coverPartial is the cover, shared by the book shell and the standalone
// cover page so the two can never drift. The caller supplies CoverClass:
// "cover--print" for the A4 page, "cover--png" for the 1600x2560 canvas.
const coverPartial = `{{define "cover"}}
<section class="cover {{.CoverClass}}">
  <div class="cover-art">{{.CoverArt}}</div>
  <div class="cover-plate"></div>
  <div class="cover-masthead">
    <p class="cover-edition">{{.Meta.Edition}}</p>
    <h1 class="cover-title">{{.Meta.Title}}</h1>
    <div class="cover-rule"></div>
    <p class="cover-subtitle">{{.Meta.Subtitle}}</p>
  </div>
  <div class="cover-hero">{{.Hero}}</div>
  <div class="cover-footer">
    <div class="cover-footer-top">
      <div>
        <p class="cover-byline">Written by</p>
        <p class="cover-author">{{.Meta.Author}}</p>
      </div>
      <ul class="cover-links">
        <li class="cover-link-iris">{{.SiteHost}}</li>
        <li class="cover-link-company">{{.CompanyHost}}</li>
      </ul>
    </div>
    <ul class="cover-credit">
      {{range .Meta.Credit}}<li>{{.}}</li>{{end}}
    </ul>
  </div>
</section>
{{end}}`

// markTemplate is the page the brand exports are screenshotted from: one
// SVG on a transparent field, sized to fill the viewport.
//
// It deliberately does not include styles.css. That sheet sets an opaque
// background on body, and Chrome's transparency override respects a declared
// background rather than replacing it. Pulling the stylesheet in here would
// quietly produce white PNGs. Nothing else on this page needs it: the mark
// carries its own colours as presentation attributes.
var markTemplate = template.Must(template.New("mark").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>pg brand mark</title>
<style>
html, body { margin: 0; padding: 0; background: transparent; }
svg { display: block; width: 100vw; height: 100vh; }
</style>
</head>
<body>

{{.Mark}}

<script>
requestAnimationFrame(function () {
    requestAnimationFrame(function () { window.__markReady = true; });
});
</script>
</body>
</html>
`))

// bookTemplate is the single-page book shell: cover, colophon, table of
// contents and chapters. Pagination (page numbers, running headers, TOC
// leaders) is applied by Paged.js at render time, when the polyfill is
// present. Without it (see loadPagedJS) the shell still renders as one
// long, readable HTML document; it simply is not paginated.
var bookTemplate = template.Must(template.New("book").Parse(coverPartial + `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{.Meta.Title}}: {{.Meta.Subtitle}}</title>
<style>{{.CSS}}</style>
</head>
<body>

{{template "cover" .}}

<section class="colophon">
  <h2>About this book</h2>
  <p>{{.Meta.Description}}</p>
  <dl>
    <dt>Edition</dt><dd>{{.Meta.Edition}}</dd>
    <dt>Author</dt><dd>{{.Meta.Author}}</dd>
    <dt>Website</dt><dd><a href="{{.Meta.Website}}">{{.Meta.Website}}</a></dd>
    <dt>Source</dt><dd><a href="{{.Meta.Repository}}">{{.Meta.Repository}}</a></dd>
    <dt>License</dt><dd>{{.Meta.License}}</dd>
    <dt>Built</dt><dd>{{.Date}}</dd>
  </dl>
  <p class="colophon-note">Runnable programs that pair with this book live
  in the library's own <code>_examples</code> directory:
  <a href="{{.ExamplesURL}}">{{.ExamplesURL}}</a>.</p>
</section>

<nav class="toc">
  <h2>Contents</h2>
  <ol>
  {{range .Chapters}}
    <li class="toc-chapter{{if eq .Number 0}} toc-preface{{end}}">
      <a href="#{{.ID}}">{{if .Number}}<span class="toc-num">{{.Number}}</span>{{end}}
        <span class="toc-title">{{.Title}}</span></a>
      {{if .Number}}
      <ol class="toc-sections">
        {{range .Sections}}<li><a href="#{{.ID}}">{{.Title}}</a></li>{{end}}
      </ol>
      {{end}}
    </li>
  {{end}}
  </ol>
</nav>

{{range .Chapters}}
<section class="chapter{{if eq .Number 0}} preface{{end}}" id="{{.ID}}">
  <header class="chapter-opener">
    {{if .Number}}<div class="chapter-number">Chapter {{.Number}}</div>{{end}}
    <h1 data-chapter-title="{{.Title}}">{{.Title}}</h1>
  </header>
  {{.Body}}
</section>
{{end}}

{{if .PagedJS}}
<script>
window.PagedConfig = {
    auto: true,
    // Paged.js fragments an inline <svg> like any other container, so a
    // diagram that does not fit the remaining space is split mid-drawing.
    // break-inside is inert in this version, so the fix is an overflow
    // handler: when the overflow starts inside a diagram figure that is not
    // already at the top of the page, hand back a range covering the whole
    // figure so it moves to the next page intact. Defensive throughout:
    // if anything here fails, layout falls back to fragmenting.
    before: function () {
        try {
            var Paged = window.Paged;
            if (!Paged || !Paged.Handler || !Paged.registerHandlers) return;
            var KeepDiagramsWhole = class extends Paged.Handler {
                onOverflow(overflow, rendered, bounds) {
                    try {
                        var node = overflow && overflow.startContainer;
                        if (!node) return;
                        if (node.nodeType !== 1) node = node.parentElement;
                        var fig = node && node.closest
                            ? node.closest("figure.diagram") : null;
                        if (!fig) return;
                        // Already at the top of the page: moving it gains
                        // nothing and would loop forever.
                        if (fig.getBoundingClientRect().top <= bounds.top + 1) return;
                        var range = document.createRange();
                        range.selectNode(fig);
                        return range;
                    } catch (e) {
                        console.log("PAGEDJS-DIAGRAM " + e.message);
                    }
                }
            };
            Paged.registerHandlers(KeepDiagramsWhole);
        } catch (e) {
            console.log("PAGEDJS-DIAGRAM-INIT " + e.message);
        }
    },
    after: function (flow) {
        window.__pagedDone = flow.total; // the generator polls this.
        console.log("PAGEDJS-DONE pages=" + flow.total);
    }
};
window.addEventListener("error", function (e) {
    console.log("PAGEDJS-ERROR " + e.message + " @" + e.filename + ":" + e.lineno);
});
window.addEventListener("unhandledrejection", function (e) {
    console.log("PAGEDJS-REJECTION " + (e.reason && e.reason.message ? e.reason.message : e.reason));
});
// Debug aid: identify the exact node when Paged.js cannot make layout
// progress ("Unable to layout item") instead of "[object HTML...Element]".
(function () {
    var origWarn = console.warn;
    console.warn = function () {
        var args = Array.prototype.slice.call(arguments).map(function (a) {
            if (a && a.nodeType === 1) {
                return a.tagName + "#" + (a.id || "") + "." + (a.className || "") +
                    " :: " + (a.textContent || "").slice(0, 160).replace(/\n/g, "⏎");
            }
            return a;
        });
        origWarn.apply(console, args);
    };
})();
</script>
<script>{{.PagedJS}}</script>
{{else}}
<script>
// assets/paged.polyfill.js was not found at build time: this document is
// one long unpaginated page, with no page numbers, running headers or
// table-of-contents leaders. See README_EBOOK.md for where to get it.
window.__pagedDone = 0;
console.log("PAGEDJS-MISSING: rendered without pagination");
</script>
{{end}}
</body>
</html>
`))

// coverTemplate is the standalone cover: the same markup and stylesheet as
// the book's first page, sized to the screenshot canvas and with no
// Paged.js. It sets window.__coverReady once the embedded fonts have
// loaded and the browser has painted, which the generator polls before
// capturing. Screenshotting earlier catches fallback glyphs.
var coverTemplate = template.Must(template.New("coverpage").Parse(coverPartial + `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{.Meta.Title}}: {{.Meta.Edition}} cover</title>
<style>{{.CSS}}</style>
<style>
html { background: var(--cover-bg); }
body { width: 800px; height: 1280px; overflow: hidden; }
</style>
</head>
<body>

{{template "cover" .}}

<script>
document.fonts.ready.then(function () {
    requestAnimationFrame(function () {
        requestAnimationFrame(function () { window.__coverReady = true; });
    });
});
</script>
</body>
</html>
`))
