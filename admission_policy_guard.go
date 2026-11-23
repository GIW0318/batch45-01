package task

import (
	"strings"
)

// Verdict is the outcome of a policy check. The zero value is intentionally
// "unknown": an unimplemented check must never be read as a pass.
type Verdict int

const (
	VerdictUnknown Verdict = iota
	VerdictAllow
	VerdictDeny
)

// Operation is the admission operation being performed.
type Operation int

const (
	OperationUnknown Operation = iota
	OperationCreate
	OperationUpdate
)

// FailurePolicy is the webhook failure policy.
type FailurePolicy int

const (
	FailureUnset FailurePolicy = iota
	FailureIgnore
	FailureFail
)

// ContainerSpec is the part of a container definition the policy inspects.
type ContainerSpec struct {
	Name       string
	Image      string
	Privileged bool
	CapAdd     []string
	CapDrop    []string
}

// PodSpec is the pod template the policy inspects. It is also used for the
// templates embedded in Deployment, StatefulSet and DaemonSet objects.
type PodSpec struct {
	Containers     []ContainerSpec
	InitContainers []ContainerSpec
	HostPathMounts []string
}

// AdmissionRequest is one admission review.
type AdmissionRequest struct {
	Kind             string
	APIGroup         string
	Namespace        string
	Operation        Operation
	Pod              PodSpec
	TimedOut         bool
	ConnCertSerial   uint64
	ActiveCertSerial uint64
}

// Decision is the admission decision handed back to the API server.
type Decision struct {
	Allowed bool
	Reason  string
}

// PolicyGuard is the image policy admission controller. It holds no state of
// its own.
type PolicyGuard struct{}

// MatchKind checks whether the request kind is managed by the policy. The
// policy must also match workload controllers such as Deployment, not only
// bare Pods.
func (g *PolicyGuard) MatchKind(kind, apiGroup string, managed []string) (Verdict, error) {
	for _, m := range managed {
		if m == kind {
			return VerdictAllow, nil
		}
	}
	return VerdictDeny, nil
}

// CheckAPIGroup verifies the rule's API group. A wildcard group is not a valid
// rule: it would apply the policy to unrelated APIs.
func (g *PolicyGuard) CheckAPIGroup(group string, allowed []string) (Verdict, error) {
	for _, a := range allowed {
		if a == "*" {
			return VerdictDeny, nil
		}
	}
	for _, a := range allowed {
		if a == group {
			return VerdictAllow, nil
		}
	}
	return VerdictDeny, nil
}

// CheckFailurePolicy decides what happens when the webhook cannot complete.
// A request that timed out must be denied.
func (g *PolicyGuard) CheckFailurePolicy(p FailurePolicy, timedOut bool) (Verdict, error) {
	if timedOut {
		return VerdictDeny, nil
	}
	switch p {
	case FailureFail:
		return VerdictDeny, nil
	case FailureIgnore:
		return VerdictAllow, nil
	default:
		return VerdictDeny, nil
	}
}

// CheckSideEffects rejects a rule that declares no side effects while the
// webhook actually performs them.
func (g *PolicyGuard) CheckSideEffects(declaredNone, actualSideEffects bool) (Verdict, error) {
	if declaredNone && actualSideEffects {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// CheckImageDigest requires the image to be pinned by digest, so a mutable tag
// cannot be repointed at another image later.
func (g *PolicyGuard) CheckImageDigest(image string) (Verdict, error) {
	if !strings.Contains(image, "@sha256:") {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// CheckPrivileged rejects a pod that has a privileged container either as a
// regular container or as an init container.
func (g *PolicyGuard) CheckPrivileged(pod PodSpec) (Verdict, error) {
	for _, c := range pod.Containers {
		if c.Privileged {
			return VerdictDeny, nil
		}
	}
	for _, c := range pod.InitContainers {
		if c.Privileged {
			return VerdictDeny, nil
		}
	}
	return VerdictAllow, nil
}

// CheckHostPath rejects host path mounts outside the approved prefixes.
func (g *PolicyGuard) CheckHostPath(pod PodSpec, allowedPrefixes []string) (Verdict, error) {
	for _, mount := range pod.HostPathMounts {
		ok := false
		for _, prefix := range allowedPrefixes {
			if mount == prefix || strings.HasPrefix(mount, strings.TrimSuffix(prefix, "/")+"/") {
				ok = true
				break
			}
		}
		if !ok {
			return VerdictDeny, nil
		}
	}
	return VerdictAllow, nil
}

// CheckCapabilities requires dangerous capabilities such as NET_RAW either to
// be absent or to be explicitly dropped.
func (g *PolicyGuard) CheckCapabilities(c ContainerSpec) (Verdict, error) {
	dropped := map[string]bool{}
	for _, d := range c.CapDrop {
		dropped[strings.ToUpper(d)] = true
	}
	if dropped["ALL"] {
		return VerdictAllow, nil
	}
	for _, a := range c.CapAdd {
		if !dropped[strings.ToUpper(a)] {
			return VerdictDeny, nil
		}
	}
	return VerdictAllow, nil
}

// CheckNamespace requires the object namespace to be selected by the policy;
// namespaces that are not selected are not exempt.
func (g *PolicyGuard) CheckNamespace(ns string, selected []string) (Verdict, error) {
	for _, s := range selected {
		if s == ns {
			return VerdictAllow, nil
		}
	}
	return VerdictDeny, nil
}

// CheckCertRotation verifies that the webhook connection uses the currently
// active serving certificate.
func (g *PolicyGuard) CheckCertRotation(connSerial, activeSerial uint64) (Verdict, error) {
	if connSerial != activeSerial {
		return VerdictDeny, nil
	}
	return VerdictAllow, nil
}

// Evaluate runs the full policy against a request. An update to a workload
// template must be evaluated exactly like a create.
func (g *PolicyGuard) Evaluate(req AdmissionRequest) (Decision, error) {
	if req.TimedOut {
		return Decision{Allowed: false, Reason: "admission request timed out"}, nil
	}
	if req.ConnCertSerial != 0 || req.ActiveCertSerial != 0 {
		if req.ConnCertSerial != req.ActiveCertSerial {
			return Decision{Allowed: false, Reason: "connection uses a stale serving certificate"}, nil
		}
	}
	if v, err := g.CheckPrivileged(req.Pod); err != nil {
		return Decision{}, err
	} else if v != VerdictAllow {
		return Decision{Allowed: false, Reason: "privileged container is not allowed"}, nil
	}
	for _, c := range req.Pod.Containers {
		if v, err := g.CheckImageDigest(c.Image); err != nil {
			return Decision{}, err
		} else if v != VerdictAllow {
			return Decision{Allowed: false, Reason: "image must be pinned by digest"}, nil
		}
		if v, err := g.CheckCapabilities(c); err != nil {
			return Decision{}, err
		} else if v != VerdictAllow {
			return Decision{Allowed: false, Reason: "added capabilities must be dropped"}, nil
		}
	}
	for _, c := range req.Pod.InitContainers {
		if v, err := g.CheckImageDigest(c.Image); err != nil {
			return Decision{}, err
		} else if v != VerdictAllow {
			return Decision{Allowed: false, Reason: "image must be pinned by digest"}, nil
		}
		if v, err := g.CheckCapabilities(c); err != nil {
			return Decision{}, err
		} else if v != VerdictAllow {
			return Decision{Allowed: false, Reason: "added capabilities must be dropped"}, nil
		}
	}
	return Decision{Allowed: true, Reason: "policy checks passed"}, nil
}
