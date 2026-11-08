package verify

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func copyCacheFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}

func (runner *engine) runtimeCheck(ctx context.Context, work *tree, args []string) *CommandCheck {
	binary := filepath.Join(filepath.Dir(work.path), "binary")
	check := &CommandCheck{Arguments: append([]string{binary}, args...)}
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = filepath.Join(work.path, runner.cwd)
	var debug string
	for _, entry := range runner.env {
		if strings.HasPrefix(entry, "GODEBUG=") {
			debug = strings.TrimPrefix(entry, "GODEBUG=")
			continue
		}
		command.Env = append(command.Env, entry)
	}
	debug += ",inittrace=1"
	command.Env = append(command.Env, "GODEBUG="+debug)
	check.Environment = map[string]string{"GODEBUG": debug}
	started := time.Now()
	output, err := command.CombinedOutput()
	check.WallTimeNS = int64(time.Since(started))
	check.Log = string(output)
	if err != nil {
		check.Failure = err.Error()
	}
	return check
}
