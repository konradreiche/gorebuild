// Package artifact encodes complete evidence without changing its schema.
package artifact

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// Encode leaves ownership of output with the caller. A gzip stream must be
// closed (including its checksum) before the caller commits the output file.
func Encode(output io.Writer, name string, value any) error {
	var compressed *gzip.Writer
	if strings.HasSuffix(name, ".gz") {
		compressed = gzip.NewWriter(output)
		output = compressed
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		if compressed != nil {
			return errors.Join(err, compressed.Close())
		}
		return err
	}
	if compressed != nil {
		return compressed.Close()
	}
	return nil
}
