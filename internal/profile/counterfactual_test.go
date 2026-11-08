package profile

import (
	"testing"
	"time"
)

func TestCounterfactualAccountsForCompetingPaths(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	action := func(id int, mode string, duration time.Duration, deps ...int) Action {
		return Action{ID: id, Mode: mode, Deps: deps, Cmd: []string{mode}, TimeStart: start, TimeDone: start.Add(duration)}
	}
	actions := []Action{action(0, "link", 5*time.Second, 1, 2, 3), action(1, "build", 75*time.Second), action(2, "build", 70*time.Second), {ID: 3, NeedBuild: true, TimeStart: start, TimeDone: start.Add(100 * time.Second)}}
	for _, test := range []struct {
		id              int
		without, saving time.Duration
	}{{1, 75 * time.Second, 5 * time.Second}, {2, 80 * time.Second, 0}, {0, 75 * time.Second, 5 * time.Second}} {
		result, err := EstimateCriticalPathSaving(actions, test.id)
		if err != nil {
			t.Fatal(err)
		}
		if result.CurrentNS != int64(80*time.Second) || result.WithoutNS != int64(test.without) || result.MaximumSavingNS != int64(test.saving) {
			t.Fatal("competing path ignored", result)
		}
	}
	if actions[1].Elapsed() != 75*time.Second {
		t.Fatal("counterfactual mutated observed action duration")
	}
	for _, id := range []int{3, 99} {
		if _, err := EstimateCriticalPathSaving(actions, id); err != nil {
			continue
		}
		t.Fatal("nonexecuted/missing action accepted")
	}
	if _, err := EstimateCriticalPathSaving([]Action{action(1, "build", time.Second, 1)}, 1); err != nil {
		return
	}
	t.Fatal("cyclic graph accepted")
}
