# openshell-driver-openshift (Kagenti Fork)

> **This is a [Kagenti](https://github.com/kagenti/kagenti) fork of [zanetworker/openshell-driver-openshift](https://github.com/zanetworker/openshell-driver-openshift).**
>
> The `mvp` branch adds namespace flag, tenant labels, scoped RBAC, and dtach session
> persistence for multi-tenant OpenShell deployments.
> See the [epic](https://github.com/kagenti/kagenti/issues/1363) for the full plan.
>
> **Upstream tracking:** `main` is kept in sync with upstream. Fork-specific work happens on `mvp`.

An [OpenShell](https://github.com/NVIDIA/OpenShell) compute driver for OpenShift/Kubernetes clusters. Implements the `ComputeDriver` gRPC contract (`compute_driver.proto`) to provision agent sandboxes as `agents.x-k8s.io/v1alpha1/Sandbox` CRDs.

## What is this?

OpenShell uses pluggable compute drivers to provision sandboxes on different platforms. The gateway communicates with drivers over **gRPC on a Unix domain socket**. This driver targets OpenShift/Kubernetes clusters.

```
openshell-gateway ──── Unix domain socket ──── openshell-driver-openshift
                                                    │
                                                    ├── Creates Sandbox CRDs
                                                    ├── Watches for status changes
                                                    ├── Injects supervisor via init container
                                                    ├── Resolves pod IPs for exec/SSH
                                                    └── Sets kagenti.io/type=agent label
```

## Architecture

Three interfaces, one implemented (Phase 1), two stubbed for Phase 2:

```
Driver (gRPC server)
  ├── SandboxProvisioner  ← K8sProvisioner (implemented)
  │   Creates/deletes/watches Sandbox CRDs, injects supervisor,
  │   resolves endpoints, validates GPU capacity
  │
  ├── PlatformEnricher    ← NoopEnricher (Phase 2: SCC, SELinux, Routes)
  │
  └── DriverMetrics       ← NoopMetrics (Phase 2: Prometheus)
```

The driver handles only platform-specific provisioning. The gateway handles sandbox lifecycle orchestration, policy delivery, credential resolution, and user authentication.

## Supervisor injection

The upstream Rust K8s driver uses a hostPath volume to mount the supervisor binary from the node. This driver uses an **init container + emptyDir** instead, which avoids hostPath (blocked by OpenShift's `restricted-v2` SCC) and doesn't require pre-staging binaries on nodes.

See [docs/why-init-container.md](docs/why-init-container.md) for the full rationale.

```yaml
# What the driver produces in the pod spec:
initContainers:
  - name: supervisor-init
    image: ghcr.io/nvidia/openshell-community/supervisor:latest
    command: ["cp", "/usr/local/bin/openshell-sandbox", "/opt/openshell/bin/"]
containers:
  - name: agent
    command: ["/opt/openshell/bin/openshell-sandbox"]  # supervisor runs first
    securityContext:
      runAsUser: 0
      capabilities:
        add: [SYS_ADMIN, NET_ADMIN, SYS_PTRACE, SYSLOG]
volumes:
  - name: supervisor-bin
    emptyDir: {}
```

## gRPC Interface

The driver implements 9 RPCs defined in `proto/compute_driver.proto`:

| RPC | Purpose |
|-----|---------|
| `GetCapabilities` | Report driver name, version, GPU support |
| `ValidateSandboxCreate` | Pre-flight check (GPU capacity, input validation) |
| `CreateSandbox` | Create a Sandbox CR with supervisor init container |
| `GetSandbox` | Fetch current state of one sandbox |
| `ListSandboxes` | List all managed sandboxes |
| `StopSandbox` | Stop a sandbox without deleting its record |
| `DeleteSandbox` | Tear down a sandbox |
| `ResolveSandboxEndpoint` | Return pod IP or DNS for exec/SSH connectivity |
| `WatchSandboxes` | Server-streaming: emit real-time sandbox state changes |

## Build and Test

```bash
make build              # build the binary
make test               # unit tests + gRPC contract tests (no cluster needed)
make test-unit          # unit tests only
make test-grpc          # gRPC contract tests only
make test-integration   # integration tests (requires cluster + Sandbox CRD)
make test-all           # all tiers
```

### Testing tiers

| Tier | What | Cluster needed? | Command |
|------|------|----------------|---------|
| 1: Unit | Fake K8s clients, helpers, validation | No | `make test-unit` |
| 2: gRPC contract | Real gRPC over UDS, fake K8s | No | `make test-grpc` |
| 3: Integration | Real cluster, real CRDs | Yes | `make test-integration` |

Integration tests require:
- `KUBECONFIG` pointing at a cluster (or `~/.kube/config`)
- The [agent-sandbox CRD](https://github.com/kubernetes-sigs/agent-sandbox) installed
- A test namespace (default: `openshell-integration-test`, override via `INTEGRATION_TEST_NAMESPACE`)

## Run

```bash
# Start the driver
./openshell-driver-openshift \
  --socket /var/run/openshell-driver.sock \
  --namespace openshell-system \
  --supervisor-image ghcr.io/nvidia/openshell-community/supervisor:latest

# Point the OpenShell gateway at it (requires gateway fork with --compute-driver-socket)
openshell-gateway --compute-driver-socket /var/run/openshell-driver.sock
```

### Flags

| Flag | Default | Purpose |
|------|---------|---------|
| `--socket` | `/var/run/openshell-driver.sock` | UDS path for gRPC |
| `--namespace` | `openshell-system` | K8s namespace for sandboxes |
| `--supervisor-image` | `ghcr.io/nvidia/openshell-community/supervisor:latest` | Supervisor OCI image |
| `--supervisor-binary-path` | `/usr/local/bin/openshell-sandbox` | Binary path inside supervisor image |
| `--supervisor-mount-path` | `/opt/openshell/bin` | Mount point in agent container |

## Gateway dependency

The OpenShell gateway currently doesn't support connecting to external driver sockets. This driver requires a [gateway fork](https://github.com/zanetworker/OpenShell) with a `--compute-driver-socket` flag (~20 lines of Rust). See [design spec](docs/specs/2026-04-21-openshift-compute-driver-design.md) Section 7 for details.

## OpenShift-specific features

Implemented:
- **Supervisor init container**: emptyDir-based, no hostPath needed
- **Kagenti auto-enrollment**: `kagenti.io/type=agent` and `openshell.ai/managed-by=openshell` labels
- **GPU validation**: Checks `nvidia.com/gpu` allocatable before creating GPU sandboxes
- **`platform_config` passthrough**: `runtime_class_name` for Kata/sandboxed containers

Planned (Phase 2):
- SCC detection and auto-selection
- SELinux context via `seLinuxOptions` (Option C: driver requests, platform owns policy)
- OpenShift Route creation for external access
- OAuth proxy sidecar injection
- Prometheus metrics + ServiceMonitor
- Helm chart
