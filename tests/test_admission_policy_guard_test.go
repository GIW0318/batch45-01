package task

import (
	"testing"

	. "gsb/batch45-safety-c-14"
)

func TestMatchKindIncludesDeploymentTemplate(t *testing.T) {
	g := &PolicyGuard{}
	got, err := g.MatchKind("Deployment", "apps", []string{"Pod", "Deployment"})
	if err != nil {
		t.Fatalf("MatchKind: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("Deployment templates must be matched, got %v", got)
	}
}

func TestMatchKindDeniesUnmanagedKind(t *testing.T) {
	g := &PolicyGuard{}
	got, err := g.MatchKind("ConfigMap", "", []string{"Pod", "Deployment"})
	if err != nil {
		t.Fatalf("MatchKind: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("kind outside the policy must not match, got %v", got)
	}
}

func TestCheckAPIGroupDeniesWildcard(t *testing.T) {
	g := &PolicyGuard{}
	got, err := g.CheckAPIGroup("apps", []string{"*"})
	if err != nil {
		t.Fatalf("CheckAPIGroup: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("wildcard API group rule, want deny, got %v", got)
	}
}

func TestCheckAPIGroupAllowsExplicitGroup(t *testing.T) {
	g := &PolicyGuard{}
	got, err := g.CheckAPIGroup("apps", []string{"apps", "batch"})
	if err != nil {
		t.Fatalf("CheckAPIGroup: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("explicit API group, want allow, got %v", got)
	}
}

func TestCheckFailurePolicyDeniesOnTimeout(t *testing.T) {
	g := &PolicyGuard{}
	got, err := g.CheckFailurePolicy(FailureIgnore, true)
	if err != nil {
		t.Fatalf("CheckFailurePolicy: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("timed out admission must deny even under an ignore policy, got %v", got)
	}
}

func TestCheckSideEffectsDeniesDeclaredNoneMismatch(t *testing.T) {
	g := &PolicyGuard{}
	got, err := g.CheckSideEffects(true, true)
	if err != nil {
		t.Fatalf("CheckSideEffects: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("rule declares no side effects but has them, want deny, got %v", got)
	}
}

func TestCheckImageDigestDeniesTagOnlyImage(t *testing.T) {
	g := &PolicyGuard{}
	got, err := g.CheckImageDigest("registry.example.com/app:1.25")
	if err != nil {
		t.Fatalf("CheckImageDigest: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("mutable tag without digest, want deny, got %v", got)
	}
}

func TestCheckImageDigestAllowsPinnedDigest(t *testing.T) {
	g := &PolicyGuard{}
	got, err := g.CheckImageDigest("registry.example.com/app@sha256:9f2c1b")
	if err != nil {
		t.Fatalf("CheckImageDigest: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("image pinned by digest, want allow, got %v", got)
	}
}

func TestCheckPrivilegedDeniesInitContainer(t *testing.T) {
	g := &PolicyGuard{}
	pod := PodSpec{
		Containers:     []ContainerSpec{{Name: "app", Image: "app@sha256:aa"}},
		InitContainers: []ContainerSpec{{Name: "setup", Image: "busybox@sha256:bb", Privileged: true}},
	}
	got, err := g.CheckPrivileged(pod)
	if err != nil {
		t.Fatalf("CheckPrivileged: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("privileged init container must be denied, got %v", got)
	}
}

func TestCheckPrivilegedAllowsUnprivilegedPod(t *testing.T) {
	g := &PolicyGuard{}
	pod := PodSpec{
		Containers:     []ContainerSpec{{Name: "app", Image: "app@sha256:aa"}},
		InitContainers: []ContainerSpec{{Name: "setup", Image: "busybox@sha256:bb"}},
	}
	got, err := g.CheckPrivileged(pod)
	if err != nil {
		t.Fatalf("CheckPrivileged: %v", err)
	}
	if got != VerdictAllow {
		t.Fatalf("no privileged container, want allow, got %v", got)
	}
}

func TestCheckHostPathDeniesUnapprovedMount(t *testing.T) {
	g := &PolicyGuard{}
	pod := PodSpec{HostPathMounts: []string{"/var/run/docker.sock"}}
	got, err := g.CheckHostPath(pod, []string{"/data"})
	if err != nil {
		t.Fatalf("CheckHostPath: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("host path outside approved prefixes, want deny, got %v", got)
	}
}

func TestCheckCapabilitiesDeniesNetRawWithoutDrop(t *testing.T) {
	g := &PolicyGuard{}
	c := ContainerSpec{Name: "app", Image: "app@sha256:aa", CapAdd: []string{"NET_RAW"}}
	got, err := g.CheckCapabilities(c)
	if err != nil {
		t.Fatalf("CheckCapabilities: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("NET_RAW added without being dropped, want deny, got %v", got)
	}
}

func TestCheckNamespaceDeniesOutsideSelector(t *testing.T) {
	g := &PolicyGuard{}
	got, err := g.CheckNamespace("kube-system", []string{"default", "prod"})
	if err != nil {
		t.Fatalf("CheckNamespace: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("namespace outside the selector must not be exempt, got %v", got)
	}
}

func TestEvaluateDeniesPrivilegedPodOnUpdate(t *testing.T) {
	g := &PolicyGuard{}
	req := AdmissionRequest{
		Kind:      "Deployment",
		APIGroup:  "apps",
		Namespace: "prod",
		Operation: OperationUpdate,
		Pod: PodSpec{
			Containers: []ContainerSpec{{Name: "app", Image: "app@sha256:aa", Privileged: true}},
		},
	}
	dec, err := g.Evaluate(req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if dec.Allowed {
		t.Fatalf("privileged pod submitted through an update must be rejected")
	}
}

func TestCheckCertRotationDeniesStaleConnection(t *testing.T) {
	g := &PolicyGuard{}
	got, err := g.CheckCertRotation(2, 3)
	if err != nil {
		t.Fatalf("CheckCertRotation: %v", err)
	}
	if got != VerdictDeny {
		t.Fatalf("connection still uses the old serving certificate, want deny, got %v", got)
	}
}
