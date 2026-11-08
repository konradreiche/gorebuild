package history

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ParseSince accepts explicit UTC dates/timestamps or a number of days. Resolve
// it once so all Git commands in a report refer to the same time window.
func ParseSince(value string, now time.Time) (time.Time, error) {
	if before, ok := strings.CutSuffix(value, "d"); ok {
		days, err := strconv.Atoi(before)
		if err != nil || days <= 0 || days > 36500 {
			return time.Time{}, fmt.Errorf("invalid --since %q: days must be between 1 and 36500", value)
		}
		return now.UTC().AddDate(0, 0, -days), nil
	}
	for _, layout := range []string{time.RFC3339, time.DateOnly} {
		parsed, err := time.Parse(layout, value)
		if err != nil {
			continue
		}
		return parsed.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("invalid --since %q: use e.g. 90d, 2025-01-01, or an RFC3339 timestamp", value)
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return output, nil
}

func readEvents(ctx context.Context, root, head string, since time.Time, limit int) ([]Event, bool, error) {
	args := []string{"log", "--first-parent", "--format=%H%x09%P%x09%ct", "--since-as-filter=" + since.Format(time.RFC3339)}
	if limit > 0 {
		args = append(args, "--max-count="+strconv.FormatUint(uint64(limit)+1, 10))
	}
	args = append(args, head, "--")
	output, err := git(ctx, root, args...)
	if err != nil {
		return nil, false, err
	}
	var events []Event
	var truncated bool
	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		if limit > 0 && len(events) == limit {
			truncated = true
			break
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return nil, false, fmt.Errorf("unexpected git log record %q", line)
		}
		epoch, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return nil, false, fmt.Errorf("invalid commit timestamp: %w", err)
		}
		event := Event{Commit: fields[0], Parents: strings.Fields(fields[1]), TimeUTC: time.Unix(epoch, 0).UTC()}
		args := []string{"diff-tree", "--root", "--no-commit-id", "-r", "--name-only", "-z", "--no-renames", event.Commit, "--"}
		if len(event.Parents) > 0 {
			args = []string{"diff", "--name-only", "-z", "--no-renames", event.Parents[0], event.Commit, "--"}
		}
		files, err := git(ctx, root, args...)
		if err != nil {
			return nil, false, fmt.Errorf("read change event %s: %w", event.Commit, err)
		}
		for file := range strings.SplitSeq(string(files), "\x00") {
			if file != "" {
				event.Files = append(event.Files, FileChange{Path: file})
			}
		}
		events = append(events, event)
	}
	return events, truncated, nil
}
