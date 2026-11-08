package history

import "context"

// InspectBuildGraph reuses history's read-only graph discovery for controlled
// revision verification. The caller supplies an isolated metadata cache and env.
func InspectBuildGraph(ctx context.Context, cwd, cache string, options Options, env []string) ([]PackageNode, string, error) {
	nodes, target, err := loadGraphWithEnv(ctx, cwd, cache, options, env)
	if err != nil {
		return nil, "", err
	}
	if _, err := analyzeGraph(nodes); err != nil {
		return nil, "", err
	}
	return nodes, target, nil
}
