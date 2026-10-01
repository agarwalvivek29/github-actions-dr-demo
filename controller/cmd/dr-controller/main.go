// dr-controller: the failover switch between GitHub Actions and Tekton.
//
// It is a GitHub App that:
//   - watches GitHub health (status page + queue probe) and picks a release path
//   - receives push webhooks and, when Tekton owns releases, starts a PipelineRun
//   - polls the branch head as a fallback trigger for when webhooks stop arriving
//   - reports Tekton results back to the commit as a status
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Repo, Branch, Workflow   string
	StatusURL                string
	QueueThreshold           time.Duration
	FailbackCooldown         time.Duration
	FailoverOn               map[string]bool
	HealthInterval           time.Duration
	PollInterval             time.Duration
	WebhookGrace             time.Duration
	WebhookSecret            string
	Namespace, ConfigMap     string
	TektonNamespace          string
	Pipeline, ServiceAccount string
	AppNamespace, AppName    string
	DashboardURL             string
}

func loadConfig() Config {
	return Config{
		Repo:             must("GITHUB_REPO"),
		Branch:           env("GITHUB_BRANCH", "main"),
		Workflow:         env("GITHUB_WORKFLOW", "release.yml"),
		StatusURL:        env("STATUS_URL", "https://www.githubstatus.com/api/v2/components.json"),
		QueueThreshold:   dur("QUEUE_THRESHOLD", "10m"),
		FailbackCooldown: dur("FAILBACK_COOLDOWN", "5m"),
		FailoverOn:       parseSet(env("FAILOVER_ON", "partial_outage,major_outage")),
		HealthInterval:   dur("HEALTH_INTERVAL", "30s"),
		PollInterval:     dur("POLL_INTERVAL", "15s"),
		WebhookGrace:     dur("WEBHOOK_GRACE", "30s"),
		WebhookSecret:    os.Getenv("GITHUB_WEBHOOK_SECRET"),
		Namespace:        env("POD_NAMESPACE", "dr-system"),
		ConfigMap:        env("CONFIG_MAP", "dr-config"),
		TektonNamespace:  env("TEKTON_NAMESPACE", "ci"),
		Pipeline:         env("TEKTON_PIPELINE", "release"),
		ServiceAccount:   env("TEKTON_SERVICE_ACCOUNT", "release-bot"),
		AppNamespace:     env("APP_NAMESPACE", "demo-prod"),
		AppName:          env("APP_NAME", "dr-demo-app"),
		DashboardURL:     env("DASHBOARD_URL", "http://localhost:9097"),
	}
}

type Event struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
	Msg  string    `json:"msg"`
}

type Controller struct {
	cfg  Config
	gh   *GitHub
	kube *Kube

	mu           sync.Mutex
	mode         string
	path         Path
	pathSince    time.Time
	reasons      []string
	signals      Signals
	healthySince time.Time
	headSHA      string
	liveSHA      string
	liveBy       string
	firstSeen    map[string]time.Time // sha -> when the poller first saw it
	webhookSeen  map[string]bool      // sha -> a push webhook arrived for it
	lastWebhook  time.Time
	running      *PipelineRun
	runningSHA   string
	pendingSHA   string
	pendingSrc   string
	timeline     []Event
}

func main() {
	cfg := loadConfig()
	gh, err := NewGitHub(env("GITHUB_API", "https://api.github.com"), must("GITHUB_APP_ID"),
		env("GITHUB_APP_PRIVATE_KEY_PATH", "/etc/github-app/private-key.pem"), cfg.Repo)
	if err != nil {
		log.Fatal(err)
	}
	kube, err := NewKube()
	if err != nil {
		log.Fatal(err)
	}
	c := &Controller{cfg: cfg, gh: gh, kube: kube, path: PathGHA, pathSince: time.Now(),
		healthySince: time.Now(), firstSeen: map[string]time.Time{}, webhookSeen: map[string]bool{}}
	c.event("boot", fmt.Sprintf("watching %s@%s — status source %s", cfg.Repo, cfg.Branch, cfg.StatusURL))

	go c.every(cfg.HealthInterval, c.healthTick)
	go c.every(cfg.PollInterval, c.pollTick)
	go c.every(3*time.Second, c.watchTick)

	http.HandleFunc("/webhook", c.handleWebhook)
	http.HandleFunc("/trigger", c.handleTrigger)
	http.HandleFunc("/state", c.handleState)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	http.HandleFunc("/", c.handleUI)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func (c *Controller) every(d time.Duration, f func()) {
	for {
		f()
		time.Sleep(d)
	}
}

// event logs and records a line on the timeline shown in the UI.
func (c *Controller) event(kind, msg string) {
	log.Printf("%-9s %s", strings.ToUpper(kind), msg)
	c.timeline = append(c.timeline, Event{time.Now(), kind, msg})
	if len(c.timeline) > 40 {
		c.timeline = c.timeline[len(c.timeline)-40:]
	}
}

// healthTick: read the switch, probe GitHub, pick the release path.
func (c *Controller) healthTick() {
	mode := "auto"
	if data, err := c.kube.ConfigMap(c.cfg.Namespace, c.cfg.ConfigMap); err == nil && data["mode"] != "" {
		mode = data["mode"]
	}
	sig := c.probe()
	live, by := c.liveVersion()

	c.mu.Lock()
	defer c.mu.Unlock()
	c.liveSHA, c.liveBy = live, by
	if mode != c.mode {
		if c.mode != "" {
			c.event("switch", fmt.Sprintf("mode %s -> %s", c.mode, mode))
		}
		c.mode = mode
	}
	if sig.actionsDown(c.cfg) {
		c.healthySince = time.Now()
	}
	if sig.webhooksDown(c.cfg) && !c.signals.webhooksDown(c.cfg) {
		c.event("detect", "status page: Webhooks="+sig.StatusWebhooks+" — poller no longer waits for webhooks")
	}
	c.signals = sig

	d := decide(c.cfg, mode, c.path, sig, c.healthySince, time.Now())
	c.reasons = d.Reasons
	if d.Path == c.path {
		return
	}
	prev := c.path
	c.path, c.pathSince = d.Path, time.Now()
	if d.Path == PathTekton {
		c.event("failover", fmt.Sprintf("%s -> tekton: %s", prev, strings.Join(d.Reasons, "; ")))
		// Anything pushed before the switch is stuck in GitHub's queue: ship head now.
		if c.headSHA != "" {
			c.requestLocked(c.headSHA, "failover")
		}
	} else {
		c.event("failback", fmt.Sprintf("tekton -> %s: %s", d.Path, strings.Join(d.Reasons, "; ")))
	}
}

// pollTick is the fallback trigger: it doesn't need GitHub to push anything to us.
func (c *Controller) pollTick() {
	sha, err := c.gh.HeadSHA(c.cfg.Branch)
	if err != nil {
		log.Printf("poll: %v", err)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if sha != c.headSHA {
		if c.headSHA != "" {
			c.event("poll", fmt.Sprintf("new head %s on %s", short(sha), c.cfg.Branch))
		}
		c.headSHA = sha
	}
	if _, ok := c.firstSeen[sha]; !ok {
		c.firstSeen[sha] = time.Now()
	}
	if c.path != PathTekton || c.webhookSeen[sha] {
		return
	}
	// With healthy webhooks give the push event a head start; during a
	// webhooks incident don't wait at all.
	grace := c.cfg.WebhookGrace
	if c.signals.webhooksDown(c.cfg) {
		grace = 0
	}
	if time.Since(c.firstSeen[sha]) >= grace {
		c.requestLocked(sha, "poller")
	}
}

func (c *Controller) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if !validSignature(c.cfg.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	if r.Header.Get("X-GitHub-Event") != "push" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var p struct {
		Ref   string `json:"ref"`
		After string `json:"after"`
		Repo  struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !strings.EqualFold(p.Repo.FullName, c.cfg.Repo) || p.Ref != "refs/heads/"+c.cfg.Branch || strings.Trim(p.After, "0") == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastWebhook = time.Now()
	c.webhookSeen[p.After] = true
	c.headSHA = p.After
	if c.path == PathTekton {
		c.event("webhook", fmt.Sprintf("push %s — Tekton owns releases", short(p.After)))
		c.requestLocked(p.After, "webhook")
	} else {
		c.event("webhook", fmt.Sprintf("push %s — GitHub Actions owns releases, ignoring", short(p.After)))
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleTrigger is the break-glass button: release a sha (default: head) on Tekton now.
func (c *Controller) handleTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	sha := r.URL.Query().Get("sha")
	if sha == "" {
		sha = c.headSHA
	}
	if sha == "" {
		http.Error(w, "no head sha yet", http.StatusConflict)
		return
	}
	c.requestLocked(sha, "manual")
	fmt.Fprintf(w, "requested %s\n", sha)
}

func validSignature(secret string, body []byte, header string) bool {
	if secret == "" {
		return true // local testing only; the deployed App always has a secret
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(header))
}

// liveVersion reads what is actually running in prod.
func (c *Controller) liveVersion() (sha, by string) {
	var d struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", c.cfg.AppNamespace, c.cfg.AppName)
	if err := c.kube.do("GET", path, "", nil, &d); err != nil {
		return "", ""
	}
	return d.Metadata.Annotations["dr.demo/git-sha"], d.Metadata.Annotations["dr.demo/deployed-by"]
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func must(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("%s is required", k)
	}
	return v
}

func dur(k, def string) time.Duration {
	d, err := time.ParseDuration(env(k, def))
	if err != nil {
		log.Fatalf("%s: %v", k, err)
	}
	return d
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
