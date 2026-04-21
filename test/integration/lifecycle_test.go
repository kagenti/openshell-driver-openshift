//go:build integration

// Package integration contains tests that run against a real Kubernetes or
// OpenShift cluster. They require:
//
//   - A valid kubeconfig (KUBECONFIG env or ~/.kube/config)
//   - The agents.x-k8s.io/v1alpha1 Sandbox CRD installed
//   - A test namespace (INTEGRATION_TEST_NAMESPACE or default openshell-integration-test)
//
// Run with:
//
//	go test -tags integration -timeout 120s ./test/integration/
package integration

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/zanetworker/openshell-driver-openshift/gen/computev1"
	"github.com/zanetworker/openshell-driver-openshift/internal/driver"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// sandboxGVR mirrors the GVR defined in the driver package. We define it
// locally because the driver package does not export it.
var sandboxGVR = schema.GroupVersionResource{
	Group:    "agents.x-k8s.io",
	Version:  "v1alpha1",
	Resource: "sandboxes",
}

// testNamespace returns the namespace used for integration tests. It reads
// the INTEGRATION_TEST_NAMESPACE env var, falling back to a fixed default.
func testNamespace() string {
	if ns := os.Getenv("INTEGRATION_TEST_NAMESPACE"); ns != "" {
		return ns
	}
	return "openshell-integration-test"
}

// uniqueName generates a sandbox name unique to the test by combining the
// test name with a timestamp to avoid collisions across parallel runs.
func uniqueName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("integ-%s-%d", t.Name(), time.Now().UnixNano())
}

// kubeconfigPath returns the path to the kubeconfig file. It checks the
// KUBECONFIG env var first, then falls back to ~/.kube/config.
func kubeconfigPath() string {
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		return kc
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kube", "config")
}

// buildClients creates real K8s dynamic and typed clients from the kubeconfig.
// It skips the test if no valid kubeconfig is found.
func buildClients(t *testing.T) (dynamic.Interface, kubernetes.Interface) {
	t.Helper()

	kcPath := kubeconfigPath()
	if kcPath == "" {
		t.Skip("no kubeconfig found: set KUBECONFIG or ensure ~/.kube/config exists")
	}
	if _, err := os.Stat(kcPath); os.IsNotExist(err) {
		t.Skipf("kubeconfig not found at %s", kcPath)
	}

	config, err := clientcmd.BuildConfigFromFlags("", kcPath)
	if err != nil {
		t.Fatalf("build kubeconfig from %s: %v", kcPath, err)
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatalf("build dynamic client: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatalf("build clientset: %v", err)
	}

	return dynClient, clientset
}

// newIntegrationDriver creates a driver.Driver that targets the integration
// test namespace using real K8s clients. It also verifies that the Sandbox CRD
// is installed on the cluster, skipping the test gracefully if not.
func newIntegrationDriver(t *testing.T) (*driver.Driver, dynamic.Interface) {
	t.Helper()

	dynClient, clientset := buildClients(t)
	ns := testNamespace()

	// Verify the CRD is reachable. A quick List with limit=1 will fail with
	// a "not found" or "the server could not find the requested resource"
	// error if the CRD is not installed.
	_, err := dynClient.Resource(sandboxGVR).Namespace(ns).List(
		context.Background(),
		metav1.ListOptions{Limit: 1},
	)
	if err != nil {
		t.Skipf("Sandbox CRD not available on cluster (skipping): %v", err)
	}

	cfg := driver.DefaultConfig()
	cfg.Namespace = ns

	logger := testLogger()
	drv := driver.NewWithClients(dynClient, clientset, cfg, logger)

	return drv, dynClient
}

// testLogger returns a logger configured for integration test output.
func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
}

// createTestSandbox is a helper that creates a sandbox via the driver and
// registers a cleanup function to delete it when the test finishes.
func createTestSandbox(t *testing.T, drv *driver.Driver, id, name string) {
	t.Helper()
	ctx := context.Background()

	_, err := drv.CreateSandbox(ctx, &pb.CreateSandboxRequest{
		Sandbox: &pb.DriverSandbox{
			Id:   id,
			Name: name,
			Spec: &pb.DriverSandboxSpec{
				Template: &pb.DriverSandboxTemplate{
					Image: "ghcr.io/nvidia/openshell-community/sandboxes/base:latest",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateSandbox(%s): %v", name, err)
	}

	t.Cleanup(func() {
		_, delErr := drv.DeleteSandbox(context.Background(), &pb.DeleteSandboxRequest{
			SandboxId:   id,
			SandboxName: name,
		})
		if delErr != nil {
			t.Logf("cleanup: failed to delete sandbox %s: %v (may already be deleted)", name, delErr)
		}
	})
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestIntegration_CreateAndListSandbox(t *testing.T) {
	drv, _ := newIntegrationDriver(t)
	ctx := context.Background()

	name := uniqueName(t)
	id := "integ-id-" + name

	createTestSandbox(t, drv, id, name)

	// List and verify our sandbox appears.
	resp, err := drv.ListSandboxes(ctx, &pb.ListSandboxesRequest{})
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}

	var found bool
	for _, sb := range resp.Sandboxes {
		if sb.GetName() == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("sandbox %s not found in list of %d sandboxes", name, len(resp.Sandboxes))
	}
}

func TestIntegration_GetSandbox(t *testing.T) {
	drv, _ := newIntegrationDriver(t)
	ctx := context.Background()

	name := uniqueName(t)
	id := "integ-id-" + name

	createTestSandbox(t, drv, id, name)

	// Get the sandbox and verify fields.
	resp, err := drv.GetSandbox(ctx, &pb.GetSandboxRequest{
		SandboxName: name,
	})
	if err != nil {
		t.Fatalf("GetSandbox(%s): %v", name, err)
	}

	sb := resp.GetSandbox()
	if sb.GetName() != name {
		t.Errorf("expected name %q, got %q", name, sb.GetName())
	}
	if sb.GetId() != id {
		t.Errorf("expected id %q, got %q", id, sb.GetId())
	}
	if sb.GetNamespace() != testNamespace() {
		t.Errorf("expected namespace %q, got %q", testNamespace(), sb.GetNamespace())
	}
}

func TestIntegration_DeleteSandbox(t *testing.T) {
	drv, _ := newIntegrationDriver(t)
	ctx := context.Background()

	name := uniqueName(t)
	id := "integ-id-" + name

	// Create manually (no cleanup registration since we delete explicitly).
	_, err := drv.CreateSandbox(ctx, &pb.CreateSandboxRequest{
		Sandbox: &pb.DriverSandbox{
			Id:   id,
			Name: name,
			Spec: &pb.DriverSandboxSpec{
				Template: &pb.DriverSandboxTemplate{
					Image: "ghcr.io/nvidia/openshell-community/sandboxes/base:latest",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateSandbox(%s): %v", name, err)
	}

	// Delete.
	delResp, err := drv.DeleteSandbox(ctx, &pb.DeleteSandboxRequest{
		SandboxId:   id,
		SandboxName: name,
	})
	if err != nil {
		t.Fatalf("DeleteSandbox(%s): %v", name, err)
	}
	if !delResp.Deleted {
		t.Error("expected Deleted=true")
	}

	// Verify it is gone.
	_, err = drv.GetSandbox(ctx, &pb.GetSandboxRequest{
		SandboxName: name,
	})
	if err == nil {
		t.Fatal("expected error after deletion, got nil")
	}
}

func TestIntegration_VerifyLabels(t *testing.T) {
	drv, dynClient := newIntegrationDriver(t)
	ctx := context.Background()

	name := uniqueName(t)
	id := "integ-id-" + name

	createTestSandbox(t, drv, id, name)

	// Read the raw CRD via the dynamic client to inspect labels.
	obj, err := dynClient.Resource(sandboxGVR).
		Namespace(testNamespace()).
		Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("dynamic Get(%s): %v", name, err)
	}

	labels := obj.GetLabels()

	checks := map[string]string{
		"kagenti.io/type":         "agent",
		"openshell.ai/managed-by": "openshell",
		"openshell.ai/sandbox-id": id,
	}

	for key, want := range checks {
		got, ok := labels[key]
		if !ok {
			t.Errorf("label %q not found on CRD", key)
			continue
		}
		if got != want {
			t.Errorf("label %q: expected %q, got %q", key, want, got)
		}
	}
}

func TestIntegration_VerifySupervisorInitContainer(t *testing.T) {
	drv, dynClient := newIntegrationDriver(t)
	ctx := context.Background()

	name := uniqueName(t)
	id := "integ-id-" + name

	createTestSandbox(t, drv, id, name)

	// Read the raw CRD spec to find the init container.
	obj, err := dynClient.Resource(sandboxGVR).
		Namespace(testNamespace()).
		Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("dynamic Get(%s): %v", name, err)
	}

	// Navigate: spec.podTemplate.spec.initContainers
	initContainers, found, err := unstructured.NestedSlice(
		obj.Object, "spec", "podTemplate", "spec", "initContainers",
	)
	if err != nil {
		t.Fatalf("read initContainers: %v", err)
	}
	if !found {
		t.Fatal("spec.podTemplate.spec.initContainers not found in CRD")
	}

	var supervisorFound bool
	for _, ic := range initContainers {
		container, ok := ic.(map[string]interface{})
		if !ok {
			continue
		}
		if name, ok := container["name"].(string); ok && name == "supervisor-init" {
			supervisorFound = true
			break
		}
	}

	if !supervisorFound {
		t.Error("init container 'supervisor-init' not found in CRD spec")
	}
}
