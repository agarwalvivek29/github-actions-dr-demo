#!/usr/bin/env python3
"""Create the DR GitHub App from a manifest (one click in the browser).

  ./scripts/create-github-app.py --repo agarwalvivek29/dr-demo-app [--name my-dr-app] [--org my-org]

1. Creates a smee.io channel for webhook delivery (no public ingress needed).
2. Opens github.com with a pre-filled App manifest; you click "Create".
3. Exchanges the returned code for the App id, private key and webhook secret,
   and writes them to .env and .secrets/github-app.pem.

Stdlib only.
"""
import argparse
import http.server
import json
import os
import secrets
import sys
import urllib.parse
import urllib.request
import webbrowser
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PORT = 3456

# What each permission is for (keep this list minimal — it's a talk slide).
PERMISSIONS = {
    "contents": "read",         # poll branch head + clone for Tekton
    "statuses": "write",        # report Tekton results on the commit
    "actions": "read",          # queue probe: how long have our runs been queued?
    "administration": "write",  # ARC registers self-hosted runners with the same App
    "metadata": "read",
}


def update_env(values: dict) -> None:
    path = ROOT / ".env"
    lines = path.read_text().splitlines() if path.exists() else []
    seen = set()
    out = []
    for line in lines:
        key = line.split("=", 1)[0]
        if key in values:
            out.append(f"{key}={values[key]}")
            seen.add(key)
        else:
            out.append(line)
    out += [f"{k}={v}" for k, v in values.items() if k not in seen]
    path.write_text("\n".join(out) + "\n")


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--repo", required=True, help="owner/name of the demo app repo")
    ap.add_argument("--name", help="GitHub App name (must be globally unique)")
    ap.add_argument("--org", help="create the App under this org instead of your user")
    args = ap.parse_args()
    owner = args.repo.split("/")[0]
    name = args.name or f"gha-dr-failover-{owner}"[:34]

    smee = urllib.request.urlopen("https://smee.io/new").geturl()
    print(f"smee channel: {smee}")

    manifest = {
        "name": name,
        "url": f"https://github.com/{args.repo}",
        "description": "Fails releases over from GitHub Actions to Tekton when Actions is down.",
        "hook_attributes": {"url": smee, "active": True},
        "redirect_url": f"http://localhost:{PORT}/callback",
        "public": False,
        "default_permissions": PERMISSIONS,
        "default_events": ["push"],
    }
    state = secrets.token_urlsafe(16)
    base = f"https://github.com/organizations/{args.org}" if args.org else "https://github.com"
    action = f"{base}/settings/apps/new?state={state}"
    result = {}

    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *a):
            pass

        def reply(self, body: str) -> None:
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.end_headers()
            self.wfile.write(body.encode())

        def do_GET(self):
            url = urllib.parse.urlparse(self.path)
            if url.path == "/":
                m = json.dumps(manifest).replace("'", "&#39;")
                self.reply(f"""<form id=f method=post action="{action}">
<input type=hidden name=manifest value='{m}'></form><script>f.submit()</script>""")
                return
            if url.path != "/callback":
                self.send_error(404)
                return
            q = urllib.parse.parse_qs(url.query)
            if q.get("state", [""])[0] != state:
                self.send_error(400, "state mismatch")
                return
            req = urllib.request.Request(
                f"https://api.github.com/app-manifests/{q['code'][0]}/conversions",
                method="POST", headers={"Accept": "application/vnd.github+json"})
            result.update(json.load(urllib.request.urlopen(req)))
            self.reply(f"<h2>Created {result['slug']}.</h2><p>Back to the terminal — "
                       f"next, install it on {args.repo}.</p>")

    print(f"opening http://localhost:{PORT}/ — click 'Create GitHub App' in the browser")
    srv = http.server.HTTPServer(("127.0.0.1", PORT), Handler)
    webbrowser.open(f"http://localhost:{PORT}/")
    while not result:
        srv.handle_request()

    key_path = ROOT / ".secrets" / "github-app.pem"
    key_path.parent.mkdir(mode=0o700, exist_ok=True)
    key_path.write_text(result["pem"])
    key_path.chmod(0o600)
    env = {
        "DEMO_REPO": args.repo,
        "GITHUB_APP_ID": str(result["id"]),
        "GITHUB_APP_SLUG": result["slug"],
        "GITHUB_WEBHOOK_SECRET": result["webhook_secret"],
        "GITHUB_APP_PRIVATE_KEY": ".secrets/github-app.pem",
        "SMEE_URL": smee,
    }
    if "DEMO_API_KEY=" not in ((ROOT / ".env").read_text() if (ROOT / ".env").exists() else ""):
        env["DEMO_API_KEY"] = secrets.token_hex(16)
    update_env(env)

    install = f"https://github.com/apps/{result['slug']}/installations/new"
    print(f"\nApp {result['slug']} (id {result['id']}) created; credentials in .env and {key_path.relative_to(ROOT)}")
    print(f"Now install it on {args.repo} (only that repo): {install}")
    webbrowser.open(install)


if __name__ == "__main__":
    sys.exit(main())
