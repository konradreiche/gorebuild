package verify

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"strings"
)

func encodeReport(file *os.File, path string, report *Report) error {
	var output io.Writer = file
	var compressed *gzip.Writer
	if strings.HasSuffix(path, ".gz") {
		compressed = gzip.NewWriter(file)
		output = compressed
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		if compressed != nil {
			compressed.Close()
		}
		return err
	}
	if compressed != nil {
		if err := compressed.Close(); err != nil {
			return err
		}
	}
	return file.Sync()
}
