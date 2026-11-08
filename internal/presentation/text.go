// Package presentation contains terminal-only formatting. It does not analyze builds.
package presentation

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Options struct {
	Details bool
	Width   int
	Top     int
}

func (o Options) Columns() int {
	if o.Width >= 40 {
		return o.Width
	}
	columns, err := strconv.Atoi(os.Getenv("COLUMNS"))
	if err != nil {
		return 80
	}
	if columns >= 40 {
		return columns
	}
	return 80
}
func (o Options) Limit() int {
	if o.Top > 0 {
		return o.Top
	}
	return 5
}
func Duration(ns int64) string { return fmt.Sprintf("%.3fs", float64(ns)/1e9) }
func Delta(ns int64) string    { return fmt.Sprintf("%+.3fs", float64(ns)/1e9) }
func Optional(ns *int64) string {
	if ns == nil {
		return "Unavailable"
	}
	return fmt.Sprintf("%.3fs", float64(*ns)/1e9)
}
func Package(name, module string) string {
	if module != "" {
		name = strings.TrimPrefix(name, module+"/")
	}
	return strings.TrimPrefix(name, "./")
}
func Section(out *strings.Builder, title string) { fmt.Fprintf(out, "\n%s:\n", title) }
func Hint(out *strings.Builder) {
	fmt.Fprintln(out, "\nNext: use --details for evidence and limitations; --out for full JSON.")
}

// Fit preserves explicit lines/indentation and wraps long words (including paths)
// rather than silently truncating identifying evidence.
func Fit(text string, width int) string {
	if width < 40 {
		width = 80
	}
	var out strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent > width/2 {
			indent = 2
		}
		lines := wrap(strings.TrimLeft(line, " "), width-indent)
		for _, part := range lines {
			out.WriteString(strings.Repeat(" ", indent))
			out.WriteString(part)
			out.WriteByte('\n')
		}
	}
	return out.String()
}
func wrap(text string, width int) []string {
	if width < 1 {
		width = 1
	}
	var lines []string
	for utf8.RuneCountInString(text) > width {
		runes := []rune(text)
		cut := width
		if space := strings.LastIndex(string(runes[:width]), " "); space > width/2 {
			cut = utf8.RuneCountInString(string(runes[:width])[:space])
		}
		lines = append(lines, string(runes[:cut]))
		text = strings.TrimLeft(string(runes[cut:]), " ")
	}
	return append(lines, text)
}

// Table wraps cells at the chosen terminal width. Values are never discarded.
func Table(out *strings.Builder, width int, headers []string, rows [][]string) {
	if len(headers) == 0 {
		return
	}
	width = max(width, 40) - 2
	sizes := make([]int, len(headers))
	all := append([][]string{headers}, rows...)
	for _, row := range all {
		for i, cell := range row {
			if i < len(sizes) {
				sizes[i] = max(sizes[i], utf8.RuneCountInString(cell))
			}
		}
	}
	total := func() int {
		n := 2 * (len(sizes) - 1)
		for _, size := range sizes {
			n += size
		}
		return n
	}
	for total() > width {
		largest := -1
		for i, size := range sizes {
			if size > 10 && (largest < 0 || size > sizes[largest]) {
				largest = i
			}
		}
		if largest < 0 {
			break
		}
		sizes[largest]--
	}
	for _, row := range all {
		cells := make([][]string, len(sizes))
		height := 1
		for column := range sizes {
			var value string
			if column < len(row) {
				value = row[column]
			}
			cells[column] = wrap(value, sizes[column])
			height = max(height, len(cells[column]))
		}
		for line := range height {
			var value strings.Builder
			value.WriteString("  ")
			for column := range sizes {
				var cell string
				if line < len(cells[column]) {
					cell = cells[column][line]
				}
				value.WriteString(cell)
				if column < len(sizes)-1 {
					value.WriteString(strings.Repeat(" ", sizes[column]-utf8.RuneCountInString(cell)+2))
				}
			}
			out.WriteString(strings.TrimRight(value.String(), " "))
			out.WriteByte('\n')
		}
	}
}
