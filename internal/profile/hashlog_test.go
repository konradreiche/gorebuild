package profile

import (
	"bytes"
	"strings"
	"testing"
)

func TestHashLogFiltersStreamingSelectedBlocks(t *testing.T) {
	var out bytes.Buffer
	log := &hashLog{output: &out, packages: []string{"example/p"}}
	input := "HASH[build example/p]: \"file p.go abc\\n\"\nHASH[build example/private]: \"discard\\n\"\nHASH[moduleIndex]: \"large index\\n\"\nHASH[link example/p]: deadbeef\ncompiler error: keep\nunfinished error"
	for _, part := range []string{input[:11], input[11:49], input[49:]} {
		if _, err := log.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.flush(); err != nil {
		t.Fatal(err)
	}
	result := out.String()
	if strings.Contains(result, "discard") || strings.Contains(result, "large index") || !strings.Contains(result, "file p.go") || !strings.Contains(result, "deadbeef") || !strings.HasSuffix(result, "unfinished error") {
		t.Fatal(result)
	}
}
