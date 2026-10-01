package main

import "testing"

func TestFingerprint(t *testing.T) {
	if got := fingerprint(""); got != "MISSING" {
		t.Fatalf("empty secret: got %q", got)
	}
	if got := fingerprint("s3cret"); len(got) != 12 || got == "MISSING" {
		t.Fatalf("fingerprint: got %q", got)
	}
}
