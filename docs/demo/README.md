# OpenShell Compute Driver Demo: Sandboxed Claude Code on OpenShift

## Architecture

```
Your Laptop                              OpenShift Cluster
┌────────────────────┐                   ┌──────────────────────────────────────┐
│                    │                   │                                      │
│  Go Compute Driver │ ──gRPC/UDS──┐     │  agent-sandbox-system namespace      │
│  (openshell-driver │             │     │  ┌──────────────────────────────┐    │
│   -openshift)      │             │     │  │ agent-sandbox-controller     │    │
│                    │             │     │  │ (watches Sandbox CRs,        │    │
└────────────────────┘             │     │  │  creates Pods + Services)    │    │
                                   │     │  └──────────────┬───────────────┘    │
                                   │     │                 │                    │
                                   │     │  default namespace                   │
                                   │     │  ┌──────────────▼───────────────┐    │
                          creates   │     │  │ Sandbox CR: claude-sandbox   │    │
                          Sandbox ──┘     │  │ labels:                      │    │
                          CRD via         │  │   kagenti.io/type: agent     │    │
                          K8s API         │  │   openshell.ai/managed-by    │    │
                                         │  └──────────────┬───────────────┘    │
                                         │                 │                    │
                                         │  ┌──────────────▼───────────────┐    │
                                         │  │ Pod: claude-sandbox          │    │
                                         │  │                              │    │
                                         │  │ Init: supervisor-init        │    │
                                         │  │   copies openshell-sandbox   │    │
                                         │  │   from quay.io/azaalouk/     │    │
                                         │  │   openshell-supervisor       │    │
                                         │  │                              │    │
                                         │  │ Container: agent             │    │
                                         │  │ ┌──────────────────────────┐ │    │
                                         │  │ │ OpenShell Supervisor     │ │    │
                                         │  │ │                          │ │    │
                                         │  │ │ Network namespace        │ │    │
                                         │  │ │   10.200.0.1 ↔ 10.200.0.2│ │    │
                                         │  │ │ L7 Proxy (:3128)        │ │    │
                                         │  │ │   OPA per-binary policy  │ │    │
                                         │  │ │ Landlock filesystem      │ │    │
                                         │  │ │ Bypass detection         │ │    │
                                         │  │ │ Ephemeral TLS CA         │ │    │
                                         │  │ │                          │ │    │
                                         │  │ │   ┌──────────────────┐  │ │    │
                                         │  │ │   │ Claude Code      │  │ │    │
                                         │  │ │   │ (sandboxed)      │  │ │    │
                                         │  │ │   │ user: sandbox    │  │ │    │
                                         │  │ │   │ netns isolated   │  │ │    │
                                         │  │ │   │ Landlock applied │  │ │    │
                                         │  │ │   └──────────────────┘  │ │    │
                                         │  │ └──────────────────────────┘ │    │
                                         │  └──────────────────────────────┘    │
                                         └──────────────────────────────────────┘
```

## What OpenShell enforces

| Security control | What it does | How to demo |
|---|---|---|
| **Per-binary L7 network policy** | `claude` can reach `api.anthropic.com`; `curl` can only reach `github.com`. Same host, different binaries, different access. | Run `curl` to anthropic (DENIED), then node/claude to anthropic (ALLOWED) |
| **Landlock filesystem** | `/sandbox` is writable, `/etc` is read-only | Try writing to `/etc` (fails), writing to `/sandbox` (succeeds) |
| **Network namespace** | All traffic forced through L7 proxy at 10.200.0.1:3128 | Show `ip addr` inside sandbox netns (10.200.0.2 only) |
| **OCSF security logging** | Every allow/deny decision is logged with binary path, destination, policy rule | Read pod logs, grep for `NET:OPEN` |
| **Bypass detection** | iptables rules detect attempts to skip the proxy | Show `CONFIG:INSTALLED` in logs |

## Prerequisites

1. OpenShift cluster with kubeconfig
2. agent-sandbox CRD and controller installed
3. Privileged SCC service account (`openshell-sandbox`)
4. ConfigMap with OPA policy (`openshell-policy`)
5. Images public on quay.io:
   - `quay.io/azaalouk/openshell-supervisor:latest`
   - `quay.io/azaalouk/demo-sandbox-claude:latest`

## Demo Steps

### Step 1: Show the sandbox is running

```bash
# The sandbox CR, pod, and labels
kubectl get sandbox claude-sandbox -n default
kubectl get pod claude-sandbox -n default
kubectl get pod claude-sandbox -n default -o jsonpath='{.metadata.labels}' | python3 -m json.tool
```

Expected output shows `kagenti.io/type: agent` and `openshell.ai/managed-by: openshell`.

### Step 2: Show what OpenShell set up (OCSF security events)

```bash
kubectl logs claude-sandbox -n default | head -15
```

Expected output shows the supervisor boot sequence:
```
CONFIG:LOADING   — OPA policy loaded
CONFIG:VALIDATED — 'sandbox' user verified
CONFIG:ENABLED   — Ephemeral TLS CA generated
CONFIG:CREATING  — Network namespace being created
CONFIG:CREATED   — Network namespace active (10.200.0.1 ↔ 10.200.0.2)
CONFIG:INSTALLED — Bypass detection rules installed
NET:LISTEN       — L7 proxy listening on :3128
CONFIG:PROBED    — Landlock available (ABI v5)
CONFIG:APPLYING  — Landlock filesystem rules applied
CONFIG:BUILT     — 11 Landlock rules active
PROC:LAUNCH      — Agent process launched inside sandbox
```

### Step 3: Verify Claude Code is inside the sandbox

```bash
kubectl exec claude-sandbox -n default -- claude --version
```

Expected: `2.1.116 (Claude Code)`

### Step 4: Demo per-binary network policy

This is the key differentiator. The policy says:
- `claude`/`node` binary can reach `api.anthropic.com:443`
- `curl` binary can only reach `github.com:443` and `api.github.com:443`

```bash
# Find the network namespace name
NS=$(kubectl logs claude-sandbox -n default | grep "CONFIG:CREATED" | grep -o 'ns:sandbox-[a-f0-9]*' | cut -d: -f2)

# curl (binary: /usr/bin/curl) trying to reach api.anthropic.com → DENIED
kubectl exec claude-sandbox -n default -- \
  nsenter --net=/var/run/netns/$NS -- \
  su -s /bin/sh sandbox -c \
  'HTTPS_PROXY=http://10.200.0.1:3128 curl -s --max-time 5 https://api.anthropic.com 2>&1; echo "exit: $?"'

# Check the OCSF log
kubectl logs claude-sandbox -n default --tail=3
```

Expected OCSF log:
```
NET:OPEN [MED] DENIED /usr/bin/curl(PID) -> api.anthropic.com:443 [policy:- engine:opa]
[reason:endpoint api.anthropic.com:443 not in policy 'github']
```

The supervisor blocked `curl` from reaching `api.anthropic.com` because the `github` policy (which is the only policy that allows `/usr/bin/curl`) doesn't include `api.anthropic.com`. Even though `api.anthropic.com` is allowed for the `claude` binary, each binary gets its own policy. A prompt injection that spawns `curl` to exfiltrate data would be caught here.

```bash
# curl trying to reach evil.com → also DENIED
kubectl exec claude-sandbox -n default -- \
  nsenter --net=/var/run/netns/$NS -- \
  su -s /bin/sh sandbox -c \
  'HTTPS_PROXY=http://10.200.0.1:3128 curl -s --max-time 5 https://evil.com 2>&1; echo "exit: $?"'

# OCSF log shows denial for every policy rule:
kubectl logs claude-sandbox -n default --tail=3
```

Expected:
```
NET:OPEN [MED] DENIED /usr/bin/curl(PID) -> evil.com:443 [policy:- engine:opa]
[reason:endpoint evil.com:443 not in policy 'claude'; endpoint evil.com:443 not in policy 'github'; ...]
```

### Step 5: Show the policy

```bash
kubectl get configmap openshell-policy -n default -o jsonpath='{.data.sandbox-policy\.yaml}'
```

Point out:
- `claude` and `node` binaries can reach `api.anthropic.com`
- `curl` and `git` can reach `github.com`
- `npm` and `node` can reach `registry.npmjs.org`
- No binary can reach anything else

### Step 6: Show the full OCSF audit trail

```bash
kubectl logs claude-sandbox -n default
```

Every security decision is logged with:
- Binary path and PID
- Destination host and port
- Policy name and engine
- Allow/deny reason
- OCSF severity level

### Step 7: Show the sandbox pod spec (init container injection)

```bash
kubectl get pod claude-sandbox -n default -o json | python3 -c "
import json, sys
pod = json.load(sys.stdin)
spec = pod['spec']

print('Init containers:')
for ic in spec.get('initContainers', []):
    print(f'  {ic[\"name\"]}: {ic[\"image\"]}')
    print(f'    command: {ic[\"command\"]}')

print()
print('Agent container:')
c = spec['containers'][0]
print(f'  image: {c[\"image\"]}')
print(f'  command: {c.get(\"command\", \"N/A\")}')
print(f'  securityContext: {c.get(\"securityContext\", {})}')
"
```

This shows the supervisor binary being injected via init container (not hostPath), which is the OpenShift-friendly approach.

## Cleanup

```bash
kubectl delete sandbox claude-sandbox -n default
kubectl delete configmap openshell-policy -n default
kubectl delete serviceaccount openshell-sandbox -n default
```

## Key talking points

1. **Per-binary policy is unique to OpenShell.** K8s NetworkPolicy is L3/L4 and applies to the whole pod. OpenShell's OPA policy distinguishes which binary made the network call. A malicious npm postinstall script can't exfiltrate data even if the agent has network access.

2. **Supervisor injection via init container.** No hostPath, no DaemonSet, no node-level changes. Works with OpenShift SCCs. The supervisor is a single binary copied from a container image.

3. **OCSF structured logging.** Every security decision is a structured event, not a free-text log line. Ready for SIEM integration.

4. **Kagenti-ready labels.** When Kagenti is installed, the `kagenti.io/type: agent` label auto-enrolls the sandbox with SPIFFE identity, OTEL tracing, and A2A discovery. Zero additional configuration.

5. **Driver is Go, supervisor is Rust, gateway is Rust.** The compute driver can be written in any language because it communicates via gRPC. We proved this with a Go driver talking to a Rust supervisor.
