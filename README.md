# openshell-driver-openshift

An [OpenShell](https://github.com/NVIDIA/OpenShell) compute driver for OpenShift/Kubernetes clusters. Implements the `ComputeDriver` gRPC contract (`compute_driver.proto`) to provision agent sandboxes as `agents.x-k8s.io/v1alpha1/Sandbox` CRDs.

## What is this?

OpenShell uses pluggable compute drivers to provision sandboxes on different platforms. The gateway communicates with drivers over **gRPC on a Unix domain socket**. This driver targets OpenShift/Kubernetes clusters.

```
openshell-gateway ──── Unix domain socket ──── openshell-driver-openshift
                                                    │
                                                    ├── Creates Sandbox CRDs
                                                    ├── Watches for status changes
                                                    ├── Resolves pod IPs for exec/SSH
                                                    └── Sets kagenti.io/type=agent label
```

## gRPC Interface

The driver implements 9 RPCs defined in `proto/compute_driver.proto`:

| RPC | Purpose |
|-----|---------|
| `GetCapabilities` | Report driver name, version, GPU support |
| `ValidateSandboxCreate` | Pre-flight check (GPU capacity, namespace quotas) |
| `CreateSandbox` | Create a Sandbox CR on the cluster |
| `GetSandbox` | Fetch current state of one sandbox |
| `ListSandboxes` | List all managed sandboxes |
| `StopSandbox` | Stop a sandbox without deleting its record |
| `DeleteSandbox` | Tear down a sandbox |
| `ResolveSandboxEndpoint` | Return pod IP or DNS for exec/SSH connectivity |
| `WatchSandboxes` | Server-streaming: emit real-time sandbox state changes |

## Build

```bash
# Generate Go code from proto (requires buf)
make proto

# Build the binary
make build

# Run tests
make test
```

## Run

The driver runs as a standalone process and listens on a Unix domain socket:

```bash
# Start the driver
./openshell-driver-openshift \
  --socket /var/run/openshell-driver.sock \
  --namespace openshell-system

# Point the OpenShell gateway at it
openshell-gateway --compute-driver-socket /var/run/openshell-driver.sock
```

## OpenShift-specific features

The driver extends the base K8s compute driver with:

- **Kagenti auto-enrollment**: Sets `kagenti.io/type: agent` label on all sandbox pods, so Kagenti automatically enrolls them with SPIFFE identity, OTEL tracing, and A2A discovery
- **GPU validation**: Checks `nvidia.com/gpu` allocatable on nodes before creating GPU sandboxes
- **`platform_config` passthrough**: Supports `runtime_class_name` (for Kata/sandboxed containers), annotations, and other OpenShift-specific fields via the opaque `platform_config` struct

## Architecture

This driver communicates with the OpenShell gateway over gRPC (Unix domain socket). The gateway handles:
- Sandbox lifecycle orchestration, persistence, and reconciliation
- Policy delivery and credential resolution
- User authentication (via identity drivers)

The driver handles only platform-specific provisioning:
- Creating/deleting Sandbox CRDs
- Watching CRD status changes
- Resolving pod endpoints
