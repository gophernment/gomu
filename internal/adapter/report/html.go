package report

import (
	"errors"
	"fmt"
	"html/template"
	"io"
	"slices"
	"strings"

	"github.com/gophernment/gomu/internal/mutant"
)

// HTML writes a report as one self-contained web page: inline style, no
// script, no external resource. Open it in a browser or keep it as a CI
// artifact.
//
// Like Text, it lists the survived mutants by default and every mutant when
// Verbose is set, grouped by file and ordered by line, column, then operator.
//
// All text goes through html/template and through the same control-character
// escaping as Text, and the page carries a Content-Security-Policy that
// forbids everything but its own style, so a hostile file name cannot run
// code or spoof the layout.
type HTML struct {
	Verbose bool
}

type htmlPage struct {
	HasMutants bool
	HasScore   bool
	Score      string
	Percent    float64
	Level      string
	Detected   int
	Evaluated  int
	Summary    mutant.Summary
	Files      []htmlFile
}

type htmlFile struct {
	Name    string
	Mutants []htmlMutant
}

type htmlMutant struct {
	Line, Column int
	Operator     string
	Original     string
	Replacement  string
	Status       string
	Class        string
}

// Write renders mutants to w in a single Write call. It does not modify mutants.
func (h HTML) Write(w io.Writer, mutants []mutant.Mutant) error {
	if w == nil {
		return errors.New("report: nil writer")
	}

	sorted := slices.Clone(mutants)
	slices.SortStableFunc(sorted, compare)

	s := mutant.Summarize(mutants)
	page := htmlPage{
		HasMutants: len(mutants) > 0,
		Score:      "n/a",
		Level:      "none",
		Evaluated:  s.Killed + s.TimedOut + s.Survived,
		Detected:   s.Killed + s.TimedOut,
		Summary:    s,
	}
	if page.Evaluated > 0 {
		page.HasScore = true
		page.Percent = s.Score()
		page.Score = fmt.Sprintf("%.1f%%", page.Percent)
		page.Level = level(page.Percent)
	}

	for _, mu := range sorted {
		if !h.Verbose && mu.Status != mutant.Survived {
			continue
		}
		if n := len(page.Files); n == 0 || page.Files[n-1].Name != clean(mu.Pos.File) {
			page.Files = append(page.Files, htmlFile{Name: clean(mu.Pos.File)})
		}
		f := &page.Files[len(page.Files)-1]
		f.Mutants = append(f.Mutants, htmlMutant{
			Line: mu.Pos.Line, Column: mu.Pos.Column,
			Operator: clean(mu.Operator), Original: clean(mu.Original), Replacement: clean(mu.Replacement),
			Status: mu.Status.String(), Class: statusClass(mu.Status),
		})
	}

	var sb strings.Builder
	if err := htmlTemplate.Execute(&sb, page); err != nil {
		return fmt.Errorf("report: render: %w", err)
	}
	return write(w, sb.String())
}

// level buckets a score for colour: the thresholds are a convention, not a rule.
func level(score float64) string {
	switch {
	case score >= 80:
		return "good"
	case score >= 60:
		return "fair"
	default:
		return "poor"
	}
}

func statusClass(s mutant.Status) string {
	return strings.ReplaceAll(s.String(), " ", "-")
}

var htmlTemplate = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">
<title>Gomu mutation report</title>
<style>
:root{color-scheme:light dark;--bg:#fff;--fg:#1f2328;--muted:#656d76;--line:#d0d7de;--card:#f6f8fa;
--good:#1a7f37;--fair:#9a6700;--poor:#cf222e;--none:#656d76}
@media(prefers-color-scheme:dark){:root{--bg:#0d1117;--fg:#e6edf3;--muted:#8d96a0;--line:#30363d;--card:#161b22;
--good:#3fb950;--fair:#d29922;--poor:#f85149;--none:#8d96a0}}
body{margin:0;padding:24px 16px;background:var(--bg);color:var(--fg);font:15px/1.5 system-ui,sans-serif}
main{max-width:860px;margin:0 auto}
h1{font-size:18px;margin:0 0 16px;color:var(--muted);font-weight:600}
h2{font-size:15px;margin:28px 0 8px;font-family:ui-monospace,monospace;word-break:break-all}
.score{font-size:56px;font-weight:700;line-height:1}
.good{color:var(--good)}.fair{color:var(--fair)}.poor{color:var(--poor)}.none{color:var(--none)}
.sub{color:var(--muted);margin:4px 0 12px}
progress{width:100%;height:10px;accent-color:currentColor}
.chips{display:flex;flex-wrap:wrap;gap:8px;margin:16px 0 0;padding:0;list-style:none}
.chips li{background:var(--card);border:1px solid var(--line);border-radius:6px;padding:4px 10px}
.chips b{margin-right:4px}
table{width:100%;table-layout:fixed;border-collapse:collapse;background:var(--card);border:1px solid var(--line)}
th,td{text-align:left;padding:6px 10px;border-top:1px solid var(--line)}
th{color:var(--muted);font-weight:600;font-size:13px;border-top:0}
th:first-child{width:80px}th:last-child{width:120px}
code{font-family:ui-monospace,monospace}
.tag{border-radius:4px;padding:1px 8px;font-size:13px;border:1px solid currentColor;white-space:nowrap}
.survived{color:var(--poor)}.killed,.timed-out{color:var(--good)}
.not-covered,.not-viable,.pending{color:var(--muted)}
.empty{margin-top:28px;color:var(--muted)}
</style>
</head>
<body>
<main>
<h1>Gomu mutation report</h1>
{{- if not .HasMutants}}
<p class="empty">No mutants found.</p>
{{- else}}
<div class="score {{.Level}}">{{.Score}}</div>
{{- if .HasScore}}
<p class="sub">{{.Detected}} of {{.Evaluated}} evaluated mutants detected</p>
<progress class="{{.Level}}" max="100" value="{{printf "%.1f" .Percent}}"></progress>
{{- else}}
<p class="sub">no mutants were evaluated</p>
{{- end}}
<ul class="chips">
<li><b class="killed">{{.Summary.Killed}}</b>killed</li>
<li><b class="survived">{{.Summary.Survived}}</b>survived</li>
<li><b class="timed-out">{{.Summary.TimedOut}}</b>timed out</li>
<li><b class="not-covered">{{.Summary.NotCovered}}</b>not covered</li>
<li><b class="not-viable">{{.Summary.NotViable}}</b>not viable</li>
{{- if .Summary.Pending}}
<li><b class="pending">{{.Summary.Pending}}</b>pending</li>
{{- end}}
</ul>
{{- range .Files}}
<h2>{{.Name}}</h2>
<table>
<thead><tr><th>Line</th><th>Operator</th><th>Change</th><th>Status</th></tr></thead>
<tbody>
{{- range .Mutants}}
<tr><td>{{.Line}}:{{.Column}}</td><td>{{.Operator}}</td><td><code>{{.Original}} → {{.Replacement}}</code></td><td><span class="tag {{.Class}}">{{.Status}}</span></td></tr>
{{- end}}
</tbody>
</table>
{{- else}}
<p class="empty">No survived mutants. Run with -v to list every mutant.</p>
{{- end}}
{{- end}}
</main>
</body>
</html>
`))
