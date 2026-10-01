// dr-demo-app: the "production" service for the GitHub Actions -> Tekton DR demo.
// It renders which commit is live and which pipeline shipped it.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"os"
)

// Bump this line in a commit to "ship a hotfix" during the drill.
const message = "hello from FOSS Mumbai"

type info struct {
	Message    string `json:"message"`
	GitSHA     string `json:"git_sha"`
	DeployedBy string `json:"deployed_by"`
	DeployedAt string `json:"deployed_at"`
	SecretFP   string `json:"secret_fingerprint"`
}

func current() info {
	return info{
		Message:    message,
		GitSHA:     env("GIT_SHA", "dev"),
		DeployedBy: env("DEPLOYED_BY", "unknown"),
		DeployedAt: env("DEPLOYED_AT", "unknown"),
		SecretFP:   fingerprint(os.Getenv("DEMO_API_KEY")),
	}
}

// fingerprint proves the runtime secret made it through either pipeline
// without ever printing it.
func fingerprint(s string) string {
	if s == "" {
		return "MISSING"
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var page = template.Must(template.New("page").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta http-equiv="refresh" content="3">
<title>dr-demo-app</title>
<style>
body{margin:0;font-family:ui-sans-serif,system-ui,sans-serif;background:#0d1117;color:#e6edf3;
display:flex;align-items:center;justify-content:center;min-height:100vh}
.card{padding:48px 64px;border-radius:16px;border:1px solid #30363d;background:#161b22;text-align:center}
.who{font-size:14px;letter-spacing:.12em;text-transform:uppercase;padding:6px 14px;border-radius:999px;display:inline-block}
.github-actions{background:#1f6feb33;color:#58a6ff}.tekton{background:#f0883e33;color:#f0883e}.unknown{background:#6e768133}
h1{font-size:44px;margin:24px 0 8px}code{font-size:18px;color:#8b949e}
dl{display:grid;grid-template-columns:auto auto;gap:6px 18px;margin-top:28px;text-align:left;color:#8b949e}
dd{margin:0;color:#e6edf3;font-family:ui-monospace,monospace}
</style></head><body><div class="card">
<span class="who {{.DeployedBy}}">shipped by {{.DeployedBy}}</span>
<h1>{{.Message}}</h1><code>{{.GitSHA}}</code>
<dl><dt>deployed at</dt><dd>{{.DeployedAt}}</dd><dt>secret fingerprint</dt><dd>{{.SecretFP}}</dd></dl>
</div></body></html>`))

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if err := page.Execute(w, current()); err != nil {
			log.Print(err)
		}
	})
	http.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(current())
	})
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	log.Printf("dr-demo-app %s listening on :8080", current().GitSHA)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
