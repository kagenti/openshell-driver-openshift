package driver

import (
	"context"
	"log/slog"
	"os"
	"testing"

	pb "github.com/zanetworker/openshell-driver-openshift/gen/computev1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func newProvisionerForTest(t *testing.T) *K8sProvisioner {
	t.Helper()
	p, _ := newTestProvisioner(t)
	return p
}

func TestK8sProvisioner_CreateAndGet(t *testing.T) {
	p := newProvisionerForTest(t)
	ctx := context.Background()

	sb := &pb.DriverSandbox{
		Id:   "sb-100",
		Name: "prov-test",
		Spec: &pb.DriverSandboxSpec{
			Template: &pb.DriverSandboxTemplate{
				Image: "test:latest",
			},
		},
	}

	if err := p.Create(ctx, sb); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := p.Get(ctx, "prov-test")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Id != "sb-100" {
		t.Errorf("expected id sb-100, got %s", got.Id)
	}
	if got.Name != "prov-test" {
		t.Errorf("expected name prov-test, got %s", got.Name)
	}
}

func TestK8sProvisioner_CreateAndDelete(t *testing.T) {
	p := newProvisionerForTest(t)
	ctx := context.Background()

	sb := &pb.DriverSandbox{
		Id:   "sb-del",
		Name: "delete-me",
		Spec: &pb.DriverSandboxSpec{
			Template: &pb.DriverSandboxTemplate{
				Image: "test:latest",
			},
		},
	}
	if err := p.Create(ctx, sb); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := p.Delete(ctx, "delete-me"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Verify gone.
	_, err := p.Get(ctx, "delete-me")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestK8sProvisioner_List(t *testing.T) {
	p := newProvisionerForTest(t)
	ctx := context.Background()

	// Start empty.
	list, err := p.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0, got %d", len(list))
	}

	// Create two sandboxes.
	for _, name := range []string{"a", "b"} {
		if err := p.Create(ctx, &pb.DriverSandbox{
			Id:   "id-" + name,
			Name: name,
			Spec: &pb.DriverSandboxSpec{
				Template: &pb.DriverSandboxTemplate{Image: "img:latest"},
			},
		}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	list, err = p.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
}

func TestK8sProvisioner_ValidateCreate_NoGPU(t *testing.T) {
	p := newProvisionerForTest(t)
	ctx := context.Background()

	sb := &pb.DriverSandbox{
		Spec: &pb.DriverSandboxSpec{Gpu: true},
	}
	err := p.ValidateCreate(ctx, sb)
	if err == nil {
		t.Fatal("expected error for GPU request with no GPU nodes")
	}
}

func TestK8sProvisioner_ValidateCreate_NoGPURequested(t *testing.T) {
	p := newProvisionerForTest(t)
	ctx := context.Background()

	sb := &pb.DriverSandbox{
		Spec: &pb.DriverSandboxSpec{Gpu: false},
	}
	err := p.ValidateCreate(ctx, sb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildSandboxSpec_SupervisorInitContainer(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Namespace = "test-ns"

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{sandboxGVR: "SandboxList"},
	)
	clientset := kubefake.NewSimpleClientset()
	p := NewK8sProvisioner(dynClient, clientset, cfg, logger)

	sb := &pb.DriverSandbox{
		Id: "sb-init",
		Spec: &pb.DriverSandboxSpec{
			Template: &pb.DriverSandboxTemplate{
				Image: "agent:latest",
			},
		},
	}

	spec := p.buildSandboxSpec(sb)

	// Verify podTemplate structure.
	podTemplate, ok := spec["podTemplate"].(map[string]interface{})
	if !ok {
		t.Fatal("missing podTemplate")
	}
	podSpec, ok := podTemplate["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("missing podTemplate.spec")
	}

	// Verify init containers.
	initContainers, ok := podSpec["initContainers"].([]interface{})
	if !ok || len(initContainers) == 0 {
		t.Fatal("missing initContainers")
	}
	initC := initContainers[0].(map[string]interface{})
	if initC["name"] != "supervisor-init" {
		t.Errorf("expected init container name supervisor-init, got %v", initC["name"])
	}
	if initC["image"] != cfg.SupervisorImage {
		t.Errorf("expected image %s, got %v", cfg.SupervisorImage, initC["image"])
	}

	// Verify command uses copy-self (no shell required — works with scratch images).
	cmd := initC["command"].([]interface{})
	expectedInitCmd := []string{cfg.SupervisorBinaryPath, "copy-self", cfg.SupervisorMountPath + "/openshell-sandbox"}
	if len(cmd) != 3 {
		t.Fatalf("expected 3-element command [binary, copy-self, dest], got %v", cmd)
	}
	for i, want := range expectedInitCmd {
		if cmd[i] != want {
			t.Errorf("command[%d] = %v, want %s", i, cmd[i], want)
		}
	}

	// Verify init container has runAsUser: 0.
	initSecCtx := initC["securityContext"].(map[string]interface{})
	if initSecCtx["runAsUser"] != int64(0) {
		t.Errorf("expected init container runAsUser 0, got %v", initSecCtx["runAsUser"])
	}

	// Verify agent container runs supervisor.
	containers, ok := podSpec["containers"].([]interface{})
	if !ok || len(containers) == 0 {
		t.Fatal("missing containers")
	}
	agentC := containers[0].(map[string]interface{})
	if agentC["name"] != "agent" {
		t.Errorf("expected container name agent, got %v", agentC["name"])
	}
	agentCmd := agentC["command"].([]interface{})
	expectedCmd := cfg.SupervisorMountPath + "/openshell-sandbox"
	if len(agentCmd) != 1 || agentCmd[0] != expectedCmd {
		t.Errorf("expected command [%s], got %v", expectedCmd, agentCmd)
	}

	// Verify security context: privileged (required by OpenShift SCC admission)
	// plus explicit capabilities for documentation.
	secCtx := agentC["securityContext"].(map[string]interface{})
	if secCtx["privileged"] != true {
		t.Errorf("expected privileged true, got %v", secCtx["privileged"])
	}
	if secCtx["runAsUser"] != int64(0) {
		t.Errorf("expected runAsUser 0, got %v", secCtx["runAsUser"])
	}
	caps, ok := secCtx["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatal("expected capabilities in securityContext")
	}
	addCaps, ok := caps["add"].([]interface{})
	if !ok {
		t.Fatal("expected capabilities.add list")
	}
	expectedCaps := []string{"SYS_ADMIN", "NET_ADMIN", "SYS_PTRACE", "SYSLOG"}
	if len(addCaps) != len(expectedCaps) {
		t.Fatalf("expected %d capabilities, got %d", len(expectedCaps), len(addCaps))
	}
	for i, want := range expectedCaps {
		if addCaps[i] != want {
			t.Errorf("capability[%d] = %v, want %s", i, addCaps[i], want)
		}
	}

	// Verify volume mounts on agent container.
	agentMounts := agentC["volumeMounts"].([]interface{})
	if len(agentMounts) != 2 {
		t.Fatalf("expected 2 volume mounts on agent, got %d", len(agentMounts))
	}
	mount := agentMounts[0].(map[string]interface{})
	if mount["name"] != "supervisor-bin" {
		t.Errorf("expected mount name supervisor-bin, got %v", mount["name"])
	}
	if mount["readOnly"] != true {
		t.Error("expected readOnly=true on agent volume mount")
	}
	saMount := agentMounts[1].(map[string]interface{})
	if saMount["name"] != "openshell-sa-token" {
		t.Errorf("expected mount name openshell-sa-token, got %v", saMount["name"])
	}
	if saMount["mountPath"] != "/var/run/secrets/openshell" {
		t.Errorf("expected mountPath /var/run/secrets/openshell, got %v", saMount["mountPath"])
	}
	if saMount["readOnly"] != true {
		t.Error("expected readOnly=true on SA token volume mount")
	}

	// Verify volumes.
	volumes, ok := podSpec["volumes"].([]interface{})
	if !ok || len(volumes) < 2 {
		t.Fatalf("expected at least 2 volumes, got %d", len(volumes))
	}
	vol := volumes[0].(map[string]interface{})
	if vol["name"] != "supervisor-bin" {
		t.Errorf("expected volume name supervisor-bin, got %v", vol["name"])
	}
	if _, ok := vol["emptyDir"]; !ok {
		t.Error("expected emptyDir volume")
	}
	saVol := volumes[1].(map[string]interface{})
	if saVol["name"] != "openshell-sa-token" {
		t.Errorf("expected volume name openshell-sa-token, got %v", saVol["name"])
	}
	projected, ok := saVol["projected"].(map[string]interface{})
	if !ok {
		t.Fatal("expected projected volume")
	}
	sources, ok := projected["sources"].([]interface{})
	if !ok || len(sources) == 0 {
		t.Fatal("expected projected sources")
	}
	src := sources[0].(map[string]interface{})
	saToken, ok := src["serviceAccountToken"].(map[string]interface{})
	if !ok {
		t.Fatal("expected serviceAccountToken source")
	}
	if saToken["audience"] != cfg.SATokenAudience {
		t.Errorf("expected audience %s, got %v", cfg.SATokenAudience, saToken["audience"])
	}
	if saToken["expirationSeconds"] != cfg.SATokenTTLSecs {
		t.Errorf("expected expirationSeconds %d, got %v", cfg.SATokenTTLSecs, saToken["expirationSeconds"])
	}
	if saToken["path"] != "token" {
		t.Errorf("expected path token, got %v", saToken["path"])
	}
}

func TestBuildSandboxSpec_Labels(t *testing.T) {
	p := newProvisionerForTest(t)

	sb := &pb.DriverSandbox{
		Id: "sb-labels",
		Spec: &pb.DriverSandboxSpec{
			Template: &pb.DriverSandboxTemplate{
				Image: "img:latest",
				Labels: map[string]string{
					"custom": "label",
				},
			},
		},
	}

	spec := p.buildSandboxSpec(sb)
	podTemplate := spec["podTemplate"].(map[string]interface{})
	meta := podTemplate["metadata"].(map[string]interface{})
	labels := meta["labels"].(map[string]interface{})

	if labels["custom"] != "label" {
		t.Errorf("expected custom=label, got %v", labels["custom"])
	}
	if labels[labelSandboxID] != "sb-labels" {
		t.Errorf("expected sandbox ID label, got %v", labels[labelSandboxID])
	}
	if labels[labelManagedBy] != "openshell" {
		t.Errorf("expected managed-by label, got %v", labels[labelManagedBy])
	}
	// No tenant configured in testConfig() — tenant labels must be absent.
	if _, ok := labels[labelTenant]; ok {
		t.Errorf("expected no %s label when tenant is empty, got %v", labelTenant, labels[labelTenant])
	}
}

func TestBuildSandboxSpec_TenantLabels(t *testing.T) {
	cfg := testConfig()
	cfg.Tenant = "team1"

	logger := testLogger()
	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{sandboxGVR: "SandboxList"},
	)
	clientset := kubefake.NewSimpleClientset()
	p := NewK8sProvisioner(dynClient, clientset, cfg, logger)

	sb := &pb.DriverSandbox{
		Id: "sb-tenant",
		Spec: &pb.DriverSandboxSpec{
			Template: &pb.DriverSandboxTemplate{
				Image: "img:latest",
			},
		},
	}

	spec := p.buildSandboxSpec(sb)
	podTemplate := spec["podTemplate"].(map[string]interface{})
	meta := podTemplate["metadata"].(map[string]interface{})
	podLabels := meta["labels"].(map[string]interface{})

	if podLabels[labelTenant] != "team1" {
		t.Errorf("expected %s=team1, got %v", labelTenant, podLabels[labelTenant])
	}
	if podLabels[labelKagentiTeam] != "team1" {
		t.Errorf("expected %s=team1, got %v", labelKagentiTeam, podLabels[labelKagentiTeam])
	}
}

func TestNewWithDeps(t *testing.T) {
	p := newProvisionerForTest(t)
	logger := testLogger()

	d := NewWithDeps(p, &NoopEnricher{}, &NoopMetrics{}, logger)

	resp, err := d.GetCapabilities(context.Background(), &pb.GetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.DriverName != "openshift" {
		t.Errorf("expected openshift, got %s", resp.DriverName)
	}
}

func TestK8sProvisioner_Watch_ChannelCloses(t *testing.T) {
	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{sandboxGVR: "SandboxList"},
	)
	clientset := kubefake.NewSimpleClientset()
	logger := testLogger()
	cfg := testConfig()

	p := NewK8sProvisioner(dynClient, clientset, cfg, logger)

	ctx, cancel := context.WithCancel(context.Background())

	ch, err := p.Watch(ctx)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	// Cancel context to stop the watcher; the channel should close.
	cancel()

	// Drain and verify the channel closes without hanging.
	for range ch {
	}
}

func TestBuildSandboxSpec_ImagePullPolicy(t *testing.T) {
	cfg := testConfig()
	cfg.ImagePullPolicy = "IfNotPresent"

	logger := testLogger()
	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{sandboxGVR: "SandboxList"},
	)
	clientset := kubefake.NewSimpleClientset()
	p := NewK8sProvisioner(dynClient, clientset, cfg, logger)

	sb := &pb.DriverSandbox{
		Id: "sb-pull",
		Spec: &pb.DriverSandboxSpec{
			Template: &pb.DriverSandboxTemplate{
				Image: "agent:latest",
			},
		},
	}

	spec := p.buildSandboxSpec(sb)
	podTemplate := spec["podTemplate"].(map[string]interface{})
	podSpec := podTemplate["spec"].(map[string]interface{})

	// Verify init container has imagePullPolicy set.
	initContainers := podSpec["initContainers"].([]interface{})
	initC := initContainers[0].(map[string]interface{})
	if initC["imagePullPolicy"] != "IfNotPresent" {
		t.Errorf("expected init container imagePullPolicy=IfNotPresent, got %v", initC["imagePullPolicy"])
	}

	// Verify agent container has imagePullPolicy set.
	containers := podSpec["containers"].([]interface{})
	agentC := containers[0].(map[string]interface{})
	if agentC["imagePullPolicy"] != "IfNotPresent" {
		t.Errorf("expected agent container imagePullPolicy=IfNotPresent, got %v", agentC["imagePullPolicy"])
	}
}

func TestBuildSandboxSpec_ImagePullPolicy_Empty(t *testing.T) {
	cfg := testConfig()
	// ImagePullPolicy left empty — should not appear in spec.

	logger := testLogger()
	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{sandboxGVR: "SandboxList"},
	)
	clientset := kubefake.NewSimpleClientset()
	p := NewK8sProvisioner(dynClient, clientset, cfg, logger)

	sb := &pb.DriverSandbox{
		Id: "sb-nopull",
		Spec: &pb.DriverSandboxSpec{
			Template: &pb.DriverSandboxTemplate{
				Image: "agent:latest",
			},
		},
	}

	spec := p.buildSandboxSpec(sb)
	podTemplate := spec["podTemplate"].(map[string]interface{})
	podSpec := podTemplate["spec"].(map[string]interface{})

	initContainers := podSpec["initContainers"].([]interface{})
	initC := initContainers[0].(map[string]interface{})
	if _, ok := initC["imagePullPolicy"]; ok {
		t.Error("expected no imagePullPolicy on init container when config is empty")
	}

	containers := podSpec["containers"].([]interface{})
	agentC := containers[0].(map[string]interface{})
	if _, ok := agentC["imagePullPolicy"]; ok {
		t.Error("expected no imagePullPolicy on agent container when config is empty")
	}
}

func TestBuildSandboxSpec_SATokenEnv(t *testing.T) {
	p := newProvisionerForTest(t)

	sb := &pb.DriverSandbox{
		Id: "sb-token",
		Spec: &pb.DriverSandboxSpec{
			Template: &pb.DriverSandboxTemplate{
				Image: "agent:latest",
			},
		},
	}

	spec := p.buildSandboxSpec(sb)
	podTemplate := spec["podTemplate"].(map[string]interface{})
	podSpec := podTemplate["spec"].(map[string]interface{})
	containers := podSpec["containers"].([]interface{})
	agentC := containers[0].(map[string]interface{})
	envList := agentC["env"].([]interface{})

	var found bool
	for _, e := range envList {
		env := e.(map[string]interface{})
		if env["name"] == "OPENSHELL_K8S_SA_TOKEN_FILE" {
			found = true
			if env["value"] != "/var/run/secrets/openshell/token" {
				t.Errorf("expected /var/run/secrets/openshell/token, got %v", env["value"])
			}
			break
		}
	}
	if !found {
		t.Error("OPENSHELL_K8S_SA_TOKEN_FILE env var not found in agent container")
	}
}
