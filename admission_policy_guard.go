package task

import "errors"

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
	return VerdictUnknown, errors.New("not implemented")
}

// CheckAPIGroup verifies the rule's API group. A wildcard group is not a valid
// rule: it would apply the policy to unrelated APIs.
func (g *PolicyGuard) CheckAPIGroup(group string, allowed []string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckFailurePolicy decides what happens when the webhook cannot complete.
// A request that timed out must be denied.
func (g *PolicyGuard) CheckFailurePolicy(p FailurePolicy, timedOut bool) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckSideEffects rejects a rule that declares no side effects while the
// webhook actually performs them.
func (g *PolicyGuard) CheckSideEffects(declaredNone, actualSideEffects bool) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckImageDigest requires the image to be pinned by digest, so a mutable tag
// cannot be repointed at another image later.
func (g *PolicyGuard) CheckImageDigest(image string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckPrivileged rejects a pod that has a privileged container either as a
// regular container or as an init container.
func (g *PolicyGuard) CheckPrivileged(pod PodSpec) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckHostPath rejects host path mounts outside the approved prefixes.
func (g *PolicyGuard) CheckHostPath(pod PodSpec, allowedPrefixes []string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckCapabilities requires dangerous capabilities such as NET_RAW either to
// be absent or to be explicitly dropped.
func (g *PolicyGuard) CheckCapabilities(c ContainerSpec) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckNamespace requires the object namespace to be selected by the policy;
// namespaces that are not selected are not exempt.
func (g *PolicyGuard) CheckNamespace(ns string, selected []string) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// CheckCertRotation verifies that the webhook connection uses the currently
// active serving certificate.
func (g *PolicyGuard) CheckCertRotation(connSerial, activeSerial uint64) (Verdict, error) {
	return VerdictUnknown, errors.New("not implemented")
}

// Evaluate runs the full policy against a request. An update to a workload
// template must be evaluated exactly like a create.
func (g *PolicyGuard) Evaluate(req AdmissionRequest) (Decision, error) {
	return Decision{}, errors.New("not implemented")
}
