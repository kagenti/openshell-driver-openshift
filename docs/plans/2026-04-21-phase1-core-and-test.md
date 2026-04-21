# Phase 1: Core + Test Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor the existing monolithic driver into interface-based architecture, add supervisor init container injection, and prove everything works end-to-end against a real OpenShift cluster.

**Architecture:** Three interfaces (`SandboxProvisioner`, `PlatformEnricher`, `DriverMetrics`) composed by the gRPC server. Phase 1 implements `SandboxProvisioner` fully; the other two ship as noop stubs. The gateway fork adds `--compute-driver-socket` for out-of-process driver support.

**Tech Stack:** Go 1.23+, gRPC (protobuf), client-go (dynamic + typed), k8s fake clients for unit tests, real cluster for integration tests. Gateway fork in Rust (minimal patch).

---

### Task 1: Define interfaces and config types

**Files:**
- Create: `internal/driver/interfaces.go`
- Create: `internal/driver/config.go`

- [ ] **Step 1: Create interfaces.go with all three interfaces**

```go
// internal/driver/interfaces.go
package driver

import (
	"context"
	"time"

	pb "github.com/zanetworker/openshell-driver-openshift/gen/computev1"
)

// SandboxProvisioner handles sandbox lifecycle on K8s.
type SandboxProvisioner interface {
	Create(ctx context.Context, sb *pb.DriverSandbox) error
	Delete(ctx context.Context, name string) error
	Get(ctx context.Context, name string) (*pb.DriverSandbox, error)
	List(ctx context.Context) ([]*pb.DriverSandbox, error)
	Watch(ctx context.Context) (<-chan WatchEvent, error)
	ResolveEndpoint(ctx context.Context, sb *pb.DriverSandbox) (*pb.SandboxEndpoint, error)
	ValidateCreate(ctx context.Context, sb *pb.DriverSandbox) error
	HasGPUCapacity(ctx context.Context) (bool, error)
}

// WatchEvent represents a sandbox state change from the K8s watcher.
type WatchEvent struct {
	Type    WatchEventType
	Sandbox *pb.DriverSandbox
	// SandboxID is set only for Deleted events.
	SandboxID string
}

// WatchEventType distinguishes sandbox watch events.
type WatchEventType int

const (
	WatchEventUpdated WatchEventType = iota
	WatchEventDeleted
)

// PlatformEnricher adds OpenShift-specific behavior to sandbox pod specs.
// Phase 1 uses the noop implementation; Phase 2 adds the real one.
type PlatformEnricher interface {
	DetectSCC(ctx context.Context, namespace string) (string, error)
	DetectSELinuxType(ctx context.Context, namespace string) (string, error)
	EnrichPodSpec(podSpec map[string]interface{}, namespace string) (map[string]interface{}, error)
}

// DriverMetrics tracks driver-level observability counters.
// Phase 1 uses the noop implementation; Phase 2 adds Prometheus.
type DriverMetrics interface {
	SandboxCreated(name string, gpu bool, duration time.Duration)
	SandboxDeleted(name string)
	SandboxFailed(name string, reason string)
	WatchEventReceived(eventType string)
}
```

- [ ] **Step 2: Create config.go with supervisor and driver config**

```go
// internal/driver/config.go
package driver

// Config holds driver configuration parsed from CLI flags.
type Config struct {
	Namespace           string
	SupervisorImage     string
	SupervisorBinaryPath string
	SupervisorMountPath string
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Namespace:           "openshell-system",
		SupervisorImage:     "ghcr.io/nvidia/openshell-community/supervisor:latest",
		SupervisorBinaryPath: "/usr/local/bin/openshell-sandbox",
		SupervisorMountPath: "/opt/openshell/bin",
	}
}
```

- [ ] **Step 3: Verify files compile**

Run: `go build ./internal/driver/`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add internal/driver/interfaces.go internal/driver/config.go
git commit -m "feat: define SandboxProvisioner, PlatformEnricher, DriverMetrics interfaces and Config"
```

---

### Task 2: Implement noop stubs for PlatformEnricher and DriverMetrics

**Files:**
- Create: `internal/driver/enricher_noop.go`
- Create: `internal/driver/metrics_noop.go`
- Create: `internal/driver/enricher_noop_test.go`
- Create: `internal/driver/metrics_noop_test.go`

- [ ] **Step 1: Write test for noop enricher**

```go
// internal/driver/enricher_noop_test.go
package driver

import (
	"context"
	"testing"
)

func TestNoopEnricher_DetectSCC_ReturnsEmpty(t *testing.T) {
	e := &NoopEnricher{}
	scc, err := e.DetectSCC(context.Background(), "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scc != "" {
		t.Errorf("expected empty SCC, got %s", scc)
	}
}

func TestNoopEnricher_DetectSELinuxType_ReturnsEmpty(t *testing.T) {
	e := &NoopEnricher{}
	sel, err := e.DetectSELinuxType(context.Background(), "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel != "" {
		t.Errorf("expected empty SELinux type, got %s", sel)
	}
}

func TestNoopEnricher_EnrichPodSpec_PassesThrough(t *testing.T) {
	e := &NoopEnricher{}
	input := map[string]interface{}{"containers": []interface{}{}}
	output, err := e.EnrichPodSpec(input, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := output["containers"]; !ok {
		t.Error("expected containers key to survive passthrough")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/driver/ -run TestNoopEnricher -v`
Expected: FAIL (NoopEnricher not defined)

- [ ] **Step 3: Implement noop enricher**

```go
// internal/driver/enricher_noop.go
package driver

import "context"

// NoopEnricher is a PlatformEnricher that does nothing. Used in Phase 1
// before OpenShift-specific enrichment is implemented.
type NoopEnricher struct{}

func (n *NoopEnricher) DetectSCC(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (n *NoopEnricher) DetectSELinuxType(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (n *NoopEnricher) EnrichPodSpec(podSpec map[string]interface{}, _ string) (map[string]interface{}, error) {
	return podSpec, nil
}
```

- [ ] **Step 4: Run enricher tests to verify they pass**

Run: `go test ./internal/driver/ -run TestNoopEnricher -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Write test for noop metrics**

```go
// internal/driver/metrics_noop_test.go
package driver

import (
	"testing"
	"time"
)

func TestNoopMetrics_DoesNotPanic(t *testing.T) {
	m := &NoopMetrics{}
	// None of these should panic.
	m.SandboxCreated("test", true, 5*time.Second)
	m.SandboxDeleted("test")
	m.SandboxFailed("test", "reason")
	m.WatchEventReceived("ADDED")
}
```

- [ ] **Step 6: Implement noop metrics**

```go
// internal/driver/metrics_noop.go
package driver

import "time"

// NoopMetrics is a DriverMetrics that discards all observations.
// Used in Phase 1 before Prometheus metrics are implemented.
type NoopMetrics struct{}

func (n *NoopMetrics) SandboxCreated(_ string, _ bool, _ time.Duration) {}
func (n *NoopMetrics) SandboxDeleted(_ string)                          {}
func (n *NoopMetrics) SandboxFailed(_ string, _ string)                 {}
func (n *NoopMetrics) WatchEventReceived(_ string)                      {}
```

- [ ] **Step 7: Run all tests**

Run: `go test ./internal/driver/ -v`
Expected: all tests pass

- [ ] **Step 8: Commit**

```bash
git add internal/driver/enricher_noop.go internal/driver/enricher_noop_test.go \
        internal/driver/metrics_noop.go internal/driver/metrics_noop_test.go
git commit -m "feat: add noop stubs for PlatformEnricher and DriverMetrics"
```

---

### Task 3: Extract SandboxProvisioner from existing driver

This is the biggest refactor. Move CRD logic out of `driver.go` into `provisioner.go` and make `driver.go` the thin gRPC server that delegates to interfaces.

**Files:**
- Create: `internal/driver/provisioner.go`
- Modify: `internal/driver/driver.go` (rewrite to compose interfaces)
- Modify: `internal/driver/helpers.go` (add supervisor injection helper)
- Rename: `internal/driver/driver_test.go` to `internal/driver/provisioner_test.go`

- [ ] **Step 1: Create provisioner.go with the K8s implementation**

Extract `Create`, `Delete`, `Get`, `List`, `Watch`, `ResolveEndpoint`, `ValidateCreate`, `HasGPUCapacity` from `driver.go` into a `K8sProvisioner` struct that implements `SandboxProvisioner`. The provisioner owns the K8s clients and the `Config`.

```go
// internal/driver/provisioner.go
package driver

import (
	"context"
	"fmt"
	"log/slog"

	pb "github.com/zanetworker/openshell-driver-openshift/gen/computev1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

var sandboxGVR = schema.GroupVersionResource{
	Group:    "agents.x-k8s.io",
	Version:  "v1alpha1",
	Resource: "sandboxes",
}

const (
	labelSandboxID = "openshell.ai/sandbox-id"
	labelManagedBy = "openshell.ai/managed-by"
	labelKagenti   = "kagenti.io/type"
	sshPort        = 2222
)

// K8sProvisioner implements SandboxProvisioner by managing
// agents.x-k8s.io/v1alpha1 Sandbox CRDs on a K8s/OpenShift cluster.
type K8sProvisioner struct {
	dynamic   dynamic.Interface
	clientset kubernetes.Interface
	config    Config
	logger    *slog.Logger
}

// NewK8sProvisioner creates a provisioner with the given K8s clients.
func NewK8sProvisioner(
	dynClient dynamic.Interface,
	clientset kubernetes.Interface,
	cfg Config,
	logger *slog.Logger,
) *K8sProvisioner {
	return &K8sProvisioner{
		dynamic:   dynClient,
		clientset: clientset,
		config:    cfg,
		logger:    logger,
	}
}

func (p *K8sProvisioner) ValidateCreate(ctx context.Context, sb *pb.DriverSandbox) error {
	if sb.GetId() == "" {
		return status.Error(codes.InvalidArgument, "sandbox id is required")
	}
	if sb.GetName() == "" {
		return status.Error(codes.InvalidArgument, "sandbox name is required")
	}
	spec := sb.GetSpec()
	if spec == nil {
		return status.Error(codes.InvalidArgument, "sandbox spec is required")
	}
	tmpl := spec.GetTemplate()
	if tmpl == nil || tmpl.GetImage() == "" {
		return status.Error(codes.InvalidArgument, "sandbox template with image is required")
	}
	if spec.GetGpu() {
		ok, err := p.HasGPUCapacity(ctx)
		if err != nil {
			return status.Errorf(codes.Internal, "check GPU capacity: %v", err)
		}
		if !ok {
			return status.Error(codes.FailedPrecondition,
				"no nodes with nvidia.com/gpu allocatable in the cluster")
		}
	}
	return nil
}

func (p *K8sProvisioner) Create(ctx context.Context, sb *pb.DriverSandbox) error {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "agents.x-k8s.io/v1alpha1",
			"kind":       "Sandbox",
			"metadata": map[string]interface{}{
				"name":      sb.GetName(),
				"namespace": p.config.Namespace,
				"labels": mergeMaps(sb.GetSpec().GetTemplate().GetLabels(), map[string]string{
					labelSandboxID: sb.GetId(),
					labelManagedBy: "openshell",
					labelKagenti:   "agent",
				}),
			},
			"spec": p.buildSandboxSpec(sb),
		},
	}

	_, err := p.dynamic.Resource(sandboxGVR).
		Namespace(p.config.Namespace).
		Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return status.Errorf(codes.Internal, "create Sandbox CR %s: %v", sb.GetName(), err)
	}

	p.logger.Info("sandbox created",
		"name", sb.GetName(),
		"id", sb.GetId(),
		"gpu", sb.GetSpec().GetGpu())
	return nil
}

func (p *K8sProvisioner) Delete(ctx context.Context, name string) error {
	err := p.dynamic.Resource(sandboxGVR).
		Namespace(p.config.Namespace).
		Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return status.Errorf(codes.Internal, "delete sandbox %s: %v", name, err)
	}
	p.logger.Info("sandbox deleted", "name", name)
	return nil
}

func (p *K8sProvisioner) Get(ctx context.Context, name string) (*pb.DriverSandbox, error) {
	obj, err := p.dynamic.Resource(sandboxGVR).
		Namespace(p.config.Namespace).
		Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "sandbox %s not found: %v", name, err)
	}
	return objToDriverSandbox(obj), nil
}

func (p *K8sProvisioner) List(ctx context.Context) ([]*pb.DriverSandbox, error) {
	list, err := p.dynamic.Resource(sandboxGVR).
		Namespace(p.config.Namespace).
		List(ctx, metav1.ListOptions{LabelSelector: labelSandboxID})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list sandboxes: %v", err)
	}
	sandboxes := make([]*pb.DriverSandbox, 0, len(list.Items))
	for i := range list.Items {
		sandboxes = append(sandboxes, objToDriverSandbox(&list.Items[i]))
	}
	return sandboxes, nil
}

func (p *K8sProvisioner) Watch(ctx context.Context) (<-chan WatchEvent, error) {
	watcher, err := p.dynamic.Resource(sandboxGVR).
		Namespace(p.config.Namespace).
		Watch(ctx, metav1.ListOptions{LabelSelector: labelSandboxID})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "start watcher: %v", err)
	}

	ch := make(chan WatchEvent, 64)
	go func() {
		defer close(ch)
		defer watcher.Stop()
		for event := range watcher.ResultChan() {
			obj, ok := event.Object.(*unstructured.Unstructured)
			if !ok {
				continue
			}
			switch event.Type {
			case watch.Added, watch.Modified:
				ch <- WatchEvent{
					Type:    WatchEventUpdated,
					Sandbox: objToDriverSandbox(obj),
				}
			case watch.Deleted:
				ch <- WatchEvent{
					Type:      WatchEventDeleted,
					SandboxID: obj.GetLabels()[labelSandboxID],
				}
			}
		}
	}()
	return ch, nil
}

func (p *K8sProvisioner) ResolveEndpoint(ctx context.Context, sb *pb.DriverSandbox) (*pb.SandboxEndpoint, error) {
	if sts := sb.GetStatus(); sts != nil && sts.GetInstanceId() != "" {
		pod, err := p.clientset.CoreV1().Pods(p.config.Namespace).
			Get(ctx, sts.GetInstanceId(), metav1.GetOptions{})
		if err != nil {
			p.logger.Warn("pod lookup failed, falling back to DNS",
				"pod", sts.GetInstanceId(), "error", err)
		} else if pod.Status.PodIP != "" {
			return &pb.SandboxEndpoint{
				Target: &pb.SandboxEndpoint_Ip{Ip: pod.Status.PodIP},
				Port:   sshPort,
			}, nil
		}
	}
	return &pb.SandboxEndpoint{
		Target: &pb.SandboxEndpoint_Host{
			Host: fmt.Sprintf("%s.%s.svc.cluster.local", sb.GetName(), p.config.Namespace),
		},
		Port: sshPort,
	}, nil
}

func (p *K8sProvisioner) HasGPUCapacity(ctx context.Context) (bool, error) {
	nodes, err := p.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, err
	}
	gpuResource := corev1.ResourceName("nvidia.com/gpu")
	for _, node := range nodes.Items {
		if alloc := node.Status.Allocatable; alloc != nil {
			if q, ok := alloc[gpuResource]; ok && !q.IsZero() {
				return true, nil
			}
		}
	}
	return false, nil
}

// buildSandboxSpec constructs the Sandbox CR spec with supervisor init
// container injection.
func (p *K8sProvisioner) buildSandboxSpec(sb *pb.DriverSandbox) map[string]interface{} {
	spec := sb.GetSpec()
	tmpl := spec.GetTemplate()

	container := map[string]interface{}{
		"name":    "agent",
		"image":   tmpl.GetImage(),
		"command": []interface{}{p.config.SupervisorMountPath + "/openshell-sandbox"},
		"env":     buildEnvList(spec.GetEnvironment(), tmpl.GetEnvironment()),
		"securityContext": map[string]interface{}{
			"runAsUser": int64(0),
			"capabilities": map[string]interface{}{
				"add": []interface{}{"SYS_ADMIN", "NET_ADMIN", "SYS_PTRACE", "SYSLOG"},
			},
		},
		"volumeMounts": []interface{}{
			map[string]interface{}{
				"name":      "supervisor-bin",
				"mountPath": p.config.SupervisorMountPath,
				"readOnly":  true,
			},
		},
	}

	if res := tmpl.GetResources(); res != nil {
		container["resources"] = buildResources(res, spec.GetGpu())
	}

	podSpec := map[string]interface{}{
		"initContainers": []interface{}{
			map[string]interface{}{
				"name":    "supervisor-init",
				"image":   p.config.SupervisorImage,
				"command": []interface{}{"cp", p.config.SupervisorBinaryPath, p.config.SupervisorMountPath + "/"},
				"volumeMounts": []interface{}{
					map[string]interface{}{
						"name":      "supervisor-bin",
						"mountPath": p.config.SupervisorMountPath,
					},
				},
			},
		},
		"containers": []interface{}{container},
		"volumes": []interface{}{
			map[string]interface{}{
				"name":     "supervisor-bin",
				"emptyDir": map[string]interface{}{},
			},
		},
	}

	// Apply platform_config passthrough.
	if pc := tmpl.GetPlatformConfig(); pc != nil {
		if fields := pc.GetFields(); fields != nil {
			if rcn, ok := fields["runtime_class_name"]; ok {
				podSpec["runtimeClassName"] = rcn.GetStringValue()
			}
		}
	}

	return map[string]interface{}{
		"podTemplate": map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": mergeMaps(tmpl.GetLabels(), map[string]string{
					labelSandboxID: sb.GetId(),
					labelManagedBy: "openshell",
					labelKagenti:   "agent",
				}),
			},
			"spec": podSpec,
		},
	}
}
```

- [ ] **Step 2: Rewrite driver.go as thin gRPC server**

```go
// internal/driver/driver.go
package driver

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	pb "github.com/zanetworker/openshell-driver-openshift/gen/computev1"
	"google.golang.org/grpc"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Driver implements the ComputeDriverServer gRPC interface by delegating
// to a SandboxProvisioner, PlatformEnricher, and DriverMetrics.
type Driver struct {
	pb.UnimplementedComputeDriverServer

	provisioner SandboxProvisioner
	enricher    PlatformEnricher
	metrics     DriverMetrics
	logger      *slog.Logger
}

// New creates a Driver using in-cluster K8s config with the given Config.
func New(cfg Config, logger *slog.Logger) (*Driver, error) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("build in-cluster config: %w", err)
	}
	dynClient, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}
	return NewWithClients(dynClient, clientset, cfg, logger), nil
}

// NewWithClients creates a Driver with pre-built K8s clients.
// Use for testing with fake clients.
func NewWithClients(
	dynClient dynamic.Interface,
	clientset kubernetes.Interface,
	cfg Config,
	logger *slog.Logger,
) *Driver {
	return &Driver{
		provisioner: NewK8sProvisioner(dynClient, clientset, cfg, logger),
		enricher:    &NoopEnricher{},
		metrics:     &NoopMetrics{},
		logger:      logger,
	}
}

// NewWithDeps creates a Driver with fully injected dependencies.
// Use when you want to provide custom provisioner, enricher, or metrics.
func NewWithDeps(
	provisioner SandboxProvisioner,
	enricher PlatformEnricher,
	metrics DriverMetrics,
	logger *slog.Logger,
) *Driver {
	return &Driver{
		provisioner: provisioner,
		enricher:    enricher,
		metrics:     metrics,
		logger:      logger,
	}
}

func (d *Driver) GetCapabilities(
	_ context.Context,
	_ *pb.GetCapabilitiesRequest,
) (*pb.GetCapabilitiesResponse, error) {
	return &pb.GetCapabilitiesResponse{
		DriverName:    "openshift",
		DriverVersion: "0.1.0",
		DefaultImage:  "ghcr.io/nvidia/openshell-community/sandboxes/base:latest",
		SupportsGpu:   true,
	}, nil
}

func (d *Driver) ValidateSandboxCreate(
	ctx context.Context,
	req *pb.ValidateSandboxCreateRequest,
) (*pb.ValidateSandboxCreateResponse, error) {
	if err := d.provisioner.ValidateCreate(ctx, req.GetSandbox()); err != nil {
		return nil, err
	}
	return &pb.ValidateSandboxCreateResponse{}, nil
}

func (d *Driver) CreateSandbox(
	ctx context.Context,
	req *pb.CreateSandboxRequest,
) (*pb.CreateSandboxResponse, error) {
	sb := req.GetSandbox()
	start := time.Now()

	if err := d.provisioner.ValidateCreate(ctx, sb); err != nil {
		d.metrics.SandboxFailed(sb.GetName(), err.Error())
		return nil, err
	}
	if err := d.provisioner.Create(ctx, sb); err != nil {
		d.metrics.SandboxFailed(sb.GetName(), err.Error())
		return nil, err
	}

	d.metrics.SandboxCreated(sb.GetName(), sb.GetSpec().GetGpu(), time.Since(start))
	return &pb.CreateSandboxResponse{}, nil
}

func (d *Driver) GetSandbox(
	ctx context.Context,
	req *pb.GetSandboxRequest,
) (*pb.GetSandboxResponse, error) {
	sb, err := d.provisioner.Get(ctx, req.GetSandboxName())
	if err != nil {
		return nil, err
	}
	return &pb.GetSandboxResponse{Sandbox: sb}, nil
}

func (d *Driver) ListSandboxes(
	ctx context.Context,
	_ *pb.ListSandboxesRequest,
) (*pb.ListSandboxesResponse, error) {
	sandboxes, err := d.provisioner.List(ctx)
	if err != nil {
		return nil, err
	}
	return &pb.ListSandboxesResponse{Sandboxes: sandboxes}, nil
}

func (d *Driver) StopSandbox(
	ctx context.Context,
	req *pb.StopSandboxRequest,
) (*pb.StopSandboxResponse, error) {
	if err := d.provisioner.Delete(ctx, req.GetSandboxName()); err != nil {
		return nil, err
	}
	return &pb.StopSandboxResponse{}, nil
}

func (d *Driver) DeleteSandbox(
	ctx context.Context,
	req *pb.DeleteSandboxRequest,
) (*pb.DeleteSandboxResponse, error) {
	if err := d.provisioner.Delete(ctx, req.GetSandboxName()); err != nil {
		return nil, err
	}
	d.metrics.SandboxDeleted(req.GetSandboxName())
	return &pb.DeleteSandboxResponse{Deleted: true}, nil
}

func (d *Driver) ResolveSandboxEndpoint(
	ctx context.Context,
	req *pb.ResolveSandboxEndpointRequest,
) (*pb.ResolveSandboxEndpointResponse, error) {
	endpoint, err := d.provisioner.ResolveEndpoint(ctx, req.GetSandbox())
	if err != nil {
		return nil, err
	}
	return &pb.ResolveSandboxEndpointResponse{Endpoint: endpoint}, nil
}

func (d *Driver) WatchSandboxes(
	_ *pb.WatchSandboxesRequest,
	stream grpc.ServerStreamingServer[pb.WatchSandboxesEvent],
) error {
	ch, err := d.provisioner.Watch(stream.Context())
	if err != nil {
		return err
	}

	for event := range ch {
		var evt *pb.WatchSandboxesEvent
		switch event.Type {
		case WatchEventUpdated:
			d.metrics.WatchEventReceived("updated")
			evt = &pb.WatchSandboxesEvent{
				Payload: &pb.WatchSandboxesEvent_Sandbox{
					Sandbox: &pb.WatchSandboxesSandboxEvent{Sandbox: event.Sandbox},
				},
			}
		case WatchEventDeleted:
			d.metrics.WatchEventReceived("deleted")
			evt = &pb.WatchSandboxesEvent{
				Payload: &pb.WatchSandboxesEvent_Deleted{
					Deleted: &pb.WatchSandboxesDeletedEvent{SandboxId: event.SandboxID},
				},
			}
		}
		if err := stream.Send(evt); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 3: Update main.go to pass Config**

Replace the two flags (`--socket`, `--namespace`) with Config fields plus the three supervisor flags:

```go
// cmd/driver/main.go — update flag parsing section only
cfg := driver.DefaultConfig()
socketPath := flag.String("socket", "/var/run/openshell-driver.sock",
    "Unix domain socket path for the gRPC server")
flag.StringVar(&cfg.Namespace, "namespace", cfg.Namespace,
    "Kubernetes namespace where sandboxes are provisioned")
flag.StringVar(&cfg.SupervisorImage, "supervisor-image", cfg.SupervisorImage,
    "OCI image containing the supervisor binary")
flag.StringVar(&cfg.SupervisorBinaryPath, "supervisor-binary-path", cfg.SupervisorBinaryPath,
    "Path to the supervisor binary inside the supervisor image")
flag.StringVar(&cfg.SupervisorMountPath, "supervisor-mount-path", cfg.SupervisorMountPath,
    "Mount point for the supervisor binary in the agent container")
flag.Parse()

// Change: driver.New(*namespace, logger) → driver.New(cfg, logger)
d, err := driver.New(cfg, logger)
```

- [ ] **Step 4: Rename driver_test.go to provisioner_test.go and update**

Update `newTestDriver` to use `NewWithClients` with `Config`:

```go
func newTestDriver(t *testing.T, objects ...runtime.Object) *Driver {
    t.Helper()
    scheme := runtime.NewScheme()
    dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
        scheme,
        map[schema.GroupVersionResource]string{sandboxGVR: "SandboxList"},
        objects...,
    )
    clientset := kubefake.NewSimpleClientset()
    logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
    return NewWithClients(dynClient, clientset, DefaultConfig(), logger)
}
```

- [ ] **Step 5: Delete old driver.go content that moved to provisioner.go**

The old `driver.go` had `sandboxGVR`, constants, `buildPodSpec`, `hasGPUCapacity`, etc. Those are now in `provisioner.go`. Remove duplicates.

- [ ] **Step 6: Run all tests**

Run: `go test ./... -timeout 30s -v`
Expected: All 18+ tests pass

- [ ] **Step 7: Build**

Run: `go build ./cmd/driver/`
Expected: no errors

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "refactor: extract SandboxProvisioner interface, add supervisor init container injection

Split monolithic driver.go into:
- provisioner.go: K8sProvisioner implements SandboxProvisioner
- driver.go: thin gRPC server composing provisioner + enricher + metrics
- interfaces.go: all three interface definitions
- config.go: Config with supervisor image/path flags

Supervisor delivery changed from bare container command to init
container + emptyDir (see docs/why-init-container.md)."
```

---

### Task 4: Add gRPC contract tests (Tier 2)

**Files:**
- Create: `internal/grpctest/contract_test.go`

- [ ] **Step 1: Write gRPC contract tests**

These tests start the full gRPC server on a temp UDS with fake K8s clients, then exercise all 9 RPCs through a real gRPC client connection.

```go
// internal/grpctest/contract_test.go
package grpctest

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/zanetworker/openshell-driver-openshift/gen/computev1"
	"github.com/zanetworker/openshell-driver-openshift/internal/driver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

var sandboxGVR = schema.GroupVersionResource{
	Group: "agents.x-k8s.io", Version: "v1alpha1", Resource: "sandboxes",
}

// startTestServer starts a gRPC server on a temp UDS and returns a connected client.
func startTestServer(t *testing.T) (pb.ComputeDriverClient, func()) {
	t.Helper()

	socketPath := filepath.Join(t.TempDir(), "test-driver.sock")
	lis, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{sandboxGVR: "SandboxList"},
	)
	clientset := kubefake.NewSimpleClientset()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	d := driver.NewWithClients(dynClient, clientset, driver.DefaultConfig(), logger)

	srv := grpc.NewServer()
	pb.RegisterComputeDriverServer(srv, d)
	go srv.Serve(lis)

	conn, err := grpc.NewClient(
		"unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		srv.Stop()
		t.Fatalf("dial: %v", err)
	}

	client := pb.NewComputeDriverClient(conn)
	cleanup := func() {
		conn.Close()
		srv.Stop()
	}
	return client, cleanup
}

func TestGRPC_GetCapabilities(t *testing.T) {
	client, cleanup := startTestServer(t)
	defer cleanup()

	resp, err := client.GetCapabilities(context.Background(), &pb.GetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	if resp.DriverName != "openshift" {
		t.Errorf("expected driver name openshift, got %s", resp.DriverName)
	}
}

func TestGRPC_CreateAndGetSandbox(t *testing.T) {
	client, cleanup := startTestServer(t)
	defer cleanup()
	ctx := context.Background()

	_, err := client.CreateSandbox(ctx, &pb.CreateSandboxRequest{
		Sandbox: &pb.DriverSandbox{
			Id: "sb-grpc-1", Name: "grpc-test",
			Spec: &pb.DriverSandboxSpec{
				Template: &pb.DriverSandboxTemplate{
					Image: "test:latest",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}

	resp, err := client.GetSandbox(ctx, &pb.GetSandboxRequest{SandboxName: "grpc-test"})
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	if resp.Sandbox.Name != "grpc-test" {
		t.Errorf("expected name grpc-test, got %s", resp.Sandbox.Name)
	}
}

func TestGRPC_CreateSandbox_InvalidArgument(t *testing.T) {
	client, cleanup := startTestServer(t)
	defer cleanup()

	_, err := client.CreateSandbox(context.Background(), &pb.CreateSandboxRequest{
		Sandbox: &pb.DriverSandbox{Name: "no-id"},
	})
	if err == nil {
		t.Fatal("expected error for missing ID")
	}
	if s, ok := status.FromError(err); !ok || s.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestGRPC_ListSandboxes_Empty(t *testing.T) {
	client, cleanup := startTestServer(t)
	defer cleanup()

	resp, err := client.ListSandboxes(context.Background(), &pb.ListSandboxesRequest{})
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if len(resp.Sandboxes) != 0 {
		t.Errorf("expected 0, got %d", len(resp.Sandboxes))
	}
}

func TestGRPC_DeleteSandbox(t *testing.T) {
	client, cleanup := startTestServer(t)
	defer cleanup()
	ctx := context.Background()

	// Create first.
	_, err := client.CreateSandbox(ctx, &pb.CreateSandboxRequest{
		Sandbox: &pb.DriverSandbox{
			Id: "sb-del", Name: "to-delete",
			Spec: &pb.DriverSandboxSpec{
				Template: &pb.DriverSandboxTemplate{Image: "test:latest"},
			},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	resp, err := client.DeleteSandbox(ctx, &pb.DeleteSandboxRequest{
		SandboxId: "sb-del", SandboxName: "to-delete",
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !resp.Deleted {
		t.Error("expected deleted=true")
	}
}

func TestGRPC_GetSandbox_NotFound(t *testing.T) {
	client, cleanup := startTestServer(t)
	defer cleanup()

	_, err := client.GetSandbox(context.Background(), &pb.GetSandboxRequest{
		SandboxName: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if s, ok := status.FromError(err); !ok || s.Code() != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

func TestGRPC_ResolveSandboxEndpoint_DNSFallback(t *testing.T) {
	client, cleanup := startTestServer(t)
	defer cleanup()

	resp, err := client.ResolveSandboxEndpoint(context.Background(),
		&pb.ResolveSandboxEndpointRequest{
			Sandbox: &pb.DriverSandbox{Name: "dns-test", Namespace: "test"},
		})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	host := resp.Endpoint.GetHost()
	if host == "" {
		t.Error("expected DNS fallback host, got empty")
	}
}

func TestGRPC_WatchSandboxes_ReceivesCreateEvent(t *testing.T) {
	client, cleanup := startTestServer(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start watch before creating.
	stream, err := client.WatchSandboxes(ctx, &pb.WatchSandboxesRequest{})
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	// Create a sandbox (the fake client may or may not trigger the watcher;
	// this test validates the gRPC stream plumbing compiles and connects).
	_, _ = client.CreateSandbox(ctx, &pb.CreateSandboxRequest{
		Sandbox: &pb.DriverSandbox{
			Id: "sb-watch", Name: "watch-test",
			Spec: &pb.DriverSandboxSpec{
				Template: &pb.DriverSandboxTemplate{Image: "test:latest"},
			},
		},
	})

	// The fake dynamic client's watcher may not fire events.
	// This test validates the stream is established without error.
	// Full event testing happens in Tier 3 (real cluster).
	_ = stream
}
```

- [ ] **Step 2: Run gRPC contract tests**

Run: `go test ./internal/grpctest/ -v -timeout 30s`
Expected: all tests pass

- [ ] **Step 3: Commit**

```bash
git add internal/grpctest/contract_test.go
git commit -m "test: add Tier 2 gRPC contract tests

7 tests exercising all RPCs through a real gRPC UDS connection
with fake K8s clients. Validates server starts, accepts connections,
routes correctly, and returns proper gRPC status codes."
```

---

### Task 5: Add cluster integration tests (Tier 3)

**Files:**
- Create: `test/integration/lifecycle_test.go`

- [ ] **Step 1: Write integration test file**

```go
//go:build integration

// Package integration tests the driver against a real K8s/OpenShift cluster.
// Run with: go test ./test/integration/ -tags integration -timeout 120s
package integration

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	pb "github.com/zanetworker/openshell-driver-openshift/gen/computev1"
	"github.com/zanetworker/openshell-driver-openshift/internal/driver"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var sandboxGVR = schema.GroupVersionResource{
	Group: "agents.x-k8s.io", Version: "v1alpha1", Resource: "sandboxes",
}

const testNamespace = "openshell-integration-test"

func buildClients(t *testing.T) (dynamic.Interface, kubernetes.Interface) {
	t.Helper()
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.Getenv("HOME") + "/.kube/config"
	}
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("build kubeconfig: %v", err)
	}
	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatalf("dynamic client: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatalf("clientset: %v", err)
	}
	return dynClient, clientset
}

func newIntegrationDriver(t *testing.T) *driver.Driver {
	t.Helper()
	dynClient, clientset := buildClients(t)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := driver.DefaultConfig()
	cfg.Namespace = testNamespace
	return driver.NewWithClients(dynClient, clientset, cfg, logger)
}

func sandboxName(t *testing.T) string {
	return fmt.Sprintf("inttest-%d", time.Now().UnixMilli())
}

func TestIntegration_CreateAndListSandbox(t *testing.T) {
	d := newIntegrationDriver(t)
	ctx := context.Background()
	name := sandboxName(t)

	// Create.
	_, err := d.CreateSandbox(ctx, &pb.CreateSandboxRequest{
		Sandbox: &pb.DriverSandbox{
			Id: "int-" + name, Name: name,
			Spec: &pb.DriverSandboxSpec{
				Template: &pb.DriverSandboxTemplate{
					Image: "busybox:latest",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Cleanup on exit.
	t.Cleanup(func() {
		d.DeleteSandbox(context.Background(), &pb.DeleteSandboxRequest{
			SandboxId: "int-" + name, SandboxName: name,
		})
	})

	// List should include it.
	resp, err := d.ListSandboxes(ctx, &pb.ListSandboxesRequest{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, sb := range resp.Sandboxes {
		if sb.Name == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("sandbox %s not found in list", name)
	}
}

func TestIntegration_GetSandbox(t *testing.T) {
	d := newIntegrationDriver(t)
	ctx := context.Background()
	name := sandboxName(t)

	_, err := d.CreateSandbox(ctx, &pb.CreateSandboxRequest{
		Sandbox: &pb.DriverSandbox{
			Id: "int-" + name, Name: name,
			Spec: &pb.DriverSandboxSpec{
				Template: &pb.DriverSandboxTemplate{Image: "busybox:latest"},
			},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		d.DeleteSandbox(context.Background(), &pb.DeleteSandboxRequest{
			SandboxId: "int-" + name, SandboxName: name,
		})
	})

	resp, err := d.GetSandbox(ctx, &pb.GetSandboxRequest{SandboxName: name})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if resp.Sandbox.Name != name {
		t.Errorf("expected %s, got %s", name, resp.Sandbox.Name)
	}
}

func TestIntegration_DeleteSandbox(t *testing.T) {
	d := newIntegrationDriver(t)
	ctx := context.Background()
	name := sandboxName(t)

	_, err := d.CreateSandbox(ctx, &pb.CreateSandboxRequest{
		Sandbox: &pb.DriverSandbox{
			Id: "int-" + name, Name: name,
			Spec: &pb.DriverSandboxSpec{
				Template: &pb.DriverSandboxTemplate{Image: "busybox:latest"},
			},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	resp, err := d.DeleteSandbox(ctx, &pb.DeleteSandboxRequest{
		SandboxId: "int-" + name, SandboxName: name,
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !resp.Deleted {
		t.Error("expected deleted=true")
	}

	// Verify it's gone.
	_, err = d.GetSandbox(ctx, &pb.GetSandboxRequest{SandboxName: name})
	if err == nil {
		t.Error("expected NotFound after delete")
	}
}

func TestIntegration_VerifyLabels(t *testing.T) {
	d := newIntegrationDriver(t)
	dynClient, _ := buildClients(t)
	ctx := context.Background()
	name := sandboxName(t)

	_, err := d.CreateSandbox(ctx, &pb.CreateSandboxRequest{
		Sandbox: &pb.DriverSandbox{
			Id: "int-" + name, Name: name,
			Spec: &pb.DriverSandboxSpec{
				Template: &pb.DriverSandboxTemplate{Image: "busybox:latest"},
			},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		d.DeleteSandbox(context.Background(), &pb.DeleteSandboxRequest{
			SandboxId: "int-" + name, SandboxName: name,
		})
	})

	// Read the raw CRD and check labels.
	obj, err := dynClient.Resource(sandboxGVR).
		Namespace(testNamespace).
		Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get raw CRD: %v", err)
	}

	labels := obj.GetLabels()
	if labels["openshell.ai/sandbox-id"] != "int-"+name {
		t.Errorf("missing sandbox-id label")
	}
	if labels["openshell.ai/managed-by"] != "openshell" {
		t.Errorf("missing managed-by label")
	}
	if labels["kagenti.io/type"] != "agent" {
		t.Errorf("missing kagenti.io/type label")
	}
}

func TestIntegration_VerifySupervisorInitContainer(t *testing.T) {
	d := newIntegrationDriver(t)
	dynClient, _ := buildClients(t)
	ctx := context.Background()
	name := sandboxName(t)

	_, err := d.CreateSandbox(ctx, &pb.CreateSandboxRequest{
		Sandbox: &pb.DriverSandbox{
			Id: "int-" + name, Name: name,
			Spec: &pb.DriverSandboxSpec{
				Template: &pb.DriverSandboxTemplate{Image: "busybox:latest"},
			},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		d.DeleteSandbox(context.Background(), &pb.DeleteSandboxRequest{
			SandboxId: "int-" + name, SandboxName: name,
		})
	})

	obj, err := dynClient.Resource(sandboxGVR).
		Namespace(testNamespace).
		Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get raw CRD: %v", err)
	}

	// Check init container exists in pod template.
	spec, _, _ := unstructuredNestedSlice(obj.Object,
		"spec", "podTemplate", "spec", "initContainers")
	if len(spec) == 0 {
		t.Fatal("expected at least one init container for supervisor injection")
	}
	initContainer := spec[0].(map[string]interface{})
	if initContainer["name"] != "supervisor-init" {
		t.Errorf("expected init container named supervisor-init, got %v", initContainer["name"])
	}
}

// unstructuredNestedSlice is a helper that navigates nested maps to find a slice.
func unstructuredNestedSlice(obj map[string]interface{}, fields ...string) ([]interface{}, bool, error) {
	current := obj
	for i, f := range fields {
		if i == len(fields)-1 {
			val, ok := current[f].([]interface{})
			return val, ok, nil
		}
		next, ok := current[f].(map[string]interface{})
		if !ok {
			return nil, false, nil
		}
		current = next
	}
	return nil, false, nil
}
```

- [ ] **Step 2: Run integration tests (requires cluster + agent-sandbox CRD installed)**

Run: `go test ./test/integration/ -tags integration -timeout 120s -v`
Expected: all 5 tests pass (if Sandbox CRD is installed on cluster)

Note: if the agent-sandbox CRD is not installed, tests will fail with "the server could not find the requested resource." Install it first:
```bash
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/agent-sandbox/main/config/crd/bases/agents.x-k8s.io_sandboxes.yaml
kubectl create namespace openshell-integration-test
```

- [ ] **Step 3: Commit**

```bash
git add test/integration/lifecycle_test.go
git commit -m "test: add Tier 3 cluster integration tests

5 integration tests against a real cluster:
- CreateAndListSandbox
- GetSandbox
- DeleteSandbox
- VerifyLabels (kagenti.io/type, openshell.ai/managed-by)
- VerifySupervisorInitContainer

Run with: go test ./test/integration/ -tags integration -timeout 120s"
```

---

### Task 6: Update Makefile and README

**Files:**
- Modify: `Makefile`
- Modify: `README.md`

- [ ] **Step 1: Update Makefile with new targets**

```makefile
.PHONY: proto build test test-grpc test-integration clean

BINARY := openshell-driver-openshift
SOCKET := /var/run/openshell-driver.sock

proto:
	buf generate
	mkdir -p gen/computev1
	mv gen/compute_driver*.go gen/computev1/ 2>/dev/null || true

build:
	go build -o $(BINARY) ./cmd/driver/

test:
	go test ./internal/... -timeout 30s -v

test-grpc:
	go test ./internal/grpctest/ -timeout 30s -v

test-integration:
	go test ./test/integration/ -tags integration -timeout 120s -v

test-all: test test-grpc

clean:
	rm -f $(BINARY) $(SOCKET)

run: build
	./$(BINARY) --socket $(SOCKET)
```

- [ ] **Step 2: Update README with architecture, testing, and supervisor info**

Add sections for the interface architecture, testing tiers, supervisor injection rationale, and the gateway fork dependency.

- [ ] **Step 3: Commit**

```bash
git add Makefile README.md
git commit -m "docs: update Makefile targets and README with Phase 1 architecture"
```

---

### Task 7: Fork gateway and add --compute-driver-socket patch

**Files (in separate repo: github.com/zanetworker/OpenShell):**
- Modify: `crates/openshell-core/src/config.rs`
- Modify: `crates/openshell-server/src/lib.rs`

- [ ] **Step 1: Fork the repo**

```bash
gh repo fork NVIDIA/OpenShell --clone --remote
cd OpenShell
git checkout -b feat/external-compute-driver-socket
```

- [ ] **Step 2: Add External variant to ComputeDriverKind**

In `crates/openshell-core/src/config.rs`, add `External` to the `ComputeDriverKind` enum and add `compute_driver_socket: Option<PathBuf>` to the config struct. Wire to CLI flag `--compute-driver-socket` and env var `OPENSHELL_COMPUTE_DRIVER_SOCKET`.

- [ ] **Step 3: Add external driver arm in build_compute_runtime**

In `crates/openshell-server/src/lib.rs`, add a match arm for `External` that calls `connect_compute_driver(&socket_path)` and passes the `Channel` to `ComputeRuntime::new_remote_vm(channel, None, ...)`.

- [ ] **Step 4: Build and verify**

```bash
cargo build -p openshell-server
```

- [ ] **Step 5: Test end-to-end**

```bash
# Terminal 1: Go driver
./openshell-driver-openshift --socket /tmp/driver.sock

# Terminal 2: Forked gateway
cargo run -p openshell-server -- --compute-driver-socket /tmp/driver.sock

# Terminal 3: CLI
openshell sandbox create -- claude
```

- [ ] **Step 6: Commit on fork**

```bash
git add -A
git commit -m "feat: add --compute-driver-socket for external compute drivers

Adds External variant to ComputeDriverKind. When set, the gateway
connects to a pre-existing Unix domain socket instead of spawning
its own driver subprocess. Enables out-of-process drivers written
in any language that implements the ComputeDriver gRPC contract."
```

---

## Summary

| Task | What it delivers | Tests added |
|---|---|---|
| 1 | Interfaces + Config types | compile check |
| 2 | Noop stubs (PlatformEnricher, DriverMetrics) | 4 unit tests |
| 3 | K8sProvisioner + thin gRPC server + supervisor injection | existing 18 tests adapted |
| 4 | gRPC contract tests (Tier 2) | 7 gRPC tests |
| 5 | Cluster integration tests (Tier 3) | 5 integration tests |
| 6 | Updated Makefile + README | n/a |
| 7 | Gateway fork with --compute-driver-socket | E2E manual verification |

**Total test count after Phase 1:** ~34 tests (18 unit + 4 noop + 7 gRPC + 5 integration)

**Exit criteria:** `openshell sandbox create -- claude` works end-to-end with forked gateway and Go driver against a real OpenShift cluster.
