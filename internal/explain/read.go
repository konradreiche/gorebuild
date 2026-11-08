// Package explain diagnoses saved history evidence without replaying builds.
package explain

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/konradreiche/gorebuild/internal/history"
	"github.com/konradreiche/gorebuild/internal/profile"
)

// These are read-only projections of the existing history format, not a new
// artifact format. Skip cold graphs/logs and decode events one at a time so a
// 337 MiB capture does not require retaining its entire JSON document in memory.
type Dataset struct {
	SchemaVersion int                     `json:"schema_version"`
	Metadata      history.Metadata        `json:"metadata"`
	Graph         []history.PackageNode   `json:"graph"`
	Packages      []history.PackageImpact `json:"packages"`
	Events        []Event                 `json:"events"`
	Warnings      []string
}

type Event struct {
	Commit           string               `json:"commit"`
	Parents          []string             `json:"parents"`
	Files            []history.FileChange `json:"files"`
	PackageDirs      []string             `json:"package_dirs"`
	BuildPackageDirs []string             `json:"build_package_dirs"`
	Replay           *Replay              `json:"replay"`
	NoVCSReplay      *Replay              `json:"no_vcs_replay"`
}

type Replay struct {
	Status           string            `json:"status"`
	Stage            string            `json:"stage"`
	Reason           string            `json:"reason"`
	BuildFlags       []string          `json:"build_flags"`
	WorkingDirectory string            `json:"working_directory"`
	PackageImports   map[string]string `json:"package_imports"`
	Warm             *struct {
		Run struct {
			WallTimeNS int64 `json:"wall_time_ns"`
		} `json:"run"`
	} `json:"warm"`
	Rebuild *struct {
		Run struct {
			WallTimeNS int64 `json:"wall_time_ns"`
			Graph      struct {
				ActionGraph []profile.Action `json:"action_graph"`
			} `json:"graph"`
		} `json:"run"`
		ActionGraph json.RawMessage `json:"action_graph"`
		Failure     string          `json:"failure"`
	} `json:"rebuild"`
	Graph   profile.GraphSummary `json:"-"`
	Problem string               `json:"-"`
}

func (replay *Replay) Measured() bool {
	return replay != nil && replay.Status == "measured" && replay.Rebuild != nil && replay.Problem == "" && replay.Rebuild.Failure == ""
}

func Read(path string) (*Dataset, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	buffer := bufio.NewReader(file)
	var source io.Reader = buffer
	if header, _ := buffer.Peek(2); len(header) == 2 && header[0] == 0x1f && header[1] == 0x8b {
		compressed, err := gzip.NewReader(buffer)
		if err != nil {
			return nil, err
		}
		defer compressed.Close()
		source = compressed
	}
	decoder := json.NewDecoder(source)
	opening, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	if opening != json.Delim('{') {
		return nil, fmt.Errorf("expected a history JSON object")
	}
	data := &Dataset{}
	seen := make(map[string]bool)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return nil, fmt.Errorf("invalid or duplicate history field %q", key)
		}
		seen[name] = true
		switch name {
		case "schema_version":
			err = decoder.Decode(&data.SchemaVersion)
		case "metadata":
			err = decoder.Decode(&data.Metadata)
		case "graph":
			err = decoder.Decode(&data.Graph)
		case "packages":
			err = decoder.Decode(&data.Packages)
		case "events":
			err = readEvents(decoder, data)
		default:
			var ignored json.RawMessage
			err = decoder.Decode(&ignored)
		}
		if err != nil {
			return nil, fmt.Errorf("history field %s: %w", name, err)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != nil {
		if err != io.EOF {
			return nil, fmt.Errorf("history trailer: %w", err)
		}
	} else {
		return nil, fmt.Errorf("unexpected trailing JSON after history")
	}
	if data.SchemaVersion < 1 || data.SchemaVersion > 4 {
		return nil, fmt.Errorf("unsupported history schema version %d", data.SchemaVersion)
	}
	if !seen["events"] || data.Metadata.ImportPath == "" {
		return nil, fmt.Errorf("incomplete history artifact: target metadata and events are required")
	}
	return data, nil
}

func readEvents(decoder *json.Decoder, data *Dataset) error {
	opening, err := decoder.Token()
	if err != nil {
		return err
	}
	if opening == nil {
		return nil
	}
	if opening != json.Delim('[') {
		return fmt.Errorf("events must be an array")
	}
	seen := make(map[string]bool)
	for decoder.More() {
		var event Event
		if err := decoder.Decode(&event); err != nil {
			return err
		}
		if event.Commit == "" || seen[event.Commit] {
			return fmt.Errorf("empty or duplicate commit %q", event.Commit)
		}
		seen[event.Commit] = true
		for _, replay := range []*Replay{event.Replay, event.NoVCSReplay} {
			if replay == nil || replay.Status != "measured" {
				continue
			}
			if err := analyzeReplay(replay); err != nil {
				replay.Problem = err.Error()
				data.Warnings = append(data.Warnings, fmt.Sprintf("%s: %s", event.Commit, err))
			}
		}
		data.Events = append(data.Events, event)
	}
	_, err = decoder.Token()
	return err
}

func analyzeReplay(replay *Replay) error {
	if replay.Rebuild == nil {
		return fmt.Errorf("successful replay has no rebuild capture")
	}
	if replay.Rebuild.Failure != "" {
		return fmt.Errorf("successful replay contains a failure: %s", replay.Rebuild.Failure)
	}
	actions := replay.Rebuild.Run.Graph.ActionGraph
	if len(replay.Rebuild.ActionGraph) > 0 && string(replay.Rebuild.ActionGraph) != "null" {
		if err := json.Unmarshal(replay.Rebuild.ActionGraph, &actions); err != nil {
			return fmt.Errorf("invalid raw rebuild graph: %w", err)
		}
	}
	if len(actions) == 0 {
		return fmt.Errorf("successful replay has no action graph")
	}
	graph, err := profile.Analyze(actions)
	if err != nil {
		return err
	}
	replay.Graph = graph
	// Keep one canonical graph, not duplicate parsed/raw copies.
	replay.Rebuild.ActionGraph = nil
	replay.Rebuild.Run.Graph.ActionGraph = nil
	return nil
}
