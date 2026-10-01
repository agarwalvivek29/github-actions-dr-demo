package main

// Starting and tracking Tekton releases. Webhook, poller, failover and the
// manual button all funnel into requestLocked, which makes triggering
// idempotent (one PipelineRun per sha) and serial (latest-wins queue, the
// same semantics as the GHA workflow's concurrency group).

import (
	"fmt"
	"log"
	"time"
)

const shaLabel = "dr.demo/git-sha"

// requestLocked asks for sha to be released on Tekton. c.mu must be held.
func (c *Controller) requestLocked(sha, source string) {
	switch {
	case sha == c.liveSHA:
		c.event("skip", fmt.Sprintf("%s already live (shipped by %s)", short(sha), c.liveBy))
		return
	case sha == c.runningSHA || sha == c.pendingSHA:
		return
	}
	if source != "manual" {
		existing, err := c.kube.ListPipelineRuns(c.cfg.TektonNamespace, shaLabel+"="+sha)
		if err != nil {
			log.Printf("list pipelineruns: %v", err)
			return
		}
		if len(existing) > 0 {
			return
		}
	}
	if c.running != nil {
		c.pendingSHA, c.pendingSrc = sha, source
		c.event("queue", fmt.Sprintf("%s (%s) waits for %s to finish", short(sha), source, short(c.runningSHA)))
		return
	}
	if err := c.startLocked(sha, source); err != nil {
		c.event("error", fmt.Sprintf("start release %s: %v", short(sha), err))
	}
}

func (c *Controller) startLocked(sha, source string) error {
	// The installation token doubles as the clone credential. In GHA this was
	// the implicit GITHUB_TOKEN; on the DR path we have to bring our own.
	token, err := c.gh.Token()
	if err != nil {
		return err
	}
	name := fmt.Sprintf("release-%s-%d", short(sha), time.Now().Unix()%100000)
	secret := "git-auth-" + name
	ns := c.cfg.TektonNamespace
	if err := c.kube.CreateSecret(ns, obj{
		"metadata":   obj{"name": secret, "labels": obj{shaLabel: sha}},
		"stringData": obj{"token": token},
	}); err != nil {
		return err
	}
	pr, err := c.kube.CreatePipelineRun(ns, obj{
		"apiVersion": "tekton.dev/v1",
		"kind":       "PipelineRun",
		"metadata": obj{"name": name, "labels": obj{
			shaLabel: sha, "dr.demo/trigger": source, "app.kubernetes.io/managed-by": "dr-controller",
		}},
		"spec": obj{
			"pipelineRef": obj{"name": c.cfg.Pipeline},
			"params": []obj{
				{"name": "repo-url", "value": "https://github.com/" + c.cfg.Repo + ".git"},
				{"name": "revision", "value": sha},
			},
			"taskRunTemplate": obj{"serviceAccountName": c.cfg.ServiceAccount},
			"workspaces": []obj{
				{"name": "source", "volumeClaimTemplate": obj{"spec": obj{
					"accessModes": []string{"ReadWriteOnce"},
					"resources":   obj{"requests": obj{"storage": "1Gi"}},
				}}},
				{"name": "git-credentials", "secret": obj{"secretName": secret}},
			},
		},
	})
	if err != nil {
		return err
	}
	// Garbage-collect the token secret together with its PipelineRun.
	_ = c.kube.PatchSecret(ns, secret, obj{"metadata": obj{"ownerReferences": []obj{{
		"apiVersion": "tekton.dev/v1", "kind": "PipelineRun", "name": pr.Metadata.Name, "uid": pr.Metadata.UID,
	}}}})

	c.running, c.runningSHA = &pr, sha
	c.event("release", fmt.Sprintf("PipelineRun %s for %s (trigger: %s)", name, short(sha), source))
	go c.status(sha, "pending", "Tekton release running (GitHub Actions failover)", name)
	return nil
}

// watchTick follows the running PipelineRun and starts the queued one after it.
func (c *Controller) watchTick() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running == nil {
		return
	}
	pr, err := c.kube.GetPipelineRun(c.cfg.TektonNamespace, c.running.Metadata.Name)
	if err != nil {
		log.Printf("get pipelinerun: %v", err)
		return
	}
	done, ok, reason := pr.Done()
	if !done {
		return
	}
	took := ""
	if pr.Status.StartTime != nil && pr.Status.CompletionTime != nil {
		took = " in " + pr.Status.CompletionTime.Sub(*pr.Status.StartTime).Round(time.Second).String()
	}
	state, kind := "success", "shipped"
	if !ok {
		state, kind = "failure", "failed"
	}
	c.event(kind, fmt.Sprintf("%s %s%s (%s)", pr.Metadata.Name, short(c.runningSHA), took, reason))
	go c.status(c.runningSHA, state, "Tekton release "+reason+took, pr.Metadata.Name)

	c.running, c.runningSHA = nil, ""
	if next, src := c.pendingSHA, c.pendingSrc; next != "" {
		c.pendingSHA, c.pendingSrc = "", ""
		c.liveSHA, c.liveBy = c.liveVersion()
		c.requestLocked(next, src)
	}
}

func (c *Controller) status(sha, state, desc, run string) {
	url := fmt.Sprintf("%s/#/namespaces/%s/pipelineruns/%s", c.cfg.DashboardURL, c.cfg.TektonNamespace, run)
	if err := c.gh.SetStatus(sha, state, desc, url); err != nil {
		log.Printf("commit status %s: %v", short(sha), err)
	}
}
