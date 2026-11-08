package history

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSamplingAvoidsRepeatedCoverageWhenAlternativesExist(t *testing.T) {
	report := &Report{Packages: []PackageImpact{
		{Dir: "a", InTarget: true, BuildChanges: 4, ReverseDependencies: []string{"consumer"}},
		{Dir: "b", InTarget: true, BuildChanges: 1},
		{Dir: "c", InTarget: true, BuildChanges: 1},
	}}
	for index, dir := range []string{"a", "a", "a", "b", "c", "a"} {
		stamp := int64(1000 - index)
		if index == 5 {
			stamp = 0
		}
		report.Events = append(report.Events, Event{TimeUTC: time.Unix(stamp, 0), PackageDirs: []string{dir}, BuildPackageDirs: []string{dir}, Files: []FileChange{{Classification: "go_source"}}})
	}
	selected := sampleEvents(report, 4)
	var indices []int
	for _, selection := range selected {
		indices = append(indices, selection.Index)
	}
	if !slices.Equal(indices, []int{0, 3, 4, 5}) {
		t.Fatal("near-identical coverage displaced alternatives", selected)
	}
	fallback := sampleEvents(report, 5)
	var found bool
	for _, selection := range fallback {
		for _, reason := range selection.Reasons {
			if strings.HasPrefix(reason, "coverage fallback:") {
				found = true
			}
		}
	}
	if len(fallback) != 5 || !found {
		t.Fatal("repeated coverage fallback must be explicit", fallback)
	}
}

func TestSamplingBalancesActivityReachCoChangesAndTime(t *testing.T) {
	report := &Report{Packages: []PackageImpact{
		{Dir: "frequent", InTarget: true, BuildChanges: 6},
		{Dir: "broad", InTarget: true, BuildChanges: 1, ReverseDependencies: []string{"a", "b", "c"}},
		{Dir: "c", InTarget: true, BuildChanges: 1},
		{Dir: "d", InTarget: true, BuildChanges: 1},
	}}
	for index := range 12 {
		event := Event{Commit: fmt.Sprint(index), TimeUTC: time.Unix(int64(1000-index), 0).UTC()}
		switch index {
		case 2, 5, 6, 7, 8, 10:
			event.PackageDirs = []string{"frequent"}
			event.BuildPackageDirs = []string{"frequent"}
		case 3:
			event.PackageDirs = []string{"broad"}
			event.BuildPackageDirs = []string{"broad"}
		case 4:
			event.PackageDirs = []string{"c", "d"}
			event.BuildPackageDirs = []string{"c", "d"}
		}
		report.Events = append(report.Events, event)
	}
	selected := sampleEvents(report, 6)
	var indices []int
	reasons := make(map[string]bool)
	for _, selection := range selected {
		indices = append(indices, selection.Index)
		for _, reason := range selection.Reasons {
			criterion, _, _ := strings.Cut(reason, ":")
			reasons[criterion] = true
		}
	}
	if !slices.Equal(indices, []int{0, 2, 3, 4, 7, 11}) || !reasons["time"] || !reasons["frequency"] || !reasons["reach"] || !reasons["multi-package"] {
		t.Fatalf("missing representative criteria: %v %+v", indices, selected)
	}
	// No dependence on map/package iteration order or on previous selections.
	slices.Reverse(report.Packages)
	if !reflect.DeepEqual(sampleEvents(report, 6), selected) {
		t.Fatal("sample selection is not reproducible")
	}
	all := sampleEvents(report, 100)
	if len(all) != len(report.Events) {
		t.Fatal("eligible events lost after queues exhausted")
	}
	for index, selection := range all {
		if selection.Index != index || len(selection.Reasons) == 0 {
			t.Fatal("event duplicated or reason missing", all)
		}
	}
	report.FocusPackage = "frequent"
	for _, selection := range sampleEvents(report, 6) {
		if !slices.Contains(report.Events[selection.Index].PackageDirs, "frequent") {
			t.Fatal("focus filter ignored")
		}
	}
}
