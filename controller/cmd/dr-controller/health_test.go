package main

import (
	"testing"
	"time"
)

func TestDecide(t *testing.T) {
	cfg := Config{QueueThreshold: time.Minute, FailbackCooldown: 2 * time.Minute,
		FailoverOn: parseSet("partial_outage,major_outage")}
	now := time.Now()
	healthy := Signals{StatusActions: "operational"}

	cases := []struct {
		name         string
		mode         string
		current      Path
		sig          Signals
		healthySince time.Time
		want         Path
	}{
		{"healthy stays on GHA", "auto", PathGHA, healthy, now.Add(-time.Hour), PathGHA},
		{"status page outage fails over", "auto", PathGHA, Signals{StatusActions: "major_outage"}, now, PathTekton},
		{"degraded alone does not fail over", "auto", PathGHA, Signals{StatusActions: "degraded_performance"}, now, PathGHA},
		{"stuck queue fails over before status page", "auto", PathGHA,
			Signals{StatusActions: "operational", QueuedRuns: 1, OldestQueued: 90 * time.Second}, now, PathTekton},
		{"short queue is normal scale-up", "auto", PathGHA,
			Signals{StatusActions: "operational", QueuedRuns: 1, OldestQueued: 20 * time.Second}, now, PathGHA},
		{"recovery waits for cooldown", "auto", PathTekton, healthy, now.Add(-30 * time.Second), PathTekton},
		{"fails back after cooldown", "auto", PathTekton, healthy, now.Add(-3 * time.Minute), PathGHA},
		{"manual tekton overrides healthy", "tekton", PathGHA, healthy, now, PathTekton},
		{"manual gha overrides outage", "gha", PathTekton, Signals{StatusActions: "major_outage"}, now, PathGHA},
		{"unreachable status page is not an outage", "auto", PathGHA, Signals{StatusErr: "timeout"}, now, PathGHA},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decide(cfg, tc.mode, tc.current, tc.sig, tc.healthySince, now); got.Path != tc.want {
				t.Fatalf("got %s (%v), want %s", got.Path, got.Reasons, tc.want)
			}
		})
	}
}

func TestValidSignature(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	// echo -n '{"ref":"refs/heads/main"}' | openssl dgst -sha256 -hmac s3cret
	good := "sha256=232a5e067b6a5aa567ed5a1d21a0dffaf6ef0eeecb6bd9bcf43b3d5ef3fc4f43"
	if !validSignature("s3cret", body, good) {
		t.Fatal("rejected a valid signature")
	}
	if validSignature("s3cret", []byte(`{"ref":"refs/heads/evil"}`), good) {
		t.Fatal("accepted a signature for a different body")
	}
	if validSignature("s3cret", body, "") {
		t.Fatal("accepted a missing signature")
	}
}
