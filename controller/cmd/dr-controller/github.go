package main

// GitHub App client: JWT -> installation token -> REST calls.
//
// Everything the DR path needs from GitHub goes through the REST API
// (commits, workflow runs, commit statuses). During an Actions or Webhooks
// incident the API is usually still up, which is what makes polling a
// viable fallback trigger.

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type GitHub struct {
	API   string
	AppID string
	Repo  string // owner/name
	key   *rsa.PrivateKey
	http  *http.Client

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func NewGitHub(api, appID, keyPath, repo string) (*GitHub, error) {
	pemBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("github app key: no PEM block")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = k
	} else if k8, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		var ok bool
		if key, ok = k8.(*rsa.PrivateKey); !ok {
			return nil, errors.New("github app key: not RSA")
		}
	} else {
		return nil, fmt.Errorf("github app key: %w", err)
	}
	return &GitHub{API: strings.TrimRight(api, "/"), AppID: appID, Repo: repo, key: key,
		http: &http.Client{Timeout: 15 * time.Second}}, nil
}

// appJWT authenticates as the App itself (only used to mint installation tokens).
func (g *GitHub) appJWT() (string, error) {
	enc := base64.RawURLEncoding
	now := time.Now()
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(), // tolerate clock drift
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": g.AppID,
	})
	signing := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, g.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

// Token returns a cached installation token for the repo, refreshing it
// before expiry. The same token is handed to Tekton as the clone credential.
func (g *GitHub) Token() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token != "" && time.Until(g.expiresAt) > 5*time.Minute {
		return g.token, nil
	}
	jwt, err := g.appJWT()
	if err != nil {
		return "", err
	}
	var inst struct {
		ID int64 `json:"id"`
	}
	if err := g.raw("GET", "/repos/"+g.Repo+"/installation", "Bearer "+jwt, nil, &inst); err != nil {
		return "", fmt.Errorf("find installation (is the App installed on %s?): %w", g.Repo, err)
	}
	var tok struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := g.raw("POST", fmt.Sprintf("/app/installations/%d/access_tokens", inst.ID), "Bearer "+jwt, map[string]any{}, &tok); err != nil {
		return "", fmt.Errorf("mint installation token: %w", err)
	}
	g.token, g.expiresAt = tok.Token, tok.ExpiresAt
	return g.token, nil
}

func (g *GitHub) do(method, path string, body, out any) error {
	tok, err := g.Token()
	if err != nil {
		return err
	}
	return g.raw(method, path, "token "+tok, body, out)
}

func (g *GitHub) raw(method, path, auth string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, g.API+path, rdr)
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := g.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// HeadSHA is the poller's view of the branch.
func (g *GitHub) HeadSHA(branch string) (string, error) {
	var c struct {
		SHA string `json:"sha"`
	}
	err := g.do("GET", "/repos/"+g.Repo+"/commits/"+branch, nil, &c)
	return c.SHA, err
}

type WorkflowRun struct {
	ID        int64     `json:"id"`
	HeadSHA   string    `json:"head_sha"`
	Status    string    `json:"status"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
}

// QueuedRuns lists runs of the release workflow that GitHub has not handed
// to any runner yet. This is the first-party symptom of a scheduler outage.
func (g *GitHub) QueuedRuns(workflow, branch string) ([]WorkflowRun, error) {
	var out struct {
		Runs []WorkflowRun `json:"workflow_runs"`
	}
	path := fmt.Sprintf("/repos/%s/actions/workflows/%s/runs?status=queued&branch=%s&per_page=20", g.Repo, workflow, branch)
	err := g.do("GET", path, nil, &out)
	return out.Runs, err
}

// SetStatus reports the Tekton run back on the commit, so the PR/commit
// page still tells developers what happened to their change.
func (g *GitHub) SetStatus(sha, state, description, targetURL string) error {
	return g.do("POST", "/repos/"+g.Repo+"/statuses/"+sha, map[string]string{
		"state": state, "context": "dr/tekton-release", "description": description, "target_url": targetURL,
	}, nil)
}
