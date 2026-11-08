package presentation

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDurationsAndTerminalWidths(t *testing.T) {
	for _, test := range []struct {
		ns              int64
		duration, delta string
	}{{0, "0.000s", "+0.000s"}, {393000000, "0.393s", "+0.393s"}, {-963000000, "-0.963s", "-0.963s"}, {12104000000, "12.104s", "+12.104s"}} {
		if Duration(test.ns) != test.duration || Delta(test.ns) != test.delta {
			t.Fatal(test, Duration(test.ns), Delta(test.ns))
		}
	}
	for _, width := range []int{80, 120} {
		var out strings.Builder
		Table(&out, width, []string{"Package", "Action", "Duration", "Path share"}, [][]string{{strings.Repeat("very/long/路径/", 16), "compile", "3.000s", "42.3%"}, {"short", "link", "1.000s", "14.1%"}})
		rendered := Fit(out.String(), width)
		for _, line := range strings.Split(rendered, "\n") {
			if utf8.RuneCountInString(line) > width {
				t.Fatalf("%d columns exceeded: %s", width, line)
			}
		}
		if !strings.Contains(rendered, "42.3%") || !strings.Contains(rendered, "short") {
			t.Fatal(rendered)
		}
	}
	t.Setenv("COLUMNS", "120")
	if (Options{}).Columns() != 120 {
		t.Fatal("terminal width ignored")
	}
	t.Setenv("COLUMNS", "invalid")
	if (Options{}).Columns() != 80 {
		t.Fatal("invalid width did not fall back")
	}
	if Optional(nil) != "Unavailable" || Package("example.org/mod/web/api/v1", "example.org/mod") != "web/api/v1" {
		t.Fatal("missing/path formatting")
	}
}
