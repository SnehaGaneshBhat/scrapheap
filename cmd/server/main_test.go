package main

import (
	"testing"

	"scrapheap/internal/sched"
)

func TestMatchesBurnResult(t *testing.T) {
	spec := sched.TaskSpec{Kind: "burn"}
	if !matchesBurnResult(spec, []byte(`{"checksum":"123"}`), []byte(`{ "checksum" : "123" }`)) {
		t.Fatal("equal checksums with different JSON whitespace did not match")
	}
	if matchesBurnResult(spec, []byte(`{"checksum":"123"}`), []byte(`{"checksum":"124"}`)) {
		t.Fatal("different checksums unexpectedly matched")
	}
	if matchesBurnResult(spec, []byte(`not-json`), []byte(`{"checksum":"123"}`)) {
		t.Fatal("invalid burn result unexpectedly matched")
	}
}
