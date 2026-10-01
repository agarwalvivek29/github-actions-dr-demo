// fake-status: a drop-in stand-in for githubstatus.com used to run outage drills.
//
// It serves the Statuspage v2 components.json the dr-controller already reads,
// proxied from the real githubstatus.com, with per-component overrides on top.
// A drill flips an override; the detection code path is unchanged.
//
//	GET  /api/v2/components.json         real feed + overrides
//	POST /api/override?component=Actions&status=major_outage
//	POST /api/override?component=Actions  (no status: clear override)
//	GET  /                               status page UI with buttons
package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

var validStatus = map[string]bool{
	"operational": true, "degraded_performance": true, "partial_outage": true, "major_outage": true,
}

// Used when the real status page is unreachable (e.g. demoing offline).
var fallback = []byte(`{"components":[
{"name":"Git Operations","status":"operational"},{"name":"Webhooks","status":"operational"},
{"name":"API Requests","status":"operational"},{"name":"Actions","status":"operational"},
{"name":"Packages","status":"operational"},{"name":"Pull Requests","status":"operational"}]}`)

type server struct {
	upstream string

	mu        sync.Mutex
	overrides map[string]string
	cache     []byte
	cachedAt  time.Time
}

func main() {
	s := &server{
		upstream:  env("UPSTREAM_URL", "https://www.githubstatus.com/api/v2/components.json"),
		overrides: map[string]string{},
	}
	http.HandleFunc("/api/v2/components.json", s.components)
	http.HandleFunc("/api/override", s.override)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(uiHTML))
	})
	log.Printf("fake-status on :8080 (upstream %s)", s.upstream)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func (s *server) feed() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache != nil && time.Since(s.cachedAt) < 30*time.Second {
		return s.cache
	}
	c := &http.Client{Timeout: 3 * time.Second}
	if resp, err := c.Get(s.upstream); err == nil {
		defer resp.Body.Close()
		if b, err := io.ReadAll(resp.Body); err == nil && resp.StatusCode == 200 {
			s.cache, s.cachedAt = b, time.Now()
			return b
		}
	}
	if s.cache != nil {
		return s.cache
	}
	return fallback
}

func (s *server) components(w http.ResponseWriter, _ *http.Request) {
	var doc map[string]any
	if err := json.Unmarshal(s.feed(), &doc); err != nil {
		json.Unmarshal(fallback, &doc)
	}
	s.mu.Lock()
	comps, _ := doc["components"].([]any)
	for _, c := range comps {
		if m, ok := c.(map[string]any); ok {
			if st, ok := s.overrides[m["name"].(string)]; ok {
				m["status"] = st
				m["simulated"] = true
			}
		}
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(doc)
}

func (s *server) override(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	comp, status := r.URL.Query().Get("component"), r.URL.Query().Get("status")
	if comp == "" || (status != "" && !validStatus[status]) {
		http.Error(w, "need component and a valid status", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if status == "" || status == "operational" {
		delete(s.overrides, comp)
		log.Printf("override cleared: %s", comp)
	} else {
		s.overrides[comp] = status
		log.Printf("override set: %s=%s", comp, status)
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

const uiHTML = `<!doctype html><html><head><meta charset="utf-8"><title>GitHub Status (drill)</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
body{margin:0;background:#0d1117;color:#e6edf3;font:15px/1.5 ui-sans-serif,system-ui,sans-serif;padding:24px}
.wrap{max-width:760px;margin:0 auto}h1{font-size:22px;margin:0 0 4px}p{color:#8b949e;margin:0 0 20px}
.row{display:flex;align-items:center;gap:10px;padding:12px 16px;border:1px solid #30363d;border-top:none;background:#161b22;flex-wrap:wrap}
.row:first-of-type{border-top:1px solid #30363d;border-radius:10px 10px 0 0}.row:last-of-type{border-radius:0 0 10px 10px}
.name{flex:1;min-width:140px}.st{font-size:13px;min-width:150px}.sim{font-size:11px;color:#f0883e;border:1px solid #f0883e;border-radius:4px;padding:0 5px;margin-left:6px}
.operational{color:#3fb950}.degraded_performance{color:#d29922}.partial_outage{color:#f0883e}.major_outage{color:#f85149}
button{background:#21262d;color:#e6edf3;border:1px solid #30363d;border-radius:6px;padding:4px 10px;cursor:pointer;font-size:12px}
</style></head><body><div class="wrap"><h1>GitHub Status — drill mode</h1>
<p>Live feed from githubstatus.com with simulated overrides. The dr-controller reads this instead of the real page.</p>
<div id="list"></div></div><script>
const S=['operational','degraded_performance','partial_outage','major_outage'];
async function set(c,s){await fetch('/api/override?component='+encodeURIComponent(c)+'&status='+s,{method:'POST'});tick()}
async function tick(){const d=await (await fetch('/api/v2/components.json')).json();
 document.getElementById('list').innerHTML=d.components.filter(c=>!c.name.startsWith('Visit')).map(c=>
 '<div class="row"><span class="name">'+c.name+(c.simulated?'<span class="sim">SIMULATED</span>':'')+'</span><span class="st '+c.status+'">'+c.status.replace('_',' ')+'</span>'
 +(c.name==='Actions'||c.name==='Webhooks'?S.map(s=>'<button onclick="set(\''+c.name+'\',\''+s+'\')">'+s.split('_')[0]+'</button>').join(''):'')+'</div>').join('')}
tick();setInterval(tick,2000);
</script></body></html>`
