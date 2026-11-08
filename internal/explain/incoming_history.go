package explain

import (
	"context"
	"go/token"
	"path/filepath"
	"strings"
)

func incomingGroupHistory(ctx context.Context, data *Dataset, root, targetDir string, source *checkedSource, fset *token.FileSet, dependency *IncomingDependency, boundary *IncomingBoundary, cache map[string]map[string]string) error {
	files := make(map[string]bool)
	for _, name := range boundary.Declarations {
		declaration := source.declarations[name]
		if declaration == nil {
			continue
		}
		relative, err := filepath.Rel(root, fset.Position(declaration.node.Pos()).Filename)
		if err != nil {
			return err
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			boundary.Constraints = append(boundary.Constraints, "generated source outside checkout prevents Git declaration attribution")
			return nil
		}
		files[filepath.ToSlash(relative)] = true
	}
	if len(files) == 0 {
		return nil
	}
	boundary.HistoryAnalyzed = true
	for _, evidence := range dependency.Events {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !evidence.DependencySourceChanged {
			continue
		}
		for _, event := range data.Events {
			if event.Commit != evidence.Commit {
				continue
			}
			if len(event.Parents) == 0 {
				boundary.HistoryUnknown++
				break
			}
			group := CandidateEvent{}
			compareHistoricalGroup(ctx, root, event.Parents[0], event.Commit, files, cache, boundary.Declarations, &group)
			if group.Unknown {
				boundary.HistoryUnknown++
			} else if group.GroupChanged {
				boundary.DeclarationChanges++
			} else {
				boundary.DeclarationUnchanged++
			}
			break
		}
	}
	return nil
}
