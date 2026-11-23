package task

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"
)

const (
	// maxSolveIterations bounds the iterative solver; exceeding it
	// triggers an immediate fallback to the previous frame pose.
	maxSolveIterations = 32
	// maxOffsetEntries caps the per-bone offset table.
	maxOffsetEntries = 512
)

// Vec3 is a point or direction in world space.
type Vec3 struct {
	X float64
	Y float64
	Z float64
}

func (v Vec3) add(o Vec3) Vec3 { return Vec3{v.X + o.X, v.Y + o.Y, v.Z + o.Z} }
func (v Vec3) sub(o Vec3) Vec3 { return Vec3{v.X - o.X, v.Y - o.Y, v.Z - o.Z} }
func (v Vec3) scale(s float64) Vec3 {
	return Vec3{v.X * s, v.Y * s, v.Z * s}
}
func (v Vec3) dot(o Vec3) float64 { return v.X*o.X + v.Y*o.Y + v.Z*o.Z }
func (v Vec3) cross(o Vec3) Vec3 {
	return Vec3{
		X: v.Y*o.Z - v.Z*o.Y,
		Y: v.Z*o.X - v.X*o.Z,
		Z: v.X*o.Y - v.Y*o.X,
	}
}
func (v Vec3) len() float64 { return math.Sqrt(v.dot(v)) }

func (v Vec3) normalized() Vec3 {
	l := v.len()
	if l < 1e-12 {
		return Vec3{}
	}
	return v.scale(1 / l)
}

// IkSolver solves two bone leg IK for one character.
type IkSolver struct {
	mu      sync.Mutex
	offsets map[string]float64
	failed  atomic.Bool
}

// NewIkSolver creates an empty solver.
func NewIkSolver() *IkSolver {
	return &IkSolver{offsets: make(map[string]float64)}
}

// ProjectToPlane projects p onto the plane through the origin with the given normal.
func (s *IkSolver) ProjectToPlane(p, normal Vec3) Vec3 {
	d := normal.dot(normal)
	if d < 1e-12 {
		return p
	}
	return p.sub(normal.scale(p.dot(normal) / d))
}

// KneePole returns a unit pole vector perpendicular to the root->target axis
// and to forward. When the axis and forward are collinear a fallback axis is
// used so the result is never a zero vector.
func (s *IkSolver) KneePole(root, target, forward Vec3) Vec3 {
	axis := target.sub(root)
	if axis.len() < 1e-12 {
		axis = Vec3{Y: 1}
	}
	pole := axis.cross(forward)
	if pole.len() < 1e-9 {
		fallback := Vec3{Y: 1}
		if math.Abs(axis.normalized().dot(fallback)) > 0.999 {
			fallback = Vec3{X: 1}
		}
		pole = axis.cross(fallback)
	}
	return pole.normalized()
}

// ClampJointAngle clamps an angle in degrees to [min, max].
func (s *IkSolver) ClampJointAngle(angle, min, max float64) float64 {
	if min > max {
		min, max = max, min
	}
	if angle < min {
		return min
	}
	if angle > max {
		return max
	}
	return angle
}

// FootPitch returns the foot pitch in degrees for a ground normal: the angle
// between the normal and the vertical axis.
func (s *IkSolver) FootPitch(normal Vec3) float64 {
	l := normal.len()
	if l < 1e-12 {
		return 0
	}
	cos := normal.Y / l
	if cos > 1 {
		cos = 1
	}
	if cos < -1 {
		cos = -1
	}
	return math.Acos(cos) * 180 / math.Pi
}

// ResolveRootMotion blends the animated root with the IK corrected root.
func (s *IkSolver) ResolveRootMotion(anim, ik Vec3, weight float64) Vec3 {
	if weight < 0 {
		weight = 0
	}
	if weight > 1 {
		weight = 1
	}
	return anim.scale(1 - weight).add(ik.scale(weight))
}

// ClampToReach shortens target to maxLen from root and reports whether it was in range.
func (s *IkSolver) ClampToReach(root, target Vec3, maxLen float64) (Vec3, bool) {
	d := target.sub(root)
	l := d.len()
	if l <= maxLen || l < 1e-12 {
		return target, true
	}
	return root.add(d.scale(maxLen / l)), false
}

// SolveOrder returns the chain sorted from root to leaf using the parent map
// (child -> parent). Bones at the same depth are ordered by name.
func (s *IkSolver) SolveOrder(chain []string, hierarchy map[string]string) []string {
	depth := func(bone string) int {
		d := 0
		for p, ok := hierarchy[bone]; ok; p, ok = hierarchy[p] {
			d++
			if d > len(hierarchy)+1 {
				break
			}
		}
		return d
	}
	ordered := make([]string, len(chain))
	copy(ordered, chain)
	sort.SliceStable(ordered, func(i, j int) bool {
		di, dj := depth(ordered[i]), depth(ordered[j])
		if di != dj {
			return di < dj
		}
		return ordered[i] < ordered[j]
	})
	return ordered
}

// AddOffset accumulates a per bone X offset requested by an IK task.
// The accumulation is atomic across goroutines and the table is capped at
// maxOffsetEntries bones.
func (s *IkSolver) AddOffset(bone string, dx float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.offsets[bone]; !ok && len(s.offsets) >= maxOffsetEntries {
		return
	}
	s.offsets[bone] += dx
}

// Offset returns the accumulated X offset of a bone.
func (s *IkSolver) Offset(bone string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.offsets[bone]
}

// PlatformTarget converts moving platform motion into an IK target offset.
func (s *IkSolver) PlatformTarget(delta Vec3, weight float64) Vec3 {
	return delta.scale(weight)
}

// SmoothWeight ramps prev towards target at rate for a dt step without
// overshooting, keeping the result inside [0, 1].
func (s *IkSolver) SmoothWeight(prev, target, dt, rate float64) float64 {
	step := rate * dt
	if step < 0 {
		step = -step
	}
	var w float64
	if target > prev {
		w = math.Min(prev+step, target)
	} else {
		w = math.Max(prev-step, target)
	}
	if w < 0 {
		return 0
	}
	if w > 1 {
		return 1
	}
	return w
}

// FallbackPose returns the pose to keep when the solve succeeded or failed.
// On failure a copy of the previous frame pose is returned so the caller can
// never observe a partially written new pose.
func (s *IkSolver) FallbackPose(prev, next []Vec3, solved bool) []Vec3 {
	src := next
	if !solved {
		src = prev
	}
	out := make([]Vec3, len(src))
	copy(out, src)
	return out
}

// LODWeight returns the IK weight used at the given LOD level, halving the
// base weight per level so LOD switches do not pop.
func (s *IkSolver) LODWeight(lod int, base float64) float64 {
	if lod <= 0 {
		return base
	}
	w := base * math.Pow(0.5, float64(lod))
	if w < 0 {
		return 0
	}
	return w
}

// Failed reports whether the last Solve hit the iteration limit and fell back.
func (s *IkSolver) Failed() bool {
	return s.failed.Load()
}

// Solve iteratively moves the chain tip towards target starting from prev.
// It runs at most maxSolveIterations iterations; if the tip has not converged
// by then it marks the solve as failed and returns the previous frame pose
// instead of looping forever.
func (s *IkSolver) Solve(prev []Vec3, target Vec3, tolerance float64) []Vec3 {
	if len(prev) == 0 {
		s.failed.Store(false)
		return nil
	}
	pose := make([]Vec3, len(prev))
	copy(pose, prev)
	s.failed.Store(false)
	for iter := 0; iter < maxSolveIterations; iter++ {
		tip := pose[len(pose)-1]
		if tip.sub(target).len() <= tolerance {
			return pose
		}
		// Pull the tip halfway towards the target each iteration.
		pose[len(pose)-1] = tip.add(target.sub(tip).scale(0.5))
	}
	// Iteration budget exhausted: flag the failure and roll back.
	s.failed.Store(true)
	out := make([]Vec3, len(prev))
	copy(out, prev)
	return out
}
