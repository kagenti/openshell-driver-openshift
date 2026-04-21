package driver

import (
	"testing"

	pb "github.com/zanetworker/openshell-driver-openshift/gen/computev1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestObjToDriverSandbox_BasicFields(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "agents.x-k8s.io/v1alpha1",
			"kind":       "Sandbox",
			"metadata": map[string]interface{}{
				"name":      "test-sandbox",
				"namespace": "default",
				"labels": map[string]interface{}{
					"openshell.ai/sandbox-id": "sb-123",
				},
			},
		},
	}

	sb := objToDriverSandbox(obj)

	if sb.Id != "sb-123" {
		t.Errorf("expected id sb-123, got %s", sb.Id)
	}
	if sb.Name != "test-sandbox" {
		t.Errorf("expected name test-sandbox, got %s", sb.Name)
	}
	if sb.Namespace != "default" {
		t.Errorf("expected namespace default, got %s", sb.Namespace)
	}
}

func TestObjToDriverSandbox_WithStatus(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "agents.x-k8s.io/v1alpha1",
			"kind":       "Sandbox",
			"metadata": map[string]interface{}{
				"name":      "sb-with-status",
				"namespace": "ns1",
				"labels": map[string]interface{}{
					"openshell.ai/sandbox-id": "sb-456",
				},
			},
			"status": map[string]interface{}{
				"sandboxName": "sb-with-status",
				"agentPod":    "sb-with-status-agent-pod",
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Ready",
						"status": "True",
						"reason": "PodRunning",
					},
				},
			},
		},
	}

	sb := objToDriverSandbox(obj)

	if sb.Status == nil {
		t.Fatal("expected non-nil status")
	}
	if sb.Status.SandboxName != "sb-with-status" {
		t.Errorf("expected sandbox name sb-with-status, got %s", sb.Status.SandboxName)
	}
	if sb.Status.InstanceId != "sb-with-status-agent-pod" {
		t.Errorf("expected instance id sb-with-status-agent-pod, got %s", sb.Status.InstanceId)
	}
	if len(sb.Status.Conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(sb.Status.Conditions))
	}
	cond := sb.Status.Conditions[0]
	if cond.Type != "Ready" || cond.Status != "True" || cond.Reason != "PodRunning" {
		t.Errorf("unexpected condition: %+v", cond)
	}
}

func TestObjToDriverSandbox_DeletionTimestamp(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "agents.x-k8s.io/v1alpha1",
			"kind":       "Sandbox",
			"metadata": map[string]interface{}{
				"name":              "deleting-sb",
				"namespace":         "default",
				"deletionTimestamp": "2026-04-20T12:00:00Z",
				"labels": map[string]interface{}{
					"openshell.ai/sandbox-id": "sb-del",
				},
			},
			"status": map[string]interface{}{
				"sandboxName": "deleting-sb",
			},
		},
	}

	sb := objToDriverSandbox(obj)

	if sb.Status == nil || !sb.Status.Deleting {
		t.Error("expected deleting=true when deletionTimestamp is set")
	}
}

func TestBuildEnvList_MergesAndOverrides(t *testing.T) {
	specEnv := map[string]string{"KEY1": "from-spec", "KEY2": "spec-only"}
	tmplEnv := map[string]string{"KEY1": "from-tmpl", "KEY3": "tmpl-only"}

	result := buildEnvList(specEnv, tmplEnv)

	envMap := make(map[string]string)
	for _, item := range result {
		m := item.(map[string]interface{})
		envMap[m["name"].(string)] = m["value"].(string)
	}

	// spec overrides tmpl for KEY1
	if envMap["KEY1"] != "from-spec" {
		t.Errorf("expected KEY1=from-spec, got %s", envMap["KEY1"])
	}
	if envMap["KEY2"] != "spec-only" {
		t.Errorf("expected KEY2=spec-only, got %s", envMap["KEY2"])
	}
	if envMap["KEY3"] != "tmpl-only" {
		t.Errorf("expected KEY3=tmpl-only, got %s", envMap["KEY3"])
	}
}

func TestBuildResources_WithGPU(t *testing.T) {
	res := &pb.DriverResourceRequirements{
		CpuRequest:    "500m",
		CpuLimit:      "2",
		MemoryRequest: "1Gi",
		MemoryLimit:   "4Gi",
	}

	result := buildResources(res, true)

	limits := result["limits"].(map[string]interface{})
	if limits["nvidia.com/gpu"] != "1" {
		t.Error("expected nvidia.com/gpu=1 in limits when gpu=true")
	}
	if limits["cpu"] != "2" {
		t.Errorf("expected cpu limit 2, got %v", limits["cpu"])
	}

	requests := result["requests"].(map[string]interface{})
	if requests["memory"] != "1Gi" {
		t.Errorf("expected memory request 1Gi, got %v", requests["memory"])
	}
}

func TestBuildResources_NoGPU(t *testing.T) {
	res := &pb.DriverResourceRequirements{
		CpuRequest: "100m",
	}

	result := buildResources(res, false)

	if limits, ok := result["limits"]; ok {
		lm := limits.(map[string]interface{})
		if _, hasGPU := lm["nvidia.com/gpu"]; hasGPU {
			t.Error("expected no GPU in limits when gpu=false")
		}
	}
}

func TestMergeMaps_BOverridesA(t *testing.T) {
	a := map[string]string{"x": "1", "y": "2"}
	b := map[string]string{"y": "3", "z": "4"}

	result := mergeMaps(a, b)

	if result["x"] != "1" {
		t.Errorf("expected x=1, got %v", result["x"])
	}
	if result["y"] != "3" {
		t.Errorf("expected y=3 (b overrides a), got %v", result["y"])
	}
	if result["z"] != "4" {
		t.Errorf("expected z=4, got %v", result["z"])
	}
}

func TestGetString_SafeExtraction(t *testing.T) {
	m := map[string]interface{}{
		"str":     "hello",
		"num":     float64(42),
		"missing": nil,
	}

	if v := getString(m, "str"); v != "hello" {
		t.Errorf("expected hello, got %s", v)
	}
	if v := getString(m, "num"); v != "42" {
		t.Errorf("expected 42, got %s", v)
	}
	if v := getString(m, "nonexistent"); v != "" {
		t.Errorf("expected empty string for missing key, got %s", v)
	}
}
