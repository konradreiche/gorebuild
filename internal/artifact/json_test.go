package artifact

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"testing"
)

func TestPlainAndGzipEvidenceAreIdentical(t *testing.T) {
	value := struct {
		Raw    string `json:"raw_log"`
		Values []int  `json:"all_results"`
	}{Raw: "complete raw evidence\n", Values: []int{1, 2, 3, 4, 5}}
	var plain, compressed bytes.Buffer
	if err := Encode(&plain, "report.json", value); err != nil {
		t.Fatal(err)
	}
	if err := Encode(&compressed, "report.json.gz", value); err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain.Bytes(), actual) {
		t.Fatal("compressed evidence differs")
	}
	for _, name := range []string{"report.json", "report.json.gz"} {
		if err := Encode(brokenWriter{}, name, value); err != nil {
			continue
		}
		t.Fatal("write/close error lost", name)
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("write rejected") }
