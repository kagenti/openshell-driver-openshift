# OpenShell Compute Driver for OpenShift: Design Spec

**Date:** 2026-04-21
**Author:** Adel Zaalouk
**Status:** Approved

## 1. What This Is

An out-of-process OpenShell compute driver written in Go that provisions agent sandboxes on OpenShift clusters. It implements the `ComputeDriver` gRPC contract (`compute_driver.proto`, 9 RPCs) and communicates with the OpenShell gateway over a Unix domain socket.

The driver is a reference implementation that proves external drivers work in any language, demonstrates Kagenti/agent-sandbox integration, and provides OpenShift-specific features that don't belong in the upstream Rust K8s driver. Over time it becomes the primary driver for RHOAI deployments.

## 2. Relationship to Upstream

The upstream Rust K8s driver (`crates/openshell-driver-kubernetes/`) runs in-process with the gateway and targets vanilla Kubernetes. This Go driver runs out-of-process and targets OpenShift specifically.

The upstream driver remains the default for vanilla K8s. This driver is the recommended choice when running on OpenShift, where SCC constraints, SELinux policy, Routes, OAuth proxy, and Kagenti integration matter.

The gateway today cannot connect to an external driver socket. This design includes a minimal fork of the gateway that adds `--compute-driver-socket` support (~20 lines of Rust). The fork is designed to be upstreamed when ready.

## 3. Architecture

```
openshell-gateway (forked)
         |
         | gRPC over Unix domain socket
         v
openshell-driver-openshift (Go binary)
         |
         |--- SandboxProvisioner (core, Phase 1)
         |    Creates/deletes/watches Sandbox CRDs
         |    Injects supervisor via init container
         |    Resolves pod endpoints
         |
         |--- PlatformEnricher (OpenShift, Phase 2)
         |    SCC detection
         |    SELinux context selection
         |    Route management
         |    OAuth proxy injection
         |
         |--- DriverMetrics (observability, Phase 2)
              Prometheus counters/gauges
              ServiceMonitor CR creation
```

Three interfaces, composed by the gRPC server:

```go
type Driver struct {
    pb.UnimplementedComputeDriverServer
    provisioner SandboxProvisioner
    enricher    PlatformEnricher
    metrics     DriverMetrics
    logger      *slog.Logger
}
```

Phase 1 implements `SandboxProvisioner` fully. `PlatformEnricher` and `DriverMetrics` ship as no-op stubs that satisfy the interface.

### Interfaces

```go
// Core: sandbox lifecycle on K8s. Implemented Phase 1.
type SandboxProvisioner interface {
    Create(ctx context.Context, sb *DriverSandbox) error
    Delete(ctx context.Context, name string) error
    Get(ctx context.Context, name string) (*DriverSandbox, error)
    List(ctx context.Context) ([]*DriverSandbox, error)
    Watch(ctx context.Context) (<-chan WatchEvent, error)
    ResolveEndpoint(ctx context.Context, sb *DriverSandbox) (*Endpoint, error)
    ValidateCreate(ctx context.Context, sb *DriverSandbox) error
    HasGPUCapacity(ctx context.Context) (bool, error)
}

// OpenShift extensions. Interface defined Phase 1, implemented Phase 2.
type PlatformEnricher interface {
    DetectSCC(ctx context.Context, namespace string) (string, error)
    DetectSELinuxType(ctx context.Context, namespace string) (string, error)
    CreateRoute(ctx context.Context, sandbox string, port int32) error
    DeleteRoute(ctx context.Context, sandbox string) error
    InjectOAuthProxy(podSpec map[string]interface{}) map[string]interface{}
}

// Observability. Interface defined Phase 1, implemented Phase 2.
type DriverMetrics interface {
    SandboxCreated(name string, gpu bool, duration time.Duration)
    SandboxDeleted(name string)
    SandboxFailed(name string, reason string)
    WatchEventReceived(eventType string)
    ActiveSandboxes() int
}
```

## 4. Supervisor Injection

The supervisor binary (`openshell-sandbox`) is the security boundary inside every sandbox pod. It sets up network namespaces, Landlock filesystem enforcement, seccomp syscall filtering, the L7 proxy with OPA policy, and credential placeholder rewriting. The driver is responsible for getting it into the pod.

### Why init container, not hostPath

The upstream Rust K8s driver uses a hostPath volume mount from `/opt/openshell/bin/` on the node. This requires the supervisor binary to be pre-staged on every node (via DaemonSet or baked into the node image) and requires `privileged` or a custom SCC on OpenShift because `restricted-v2` blocks hostPath mounts.

This driver uses an init container + emptyDir volume instead:

1. An init container pulls the supervisor image and copies the binary into a shared emptyDir
2. The agent container mounts the emptyDir read-only and runs the supervisor as its entrypoint
3. No hostPath, no node access, works with less elevated SCCs

The trade-off is one extra image pull per sandbox creation (the supervisor image, ~15MB). This is negligible compared to the agent image pull and avoids requiring a DaemonSet or node-level changes.

### Pod spec produced by the driver

```yaml
initContainers:
  - name: supervisor-init
    image: {{ supervisorImage }}
    command: ["cp", "{{ supervisorBinaryPath }}", "{{ supervisorMountPath }}/"]
    volumeMounts:
      - name: supervisor-bin
        mountPath: {{ supervisorMountPath }}

containers:
  - name: agent
    image: {{ sandboxImage }}
    command: ["{{ supervisorMountPath }}/openshell-sandbox"]
    securityContext:
      runAsUser: 0
      capabilities:
        add: [SYS_ADMIN, NET_ADMIN, SYS_PTRACE, SYSLOG]
    volumeMounts:
      - name: supervisor-bin
        mountPath: {{ supervisorMountPath }}
        readOnly: true

volumes:
  - name: supervisor-bin
    emptyDir: {}
```

### Configurable flags

| Flag | Default | Purpose |
|---|---|---|
| `--supervisor-image` | `ghcr.io/nvidia/openshell-community/supervisor:latest` | OCI image containing the supervisor binary |
| `--supervisor-binary-path` | `/usr/local/bin/openshell-sandbox` | Path to the binary inside the supervisor image |
| `--supervisor-mount-path` | `/opt/openshell/bin` | Mount point in the agent container |

### Startup sequence inside the pod

1. Init container copies supervisor binary into emptyDir (~1 second)
2. Agent container starts with command `/opt/openshell/bin/openshell-sandbox`
3. Supervisor runs as root (runAsUser: 0)
4. Supervisor creates network namespace + veth pair
5. Supervisor starts L7 proxy, loads OPA policy, generates ephemeral TLS CA
6. Supervisor installs Landlock filesystem rules and seccomp BPF filters
7. Supervisor connects outbound to gateway (gRPC) for policy and credentials
8. Supervisor forks, drops privileges, applies Landlock + seccomp, sets HTTPS_PROXY, execs the agent command
9. Agent process runs inside the sandbox with restricted network, filesystem, and syscall access

The driver creates the pod spec. Everything from step 2 onward is owned by the supervisor (OpenShell), not the driver.

## 5. SELinux Integration

The driver does not own SELinux policy. It requests the appropriate SELinux context via `seLinuxOptions` in the pod spec. The platform (OpenShift SCCs, RHEL, AgenticOS) provides and enforces the policy.

### Three options considered

**Option A: OpenShell ships SELinux policy modules.** The driver/operator deploys `.pp` policy modules on the host defining `openshell_agent_t` and `openshell_supervisor_t`. The supervisor uses `setexeccon()` to label the agent process. This works but turns OpenShell into a host component, not just an in-container supervisor.

**Option B: Use existing container SELinux types.** Rely on OpenShift's default `container_t` label. No customization, no host changes, but only coarse-grained container-level SELinux, not per-agent fine-grained policy.

**Option C (chosen): Compute driver requests SELinux context.** The driver sets `seLinuxOptions.type` in the pod spec. If a custom `openshell_agent_t` policy exists on the cluster, the driver requests it. If not, it falls back to the platform default. The SELinux policy modules themselves are managed by whoever owns the host: AgenticOS on RHEL, OpenShift SCCs on K8s.

### Why Option C

OpenShell is an in-container supervisor. Its isolation primitives (Landlock, seccomp, network namespaces) are self-applicable: a process can restrict itself without host privileges. SELinux is different. SELinux policy is compiled at the host level and loaded at boot. A container process cannot create new policy modules or set its own label without `CAP_MAC_ADMIN`.

Option C preserves the boundary: OpenShell handles in-container isolation, the platform handles host-level MAC. The driver just asks for the right label.

### Implementation

The `PlatformEnricher.DetectSELinuxType()` method (Phase 2):

1. Checks if the namespace's SCC allows `seLinuxOptions`
2. Checks if `openshell_agent_t` policy exists on the cluster (via MachineConfig or node labels)
3. Returns `"openshell_agent_t"` if available, `""` (platform default) if not

Phase 1 stub returns `""` (use platform default). On OpenShift, this is already `container_t` with SELinux enforcing.

### Across deployment targets

| Platform | Policy provider | Driver action |
|---|---|---|
| OpenShift (default) | SCCs assign `container_t` | Nothing (Phase 1). Requests custom type if detected (Phase 2). |
| OpenShift + custom policy | Admin deploys `openshell_agent_t` module | Detects and requests via `seLinuxOptions` |
| RHEL + AgenticOS | AgenticOS ships policy modules | Not this driver (podman driver would use `--security-opt label=type:`) |
| Vanilla K8s | No SELinux | Driver does nothing |

## 6. Kagenti Auto-Enrollment

The driver sets `kagenti.io/type: agent` on every sandbox pod's labels. When Kagenti is installed on the cluster:

1. Kagenti's AuthBridge webhook sees the label, injects sidecars (envoy, spiffe-helper, otel-collector)
2. AgentCard controller auto-creates an A2A agent card
3. NetworkPolicy controller applies trust-based networking

When Kagenti is not installed, the label is ignored. No coupling.

The driver also sets `openshell.ai/managed-by: openshell` so Kagenti can detect OpenShell-managed workloads and skip AuthBridge identity injection (the supervisor handles identity).

## 7. Gateway Fork

Fork `github.com/NVIDIA/OpenShell` into `github.com/zanetworker/OpenShell`. The driver repo (`github.com/zanetworker/openshell-driver-openshift`) and the gateway fork are separate repos.

### Changes (~20 lines in 2 files)

**`crates/openshell-core/src/config.rs`:**
- Add `External` variant to `ComputeDriverKind` enum
- Add `compute_driver_socket: Option<PathBuf>` config field
- Wire to CLI flag `--compute-driver-socket` and env var `OPENSHELL_COMPUTE_DRIVER_SOCKET`

**`crates/openshell-server/src/lib.rs`:**
- Add arm in `build_compute_runtime()`:
  ```rust
  External => {
      let channel = connect_compute_driver(&config.compute_driver_socket).await?;
      ComputeRuntime::new_remote_vm(channel, None, store, ...).await
  }
  ```

### What we do NOT change

Supervisor internals, policy engine, credential proxy, CLI, inference routing, or any other gateway component. The fork is minimal and tracks upstream.

### End-to-end test flow

```bash
# Terminal 1: start Go driver
./openshell-driver-openshift --socket /tmp/driver.sock --namespace openshell-system

# Terminal 2: start forked gateway
cargo run -p openshell-server -- --compute-driver-socket /tmp/driver.sock

# Terminal 3: create a sandbox
openshell sandbox create -- claude
```

## 8. Testing Strategy

### Tier 1: Unit tests (fake K8s clients)

Test helpers, input validation, error paths, conversion logic. No cluster needed. Currently 18 tests, expanding with interface refactor.

### Tier 2: gRPC contract tests

Start the driver binary on a temporary UDS, connect a Go gRPC test client, exercise all 9 RPCs against fake K8s clients. Proves gRPC server starts, accepts connections, routes correctly, returns proper status codes. No cluster needed.

### Tier 3: Integration tests (real cluster)

Real K8s clients, real Sandbox CRDs, real pods. Build tag `integration`. Run with `go test ./... -tags integration -timeout 120s`.

Tests:
- Create sandbox, verify pod starts with supervisor init container
- Verify `kagenti.io/type=agent` and `openshell.ai/managed-by=openshell` labels
- Verify endpoint resolution returns real pod IP
- Delete sandbox, verify pod and CRD cleanup
- GPU validation against actual node capacity
- WatchSandboxes stream receives events for create/delete

### Tier 4: End-to-end with forked gateway (Phase 2)

Start forked gateway + Go driver + CLI. Create sandbox, connect, run command, verify policy enforcement. Depends on gateway fork.

### What ships per phase

| Phase | Tiers |
|---|---|
| Phase 1 | Tiers 1, 2, 3 |
| Phase 2 | + Tier 4 |

## 9. Project Structure

```
openshell-driver-openshift/
├── proto/
│   └── compute_driver.proto
├── gen/computev1/
│   ├── compute_driver.pb.go
│   └── compute_driver_grpc.pb.go
├── cmd/driver/
│   └── main.go
├── internal/
│   ├── driver/
│   │   ├── driver.go                 # gRPC server, composes interfaces
│   │   ├── provisioner.go            # SandboxProvisioner implementation
│   │   ├── provisioner_test.go       # unit tests
│   │   ├── enricher.go               # PlatformEnricher interface + noop
│   │   ├── enricher_openshift.go     # real implementation (Phase 2)
│   │   ├── metrics.go                # DriverMetrics interface + noop
│   │   ├── metrics_prometheus.go     # real implementation (Phase 2)
│   │   ├── helpers.go                # K8s <-> proto conversion
│   │   └── helpers_test.go           # conversion tests
│   └── grpctest/
│       └── contract_test.go          # Tier 2 gRPC contract tests
├── test/
│   └── integration/
│       └── lifecycle_test.go         # Tier 3 cluster integration tests
├── deploy/
│   └── helm/
│       └── openshell-driver-openshift/  # Phase 2
├── docs/
│   ├── specs/
│   │   └── 2026-04-21-openshift-compute-driver-design.md
│   └── why-init-container.md
├── Makefile
├── go.mod
└── README.md
```

## 10. Delivery Phases

### Phase 1: Core + Test

- Refactor existing code into `SandboxProvisioner` interface
- Add `PlatformEnricher` and `DriverMetrics` as noop stubs
- Supervisor injection via init container + emptyDir
- Fork gateway, add `--compute-driver-socket`
- Tier 1 unit tests (expanded)
- Tier 2 gRPC contract tests
- Tier 3 integration tests against real cluster
- Documentation: README, why-init-container.md

**Exit criteria:** `openshell sandbox create -- claude` works end-to-end with forked gateway and Go driver against a real OpenShift cluster.

### Phase 2: OpenShift + Observability

- `PlatformEnricher` real implementation: SCC detection, SELinux context, Routes, OAuth proxy
- `DriverMetrics` real implementation: Prometheus counters/gauges, ServiceMonitor CR
- Helm chart for driver deployment
- Tier 4 end-to-end tests with forked gateway
- Kagenti integration testing (verify auto-enrollment, AgentCard creation)

**Exit criteria:** Helm-deployed driver with OpenShift-aware pod specs, Prometheus metrics visible in cluster monitoring.

### Phase 3: Production

- OLM operator (managed by RHOAI DataScienceCluster)
- ImageStream triggers for supervisor image updates
- Upstream the `--compute-driver-socket` gateway patch to NVIDIA/OpenShell
- RHOAI integration testing

**Exit criteria:** RHOAI manages the driver lifecycle via DataScienceCluster CR.

## 11. Risks

| Risk | Mitigation |
|---|---|
| Upstream gateway rejects the `--compute-driver-socket` patch | Fork tracks upstream. The TODO comments in the codebase explicitly call out external driver support as the planned direction. |
| agent-sandbox CRD API changes | Pin CRD version (`v1alpha1`). Watch upstream for breaking changes. |
| Supervisor binary incompatible with OpenShift security profile | The supervisor needs `SYS_ADMIN`, `NET_ADMIN`, `SYS_PTRACE`, `SYSLOG` capabilities. Verify these are allowed under the target SCC. `restricted-v2` will NOT work; a custom SCC is required for the agent container. |
| Init container image pull adds latency | Supervisor image is ~15MB. Negligible compared to agent image pull. Image pull policy `IfNotPresent` caches after first pull per node. |
| Kagenti not installed on target cluster | Driver sets labels regardless. Labels are ignored when no controller watches them. No runtime error. |
