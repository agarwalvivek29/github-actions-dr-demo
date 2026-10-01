package main

// Failure detection. Two independent signals, because they fail differently:
//
//  1. Status page (githubstatus.com, Statuspage v2 schema) — authoritative but
//     lagging: incidents are often declared well after jobs start queueing.
//  2. Queue probe — first-party and leading: how long has our own release
//     workflow been sitting in "queued" without any runner picking it up?
//
// Either one tripping moves releases to Tekton (in auto mode).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Signals struct {
	CheckedAt      time.Time     `json:"checked_at"`
	StatusActions  string        `json:"status_actions"`
	StatusWebhooks string        `json:"status_webhooks"`
	StatusErr      string        `json:"status_error,omitempty"`
	QueuedRuns     int           `json:"queued_runs"`
	OldestQueued   time.Duration `json:"oldest_queued_ns"`
	OldestQueuedBy string        `json:"oldest_queued_run,omitempty"`
	QueueErr       string        `json:"queue_error,omitempty"`
}

var statusClient = &http.Client{Timeout: 5 * time.Second}

// fetchStatus reads component statuses from a Statuspage v2 components.json.
// Pointing STATUS_URL at the fake status page is the only thing that changes
// in a drill; the parsing and decision code are identical to production.
func fetchStatus(url string) (actions, webhooks string, err error) {
	resp, err := statusClient.Get(url)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("status page: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Components []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"components"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", err
	}
	for _, c := range body.Components {
		switch c.Name {
		case "Actions":
			actions = c.Status
		case "Webhooks":
			webhooks = c.Status
		}
	}
	return actions, webhooks, nil
}

func (c *Controller) probe() Signals {
	s := Signals{CheckedAt: time.Now()}
	a, w, err := fetchStatus(c.cfg.StatusURL)
	if err != nil {
		// An unreachable status page is not evidence of an outage.
		s.StatusErr = err.Error()
	}
	s.StatusActions, s.StatusWebhooks = a, w

	runs, err := c.gh.QueuedRuns(c.cfg.Workflow, c.cfg.Branch)
	if err != nil {
		s.QueueErr = err.Error()
	}
	s.QueuedRuns = len(runs)
	for _, r := range runs {
		if age := time.Since(r.CreatedAt); age > s.OldestQueued {
			s.OldestQueued, s.OldestQueuedBy = age, r.HTMLURL
		}
	}
	return s
}

type Path string

const (
	PathGHA    Path = "github-actions"
	PathTekton Path = "tekton"
)

type Decision struct {
	Path    Path
	Reasons []string
}

// decide is the whole failover policy, kept pure so it can be unit tested
// and read on a slide.
func decide(cfg Config, mode string, current Path, s Signals, healthySince time.Time, now time.Time) Decision {
	switch mode {
	case "tekton":
		return Decision{PathTekton, []string{"manual switch: mode=tekton"}}
	case "gha", "github-actions":
		return Decision{PathGHA, []string{"manual switch: mode=gha"}}
	}

	var down []string
	if cfg.FailoverOn[s.StatusActions] {
		down = append(down, "status page: Actions="+s.StatusActions)
	}
	if s.OldestQueued >= cfg.QueueThreshold {
		down = append(down, fmt.Sprintf("queue probe: %d run(s) queued, oldest %s (threshold %s)",
			s.QueuedRuns, s.OldestQueued.Round(time.Second), cfg.QueueThreshold))
	}
	if len(down) > 0 {
		return Decision{PathTekton, down}
	}
	if current == PathTekton {
		if wait := cfg.FailbackCooldown - now.Sub(healthySince); wait > 0 {
			return Decision{PathTekton, []string{fmt.Sprintf("Actions healthy again — failback in %s", wait.Round(time.Second))}}
		}
		return Decision{PathGHA, []string{"Actions healthy for " + cfg.FailbackCooldown.String() + " — failed back"}}
	}
	return Decision{PathGHA, []string{"Actions healthy"}}
}

func (s Signals) actionsDown(cfg Config) bool {
	return cfg.FailoverOn[s.StatusActions] || s.OldestQueued >= cfg.QueueThreshold
}

func (s Signals) webhooksDown(cfg Config) bool { return cfg.FailoverOn[s.StatusWebhooks] }

func parseSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			m[v] = true
		}
	}
	return m
}
