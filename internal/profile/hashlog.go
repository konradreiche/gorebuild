package profile

import (
	"bytes"
	"io"
	"strings"
)

// hashLog keeps only selected Go cache-hash blocks and ordinary build errors.
// Filtering happens while the process runs, not after writing an enormous log.
// Go's diagnostic format is internal; unknown HASH lines are discarded rather
// than misattributed. A changed format is reported as missing evidence upstream.
type hashLog struct {
	output   io.Writer
	packages []string
	pending  []byte
}

func (log *hashLog) Write(data []byte) (int, error) {
	size := len(data)
	for len(data) > 0 {
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			log.pending = append(log.pending, data...)
			break
		}
		log.pending = append(log.pending, data[:end+1]...)
		if err := log.line(); err != nil {
			return 0, err
		}
		data = data[end+1:]
	}
	// Bound a malformed/no-newline diagnostic without silently calling it complete.
	if len(log.pending) > 1024*1024 {
		log.pending = []byte("diagnostic line exceeded 1 MiB; cache-hash evidence incomplete\n")
		if err := log.line(); err != nil {
			return 0, err
		}
	}
	return size, nil
}

func (log *hashLog) line() error {
	line := string(log.pending)
	keep := !strings.HasPrefix(line, "HASH")
	for _, pkg := range log.packages {
		if strings.HasPrefix(line, "HASH[build "+pkg+"]") || strings.HasPrefix(line, "HASH[link "+pkg+"]") {
			keep = true
			break
		}
	}
	log.pending = nil
	if !keep {
		return nil
	}
	_, err := io.WriteString(log.output, line)
	return err
}

func (log *hashLog) flush() error {
	if len(log.pending) == 0 {
		return nil
	}
	return log.line()
}
