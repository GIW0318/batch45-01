package task

import "errors"

var errNotImplemented = errors.New("not implemented")

// Vec3 is a point or direction in world space.
type Vec3 struct {
	X float64
	Y float64
	Z float64
}

// IkSolver solves two bone leg IK for one character.
type IkSolver struct{}

// NewIkSolver creates an empty solver.
func NewIkSolver() *IkSolver {
	return &IkSolver{}
}

// ProjectToPlane projects p onto the plane through the origin with the given normal.
func (s *IkSolver) ProjectToPlane(p, normal Vec3) Vec3 {
	return Vec3{}
}

// KneePole returns a unit pole vector perpendicular to the root->target axis.
func (s *IkSolver) KneePole(root, target, forward Vec3) Vec3 {
	return Vec3{}
}

// ClampJointAngle clamps an angle in degrees to [min, max].
func (s *IkSolver) ClampJointAngle(angle, min, max float64) float64 {
	return 0
}

// FootPitch returns the foot pitch in degrees for a ground normal.
func (s *IkSolver) FootPitch(normal Vec3) float64 {
	return 0
}

// ResolveRootMotion blends the animated root with the IK corrected root.
func (s *IkSolver) ResolveRootMotion(anim, ik Vec3, weight float64) Vec3 {
	return Vec3{}
}

// ClampToReach shortens target to maxLen from root and reports whether it was in range.
func (s *IkSolver) ClampToReach(root, target Vec3, maxLen float64) (Vec3, bool) {
	return Vec3{}, false
}

// SolveOrder returns the chain sorted from root to leaf using the parent map.
func (s *IkSolver) SolveOrder(chain []string, hierarchy map[string]string) []string {
	return nil
}

// AddOffset accumulates a per bone X offset requested by an IK task.
func (s *IkSolver) AddOffset(bone string, dx float64) {
}

// Offset returns the accumulated X offset of a bone.
func (s *IkSolver) Offset(bone string) float64 {
	return 0
}

// PlatformTarget converts moving platform motion into an IK target offset.
func (s *IkSolver) PlatformTarget(delta Vec3, weight float64) Vec3 {
	return Vec3{}
}

// SmoothWeight ramps prev towards target at rate for a dt step.
func (s *IkSolver) SmoothWeight(prev, target, dt, rate float64) float64 {
	return 0
}

// FallbackPose returns the pose to keep when the solve succeeded or failed.
func (s *IkSolver) FallbackPose(prev, next []Vec3, solved bool) []Vec3 {
	return nil
}

// LODWeight returns the IK weight used at the given LOD level.
func (s *IkSolver) LODWeight(lod int, base float64) float64 {
	return 0
}
