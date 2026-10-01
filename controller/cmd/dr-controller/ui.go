package main

import (
	"encoding/json"
	"net/http"
	"time"
)

type State struct {
	Mode         string    `json:"mode"`
	Path         Path      `json:"path"`
	PathSince    time.Time `json:"path_since"`
	Reasons      []string  `json:"reasons"`
	Signals      Signals   `json:"signals"`
	HeadSHA      string    `json:"head_sha"`
	LiveSHA      string    `json:"live_sha"`
	LiveBy       string    `json:"live_by"`
	RunningRun   string    `json:"running_run,omitempty"`
	RunningSHA   string    `json:"running_sha,omitempty"`
	PendingSHA   string    `json:"pending_sha,omitempty"`
	LastWebhook  time.Time `json:"last_webhook"`
	QueueLimit   string    `json:"queue_threshold"`
	StatusSource string    `json:"status_source"`
	Timeline     []Event   `json:"timeline"`
}

func (c *Controller) handleState(w http.ResponseWriter, _ *http.Request) {
	c.mu.Lock()
	s := State{Mode: c.mode, Path: c.path, PathSince: c.pathSince, Reasons: c.reasons, Signals: c.signals,
		HeadSHA: c.headSHA, LiveSHA: c.liveSHA, LiveBy: c.liveBy, RunningSHA: c.runningSHA, PendingSHA: c.pendingSHA,
		LastWebhook: c.lastWebhook, QueueLimit: c.cfg.QueueThreshold.String(), StatusSource: c.cfg.StatusURL,
		Timeline: append([]Event(nil), c.timeline...)}
	if c.running != nil {
		s.RunningRun = c.running.Metadata.Name
	}
	c.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(s)
}

func (c *Controller) handleUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(uiHTML))
}

const uiHTML = `<!doctype html><html><head><meta charset="utf-8"><title>dr-controller</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
:root{--bg:#0d1117;--card:#161b22;--line:#30363d;--fg:#e6edf3;--mute:#8b949e;--gha:#58a6ff;--tek:#f0883e;--ok:#3fb950;--bad:#f85149}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.5 ui-sans-serif,system-ui,sans-serif;padding:24px}
.wrap{max-width:1100px;margin:0 auto;display:grid;gap:16px;grid-template-columns:1fr 1fr}
.card{background:var(--card);border:1px solid var(--line);border-radius:12px;padding:18px 20px}
.hero{grid-column:1/-1;display:flex;align-items:center;gap:24px}
.path{font-size:40px;font-weight:700;letter-spacing:-.02em}
.gha{color:var(--gha)}.tekton{color:var(--tek)}
.pill{padding:3px 10px;border-radius:999px;border:1px solid var(--line);color:var(--mute);font-size:13px}
h2{margin:0 0 10px;font-size:13px;text-transform:uppercase;letter-spacing:.1em;color:var(--mute)}
dl{display:grid;grid-template-columns:max-content 1fr;gap:4px 16px;margin:0}dt{color:var(--mute)}dd{margin:0;font-family:ui-monospace,monospace}
.operational{color:var(--ok)}.degraded_performance{color:#d29922}.partial_outage,.major_outage{color:var(--bad)}
.tl{grid-column:1/-1;font-family:ui-monospace,monospace;font-size:13px}
.tl div{padding:3px 0;border-bottom:1px solid #21262d}.tl b{display:inline-block;width:80px}
.k-failover b,.k-failed b,.k-error b{color:var(--bad)}.k-shipped b,.k-failback b{color:var(--ok)}.k-release b{color:var(--tek)}
.k-poll b,.k-webhook b{color:var(--gha)}.t{color:var(--mute);margin-right:10px}
button{background:#21262d;color:var(--fg);border:1px solid var(--line);border-radius:6px;padding:6px 12px;cursor:pointer}
@media(max-width:720px){.wrap{grid-template-columns:1fr}.hero{flex-wrap:wrap}}
</style></head><body><div class="wrap">
<div class="card hero"><div><h2>releases go to</h2><div id="path" class="path">…</div></div>
<div><span class="pill" id="mode"></span> <span class="pill" id="since"></span><div id="reasons" style="margin-top:8px;color:var(--mute)"></div></div>
<div style="margin-left:auto"><button onclick="fetch('/trigger',{method:'POST'})">break glass: release head on Tekton</button></div></div>
<div class="card"><h2>signals</h2><dl id="signals"></dl></div>
<div class="card"><h2>release</h2><dl id="release"></dl></div>
<div class="card tl"><h2>timeline</h2><div id="tl"></div></div>
</div><script>
const s7=s=>s?s.slice(0,7):'—', ago=t=>{if(!t||t.startsWith('0001'))return 'never';const d=(Date.now()-new Date(t))/1000;return d<90?Math.round(d)+'s ago':Math.round(d/60)+'m ago'};
const row=(k,v,c)=>'<dt>'+k+'</dt><dd class="'+(c||'')+'">'+v+'</dd>';
async function tick(){try{const s=await (await fetch('/state')).json();
 const p=document.getElementById('path');p.textContent=s.path==='tekton'?'Tekton':'GitHub Actions';p.className='path '+(s.path==='tekton'?'tekton':'gha');
 document.getElementById('mode').textContent='mode: '+s.mode;document.getElementById('since').textContent='since '+ago(s.path_since);
 document.getElementById('reasons').textContent=(s.reasons||[]).join(' · ');
 const g=s.signals||{};
 document.getElementById('signals').innerHTML=row('status: Actions',g.status_actions||g.status_error||'?',g.status_actions)+row('status: Webhooks',g.status_webhooks||'?',g.status_webhooks)
  +row('queued runs',(g.queued_runs||0)+(g.oldest_queued_ns?' (oldest '+Math.round(g.oldest_queued_ns/1e9)+'s, limit '+s.queue_threshold+')':''),g.oldest_queued_ns/1e9>1?'degraded_performance':'')
  +row('last webhook',ago(s.last_webhook))+row('status source',s.status_source.replace(/^https?:\/\//,''));
 document.getElementById('release').innerHTML=row('branch head',s7(s.head_sha))+row('live in prod',s7(s.live_sha)+(s.live_by?' · '+s.live_by:''),s.head_sha&&s.head_sha===s.live_sha?'operational':'degraded_performance')
  +row('tekton running',s.running_run?s.running_run:'—')+row('queued next',s7(s.pending_sha));
 document.getElementById('tl').innerHTML=(s.timeline||[]).slice().reverse().map(e=>'<div class="k-'+e.kind+'"><span class="t">'+new Date(e.at).toLocaleTimeString()+'</span><b>'+e.kind+'</b>'+e.msg.replace(/</g,'&lt;')+'</div>').join('');
}catch(e){}}
tick();setInterval(tick,1500);
</script></body></html>`
