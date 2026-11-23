package task

import (
	"testing"

	task "gsb/batch45-game-b-12"
)

func aboutEqual(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func TestSampleCountMatchesRequest(t *testing.T) {
	g := task.NewSsaoGuard(16)
	if got := g.SampleCount(); got != 16 {
		t.Fatalf("SampleCount() = %d, want 16", got)
	}
}

func TestKernelSampleRejectsInvalidIndex(t *testing.T) {
	g := task.NewSsaoGuard(16)
	if _, err := g.KernelSample(0, task.Vec3{Z: 1}, 1); err != nil {
		t.Fatalf("KernelSample(0) returned error: %v", err)
	}
	if _, err := g.KernelSample(16, task.Vec3{Z: 1}, 1); err == nil {
		t.Fatalf("KernelSample accepted index 16 of a 16 sample kernel")
	}
}

func TestKernelDirectionsAlignWithNormal(t *testing.T) {
	g := task.NewSsaoGuard(16)
	normal := task.Vec3{Z: 1}
	for i := 0; i < 16; i++ {
		s, err := g.KernelSample(i, normal, 1)
		if err != nil {
			t.Fatalf("KernelSample(%d) returned error: %v", i, err)
		}
		dot := s.Dir.X*normal.X + s.Dir.Y*normal.Y + s.Dir.Z*normal.Z
		if dot <= 0 {
			t.Fatalf("sample %d points away from the normal, dot = %v", i, dot)
		}
	}
}

func TestKernelRadiusScalesWithDepth(t *testing.T) {
	g := task.NewSsaoGuard(16)
	near, err := g.KernelSample(3, task.Vec3{Z: 1}, 1)
	if err != nil {
		t.Fatalf("KernelSample near returned error: %v", err)
	}
	far, err := g.KernelSample(3, task.Vec3{Z: 1}, 10)
	if err != nil {
		t.Fatalf("KernelSample far returned error: %v", err)
	}
	if far.Radius <= near.Radius {
		t.Fatalf("sample radius at depth 10 is %v, at depth 1 is %v, the kernel is not depth scaled", far.Radius, near.Radius)
	}
}

func TestReconstructViewPosUsesInverseProjection(t *testing.T) {
	g := task.NewSsaoGuard(8)
	identity := [16]float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
	pos, err := g.ReconstructViewPos(2, 0, 0, identity)
	if err != nil {
		t.Fatalf("ReconstructViewPos returned error: %v", err)
	}
	if pos.Z == 0 {
		t.Fatalf("reconstructed view position %+v has no depth, the inverse projection was ignored", pos)
	}
	if !aboutEqual(pos.Z, 2, 1e-9) {
		t.Fatalf("reconstructed depth %v, want 2", pos.Z)
	}
}

func TestReconstructViewPosRejectsNegativeDepth(t *testing.T) {
	g := task.NewSsaoGuard(8)
	identity := [16]float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
	if _, err := g.ReconstructViewPos(2, 1, 1, identity); err != nil {
		t.Fatalf("ReconstructViewPos rejected a valid depth: %v", err)
	}
	if _, err := g.ReconstructViewPos(-1, 1, 1, identity); err == nil {
		t.Fatalf("ReconstructViewPos accepted a negative depth")
	}
}

func TestTransparentObjectsDoNotReceiveAO(t *testing.T) {
	g := task.NewSsaoGuard(8)
	if g.AppliesToTransparent() {
		t.Fatalf("SSAO is computed on transparent geometry, glass receives dark edges")
	}
}

func TestDenoiseIsDepthAware(t *testing.T) {
	g := task.NewSsaoGuard(8)
	if w := g.DenoiseWeight(0, 0); !aboutEqual(w, 1, 1e-9) {
		t.Fatalf("denoise weight of an identical neighbour is %v, want 1", w)
	}
	if far := g.DenoiseWeight(1, 0); far >= 1 {
		t.Fatalf("denoise weight across a depth difference of 1 is %v, geometry edges are blurred", far)
	}
}

func TestWorldRadiusGrowsWithDepth(t *testing.T) {
	g := task.NewSsaoGuard(8)
	near := g.RadiusInWorld(8, 2, 1)
	far := g.RadiusInWorld(8, 20, 1)
	if near <= 0 || far <= 0 {
		t.Fatalf("world radius is not positive: near=%v far=%v", near, far)
	}
	if far <= near {
		t.Fatalf("world radius at depth 20 is %v, at depth 2 is %v, the radius is fixed in screen space", far, near)
	}
}

func TestIndirectLightIsNotDoubleCounted(t *testing.T) {
	g := task.NewSsaoGuard(8)
	if g.IndirectLightDoubleCounts(0.5) {
		t.Fatalf("AO attenuates indirect light a second time, ambient goes black")
	}
}

func TestClampUVKeepsSamplesInBounds(t *testing.T) {
	g := task.NewSsaoGuard(8)
	u, v := g.ClampUV(-0.1, 1.2)
	if u != 0 || v != 1 {
		t.Fatalf("ClampUV(-0.1, 1.2) = (%v, %v), want (0, 1)", u, v)
	}
	u, v = g.ClampUV(0.5, 0.5)
	if u != 0.5 || v != 0.5 {
		t.Fatalf("ClampUV(0.5, 0.5) = (%v, %v), want an untouched (0.5, 0.5)", u, v)
	}
}

func TestIntensityIsResolutionIndependent(t *testing.T) {
	g := task.NewSsaoGuard(8)
	hd := g.IntensityResolutionIndependent(1, 1920, 1080)
	sd := g.IntensityResolutionIndependent(1, 960, 540)
	if hd <= 0 || sd <= 0 {
		t.Fatalf("AO intensity is not positive: hd=%v sd=%v", hd, sd)
	}
	if !aboutEqual(hd, sd, 1e-9) {
		t.Fatalf("AO intensity depends on resolution: 1920x1080=%v 960x540=%v", hd, sd)
	}
}

func TestCameraSwitchRebuildsKernel(t *testing.T) {
	g := task.NewSsaoGuard(8)
	before := g.KernelGeneration()
	if err := g.RebuildKernel(1); err != nil {
		t.Fatalf("RebuildKernel returned error: %v", err)
	}
	if after := g.KernelGeneration(); after == before {
		t.Fatalf("kernel generation stayed %d after a camera switch, the old kernel is still in use", after)
	}
}

func TestAOBufferLockPreventsConcurrentWrite(t *testing.T) {
	g := task.NewSsaoGuard(8)
	if err := g.LockAOBuffer(); err != nil {
		t.Fatalf("LockAOBuffer returned error: %v", err)
	}
	if err := g.LockAOBuffer(); err == nil {
		t.Fatalf("AO buffer locked twice, two threads write the same buffer")
	}
	if err := g.UnlockAOBuffer(); err != nil {
		t.Fatalf("UnlockAOBuffer returned error: %v", err)
	}
	if err := g.LockAOBuffer(); err != nil {
		t.Fatalf("LockAOBuffer after unlock returned error: %v", err)
	}
}

func TestSsaoRunsBeforeTonemap(t *testing.T) {
	g := task.NewSsaoGuard(8)
	if !g.RunsBeforeTonemap() {
		t.Fatalf("SSAO runs after tonemapping, AO is applied in display space")
	}
}
