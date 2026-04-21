# Why Init Container for Supervisor Delivery

## The upstream approach

The Rust K8s driver in NVIDIA/OpenShell uses a hostPath volume to mount the supervisor binary from the node filesystem:

```yaml
volumes:
  - name: supervisor
    hostPath:
      path: /opt/openshell/bin
      type: Directory
```

This requires the supervisor binary to be pre-staged on every node, typically by a DaemonSet or by baking it into the node image. The pods mount it read-only.

## Why we diverge

OpenShift's `restricted-v2` SCC (the default for workloads) blocks hostPath volume mounts. Using hostPath requires either:

- `privileged` SCC (too broad, violates least-privilege)
- A custom SCC that allows hostPath but restricts everything else (operational overhead per cluster)
- Dropping to `anyuid` or `hostaccess` SCCs (security regression)

Additionally, a DaemonSet to pre-stage the binary on every node requires its own elevated SCC, RBAC, and lifecycle management.

## Our approach

We use an init container that copies the supervisor binary from a container image into an emptyDir volume shared with the agent container:

```yaml
initContainers:
  - name: supervisor-init
    image: ghcr.io/nvidia/openshell-community/supervisor:latest
    command: ["cp", "/usr/local/bin/openshell-sandbox", "/opt/openshell/bin/"]
    volumeMounts:
      - name: supervisor-bin
        mountPath: /opt/openshell/bin

containers:
  - name: agent
    command: ["/opt/openshell/bin/openshell-sandbox"]
    volumeMounts:
      - name: supervisor-bin
        mountPath: /opt/openshell/bin
        readOnly: true

volumes:
  - name: supervisor-bin
    emptyDir: {}
```

## Trade-offs

| | hostPath (upstream) | Init container (ours) |
|---|---|---|
| SCC requirement | Needs hostPath access (custom or privileged SCC) | Works without hostPath |
| Node pre-staging | Required (DaemonSet or baked into node image) | Not required |
| Cold start cost | None (binary already on node) | One `cp` command (~15MB, <1 second) |
| Image pull | None (binary on node filesystem) | One pull per node (cached after first) |
| Supervisor version | Tied to what's on the node | Tied to init container image tag |
| BYOC compatibility | Works with any agent image | Works with any agent image |
| Operational overhead | DaemonSet lifecycle, node affinity, SCC exceptions | None beyond the init container |

The init container approach trades a negligible cold start cost (copying one binary) for significantly simpler operations on OpenShift. No DaemonSet, no node access, no SCC exceptions for the delivery mechanism.

Note: the agent container itself still requires elevated capabilities (`SYS_ADMIN`, `NET_ADMIN`, `SYS_PTRACE`, `SYSLOG`) for the supervisor to create network namespaces and install Landlock/seccomp. This requires a custom SCC regardless of the supervisor delivery method. The init container approach eliminates the *additional* SCC exception that hostPath would require on top of the capabilities.
