#!/usr/bin/env bash
# Create the kind cluster and a local registry reachable from nodes and pods
# as kind-registry:5000 (and from the laptop as localhost:5001).
source "$(dirname "$0")/lib.sh"

if [[ "$(docker inspect -f '{{.State.Running}}' "$REG_NAME" 2>/dev/null || true)" != "true" ]]; then
  log "starting local registry $REG_NAME (localhost:$REG_PORT)"
  docker rm -f "$REG_NAME" >/dev/null 2>&1 || true
  docker run -d --restart=always -p "127.0.0.1:${REG_PORT}:5000" --network bridge --name "$REG_NAME" registry:2 >/dev/null
fi

if ! kind get clusters | grep -qx "$CLUSTER"; then
  log "creating kind cluster $CLUSTER"
  kind create cluster --config "$ROOT/kind/cluster.yaml"
fi

log "wiring registry into containerd"
for node in $(kind get nodes --name "$CLUSTER"); do
  for host in "localhost:${REG_PORT}" "kind-registry:5000"; do
    dir="/etc/containerd/certs.d/${host}"
    docker exec "$node" mkdir -p "$dir"
    printf '[host."http://%s:5000"]\n' "$REG_NAME" | docker exec -i "$node" cp /dev/stdin "$dir/hosts.toml"
  done
done
docker network connect kind "$REG_NAME" 2>/dev/null || true

# Pods resolve through CoreDNS, which can't see docker-network names: pin them.
# EXTRA_DNS_CONTAINERS lets tests add more (e.g. a local git daemon).
log "adding docker-network hosts to CoreDNS"
hosts=""
for c in "$REG_NAME" ${EXTRA_DNS_CONTAINERS:-}; do
  hosts+="$(docker inspect -f '{{(index .NetworkSettings.Networks "kind").IPAddress}}' "$c") $c,"
done
corefile=$(k -n kube-system get cm coredns -o jsonpath='{.data.Corefile}' | HOSTS="$hosts" python3 -c '
import os, re, sys
cf = re.sub(r"    hosts \{.*?\n    \}\n", "", sys.stdin.read(), flags=re.S)
entries = "".join(f"       {h}\n" for h in os.environ["HOSTS"].split(",") if h)
print(cf.replace("    ready\n", "    ready\n    hosts {\n" + entries + "       fallthrough\n    }\n", 1), end="")')
k -n kube-system create cm coredns --from-literal=Corefile="$corefile" --dry-run=client -o yaml | k apply -f - >/dev/null

k apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata: { name: local-registry-hosting, namespace: kube-public }
data:
  localRegistryHosting.v1: |
    host: "localhost:${REG_PORT}"
    hostFromContainerRuntime: "kind-registry:5000"
    hostFromClusterNetwork: "kind-registry:5000"
EOF
log "cluster ready: context $CTX"
