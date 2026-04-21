# OpenShell Compute Driver Demo: Sandboxed Claude Code on OpenShift

## Architecture

```
Your Laptop                              OpenShift Cluster
┌────────────────────┐                   ┌──────────────────────────────────────┐
│                    │                   │  agent-sandbox-system namespace      │
│  openshell CLI     │                   │  ┌──────────────────────────────┐    │
│  (v0.0.32)         │                   │  │ agent-sandbox-controller     │    │
│                    │                   │  │ (watches Sandbox CRs,        │    │
│  openshell         │                   │  │  creates Pods + Services)    │    │
│   sandbox list     │                   │  └──────────────┬───────────────┘    │
│   sandbox create   │                   │                 │                    │
│   sandbox exec     │                   │  default namespace                   │
│   logs             │                   │  ┌──────────────▼───────────────┐    │
│                    │──port-forward───►│  │ Gateway + Driver Pod         │    │
│                    │     :8080        │  │ ┌───────────┐ ┌────────────┐ │    │
└────────────────────┘                   │  │ │ Go Driver │◄UDS►Gateway │ │    │
                                         │  │ │ (sidecar) │ │ (forked)  │ │    │
                                         │  │ └───────────┘ └────────────┘ │    │
                                         │  └──────────────┬───────────────┘    │
                                         │                 │ creates            │
                                         │  ┌──────────────▼───────────────┐    │
                                         │  │ Sandbox Pod                   │    │
                                         │  │ ┌──────────────────────────┐ │    │
                                         │  │ │ Supervisor               │ │    │
                                         │  │ │  Network namespace       │ │    │
                                         │  │ │  L7 Proxy (OPA policy)   │ │    │
                                         │  │ │  Landlock filesystem     │ │    │
                                         │  │ │  Credential proxy        │ │    │
                                         │  │ │    ┌──────────────────┐ │ │    │
                                         │  │ │    │ Claude Code      │ │ │    │
                                         │  │ │    │ (sandboxed)      │ │ │    │
                                         │  │ │    └──────────────────┘ │ │    │
                                         │  │ └──────────────────────────┘ │    │
                                         │  └──────────────────────────────┘    │
                                         └──────────────────────────────────────┘
```

## Prerequisites

### 1. Install agent-sandbox CRD and controller

```bash
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/agent-sandbox/main/k8s/crds/agents.x-k8s.io_sandboxes.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/agent-sandbox/main/k8s/rbac.generated.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/agent-sandbox/main/k8s/controller.yaml

# Fix the controller image (upstream manifest uses ko:// references)
kubectl set image deployment/agent-sandbox-controller \
  agent-sandbox-controller=registry.k8s.io/agent-sandbox/agent-sandbox-controller:v0.3.10 \
  -n agent-sandbox-system
```

### 2. Create privileged service account (OpenShift)

The supervisor needs to create network namespaces, which requires privileged access on OpenShift.

```bash
kubectl create serviceaccount openshell-sandbox -n default
oc adm policy add-scc-to-user privileged -z openshell-sandbox -n default
```

### 3. Deploy gateway + driver

```bash
export HANDSHAKE_SECRET=$(openssl rand -hex 32)
envsubst < deploy/gateway-with-driver.yaml | kubectl apply -f -

# Wait for both containers to be ready
kubectl wait --for=condition=ready pod -l app=openshell-gateway --timeout=60s
```

### 4. Connect the CLI

```bash
kubectl port-forward svc/openshell-gateway 8080:8080 &
openshell gateway add http://localhost:8080 --local
```

### 5. Create a provider (for credential proxy)

```bash
openshell provider create --name anthropic --type anthropic \
  --credential ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY
```

## Demo Steps

### Create a sandbox

```bash
openshell sandbox create --provider anthropic \
  --from quay.io/azaalouk/demo-sandbox-claude:latest -- sleep infinity
```

Ignore the "gateway CONNECT failed with status 400" error. This is a known issue between CLI v0.0.32 and the forked gateway's SSH path. The sandbox itself is created successfully.

### Verify it's running

```bash
openshell sandbox list
```

### Claude Code is inside the sandbox

```bash
openshell sandbox exec -n <sandbox-name> -- claude --version
```

Output: `2.1.116 (Claude Code)`

### Show sandbox user (not root)

```bash
openshell sandbox exec -n <sandbox-name> -- id
```

Output: `uid=1001(sandbox) gid=1001(sandbox) groups=1001(sandbox)`

### Show credential proxy (agent never sees real key)

```bash
openshell sandbox exec -n <sandbox-name> -- env | grep ANTHROPIC
```

Output: `ANTHROPIC_API_KEY=openshell:resolve:env:ANTHROPIC_API_KEY`

The real key is never exposed to the agent. The supervisor's proxy swaps the placeholder for the real key at the HTTP layer.

### Show proxy env vars (set automatically by supervisor)

```bash
openshell sandbox exec -n <sandbox-name> -- env | grep PROXY
```

Output shows `HTTPS_PROXY=http://10.200.0.1:3128` set automatically. The agent doesn't configure this.

### DENIED: curl to evil.com (blocked by L7 proxy)

```bash
openshell sandbox exec -n <sandbox-name> -- curl -sv --max-time 5 https://evil.com
```

Key output:
```
< HTTP/1.1 403 Forbidden
```

The supervisor's L7 proxy blocked the request. No policy allows any binary to reach `evil.com`.

### Show the OCSF security audit trail

```bash
openshell logs <sandbox-name> --source sandbox
```

Key line:
```
NET:OPEN [MED] DENIED /usr/bin/curl(PID) -> evil.com:443 [policy:- engine:opa]
  [reason:network connections not allowed by policy]
```

Every security decision is a structured OCSF event with the binary path, PID, destination, and reason.

### Live tail (shows events as they happen)

```bash
openshell logs <sandbox-name> --source sandbox --tail
```

Run curl in another terminal and watch the deny events appear in real-time.

## What the demo proves

| Claim | Evidence |
|---|---|
| Per-binary L7 network policy | `curl` → 403 Forbidden. Different binaries get different access. |
| Credential isolation | `ANTHROPIC_API_KEY=openshell:resolve:env:...` (placeholder, not real key) |
| Automatic proxy configuration | `HTTPS_PROXY` set by supervisor, not by user |
| OCSF structured logging | Every allow/deny is a structured event with binary, PID, destination |
| Landlock filesystem | Agent runs as `sandbox` user with restricted filesystem |
| Supervisor injection via init container | No hostPath, no DaemonSet, works with OpenShift SCCs |
| Kagenti-ready labels | `kagenti.io/type=agent` on every sandbox pod |
| External compute driver | Go driver communicates with Rust gateway via gRPC over UDS |

## Images used

| Image | Purpose | Registry |
|---|---|---|
| `quay.io/azaalouk/openshell-gateway` | Forked gateway with `--compute-driver-socket` | quay.io (public) |
| `quay.io/azaalouk/openshell-driver-openshift` | Go compute driver | quay.io (public) |
| `quay.io/azaalouk/openshell-supervisor` | Supervisor binary (built from fork) | quay.io (public) |
| `quay.io/azaalouk/demo-sandbox-claude` | Claude Code + iproute2 + sandbox user | quay.io (public) |

## Known issues

1. **"gateway CONNECT failed with status 400"** after `sandbox create`. The SSH CONNECT path has a protocol mismatch between CLI v0.0.32 and the forked gateway. Workaround: use `sandbox exec` instead of `sandbox connect`.

2. **Default restrictive policy**. When no custom policy is configured through the gateway, the supervisor uses a restrictive default that blocks all network traffic. Claude Code needs `api.anthropic.com` allowed to function. Configure a policy through `openshell policy set` or mount a policy ConfigMap.

3. **`/home/sandbox/.profile: Permission denied`**. Cosmetic error from Landlock blocking shell profile reads. Does not affect functionality.

## Cleanup

```bash
openshell sandbox delete <sandbox-name>
kubectl delete -f deploy/gateway-with-driver.yaml
kubectl delete serviceaccount openshell-sandbox -n default
kubectl delete deployment agent-sandbox-controller -n agent-sandbox-system
```
