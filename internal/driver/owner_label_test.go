package driver

import (
	"context"
	"testing"

	pb "github.com/zanetworker/openshell-driver-openshift/gen/computev1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

const ownerLabelKey = "openshell.ai/owner"

// extractStringMap handles both map[string]string and map[string]interface{} for
// unstructured map fields (buildSandboxSpec returns nested map[string]interface{}).
func extractStringMap(t *testing.T, parent map[string]interface{}, key string) map[string]string {
	t.Helper()
	raw, ok := parent[key]
	if !ok {
		t.Fatalf("key %q not found in map", key)
	}
	switch v := raw.(type) {
	case map[string]string:
		return v
	case map[string]interface{}:
		out := make(map[string]string, len(v))
		for k, val := range v {
			s, _ := val.(string)
			out[k] = s
		}
		return out
	default:
		t.Fatalf("key %q has unexpected type %T", key, raw)
		return nil
	}
}

func TestOwnerLabelPropagation_SandboxCR(t *testing.T) {
	p, dynClient := newTestProvisioner(t)
	ctx := context.Background()

	ownerSubject := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

	sb := &pb.DriverSandbox{
		Id:   "sb-owner-cr",
		Name: "alice-sandbox",
		Spec: &pb.DriverSandboxSpec{
			Template: &pb.DriverSandboxTemplate{
				Image: "agent:latest",
				Labels: map[string]string{
					ownerLabelKey: ownerSubject,
					"custom-key":  "custom-value",
				},
			},
		},
	}

	if err := p.Create(ctx, sb); err != nil {
		t.Fatalf("create: %v", err)
	}

	obj, err := dynClient.Resource(sandboxGVR).
		Namespace(p.cfg.Namespace).
		Get(ctx, "alice-sandbox", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get created sandbox: %v", err)
	}

	labels := obj.GetLabels()

	// Owner label from gateway must be present on the Sandbox CR.
	if got := labels[ownerLabelKey]; got != ownerSubject {
		t.Errorf("expected %s=%s on Sandbox CR, got %q", ownerLabelKey, ownerSubject, got)
	}

	// Custom labels must also propagate.
	if got := labels["custom-key"]; got != "custom-value" {
		t.Errorf("expected custom-key=custom-value, got %q", got)
	}

	// Driver's own labels must still be present.
	if got := labels[labelSandboxID]; got != "sb-owner-cr" {
		t.Errorf("expected %s=sb-owner-cr, got %q", labelSandboxID, got)
	}
}

func TestOwnerLabelPropagation_PodTemplate(t *testing.T) {
	p, _ := newTestProvisioner(t)

	ownerSubject := "f9e8d7c6-b5a4-3210-fedc-ba0987654321"

	sb := &pb.DriverSandbox{
		Id:   "sb-owner-pod",
		Name: "bob-sandbox",
		Spec: &pb.DriverSandboxSpec{
			Template: &pb.DriverSandboxTemplate{
				Image: "agent:latest",
				Labels: map[string]string{
					ownerLabelKey: ownerSubject,
				},
			},
		},
	}

	spec := p.buildSandboxSpec(sb)

	podTemplate, ok := spec["podTemplate"].(map[string]interface{})
	if !ok {
		t.Fatal("missing podTemplate in spec")
	}
	metadata, ok := podTemplate["metadata"].(map[string]interface{})
	if !ok {
		t.Fatal("missing podTemplate.metadata")
	}

	podLabels := extractStringMap(t, metadata, "labels")

	// Owner label must appear on pod template labels.
	if got := podLabels[ownerLabelKey]; got != ownerSubject {
		t.Errorf("expected %s=%s on pod template, got %q", ownerLabelKey, ownerSubject, got)
	}

	// Driver labels must coexist.
	if got := podLabels[labelSandboxID]; got != "sb-owner-pod" {
		t.Errorf("expected %s=sb-owner-pod on pod template, got %q", labelSandboxID, got)
	}
}

func TestOwnerLabelNotOverwrittenByDriver(t *testing.T) {
	p, dynClient := newTestProvisioner(t)
	ctx := context.Background()

	ownerSubject := "unique-user-uuid-12345"

	sb := &pb.DriverSandbox{
		Id:   "sb-owner-safe",
		Name: "safe-sandbox",
		Spec: &pb.DriverSandboxSpec{
			Template: &pb.DriverSandboxTemplate{
				Image: "agent:latest",
				Labels: map[string]string{
					ownerLabelKey: ownerSubject,
				},
			},
		},
	}

	if err := p.Create(ctx, sb); err != nil {
		t.Fatalf("create: %v", err)
	}

	obj, err := dynClient.Resource(sandboxGVR).
		Namespace(p.cfg.Namespace).
		Get(ctx, "safe-sandbox", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// The driver's mergeMaps uses template labels as base and driver labels as
	// overrides. Since the driver does NOT set openshell.ai/owner, the gateway's
	// value must survive untouched.
	if got := obj.GetLabels()[ownerLabelKey]; got != ownerSubject {
		t.Errorf("owner label was overwritten: expected %s, got %q", ownerSubject, got)
	}
}

func TestOwnerLabelWithTenantConfig(t *testing.T) {
	cfg := testConfig()
	cfg.Tenant = "team1"

	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{sandboxGVR: "SandboxList"},
	)
	clientset := kubefake.NewSimpleClientset()
	p := NewK8sProvisioner(dynClient, clientset, cfg, testLogger())
	ctx := context.Background()

	ownerSubject := "tenant-user-abc"

	sb := &pb.DriverSandbox{
		Id:   "sb-tenant-owner",
		Name: "tenant-sandbox",
		Spec: &pb.DriverSandboxSpec{
			Template: &pb.DriverSandboxTemplate{
				Image: "agent:latest",
				Labels: map[string]string{
					ownerLabelKey: ownerSubject,
				},
			},
		},
	}

	if err := p.Create(ctx, sb); err != nil {
		t.Fatalf("create: %v", err)
	}

	spec := p.buildSandboxSpec(sb)
	podTemplate := spec["podTemplate"].(map[string]interface{})
	metadata := podTemplate["metadata"].(map[string]interface{})
	podLabels := extractStringMap(t, metadata, "labels")

	// Owner and tenant labels must coexist.
	if got := podLabels[ownerLabelKey]; got != ownerSubject {
		t.Errorf("expected owner=%s, got %q", ownerSubject, got)
	}
	if got := podLabels[labelTenant]; got != "team1" {
		t.Errorf("expected tenant=team1, got %q", got)
	}
	if got := podLabels[labelKagentiTeam]; got != "team1" {
		t.Errorf("expected kagenti.io/team=team1, got %q", got)
	}
}
