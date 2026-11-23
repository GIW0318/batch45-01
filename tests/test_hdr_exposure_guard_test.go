package task

import (
	"testing"

	task "gsb/batch45-game-b-13"
)

func within(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func TestMeterFrameAveragesWholeFrame(t *testing.T) {
	e := task.NewHdrExposure()
	got, err := e.MeterFrame([]float64{0.1, 0.2, 0.9})
	if err != nil {
		t.Fatalf("MeterFrame returned error: %v", err)
	}
	if !within(got, 0.4, 1e-9) {
		t.Fatalf("metered luminance = %v, want the frame average 0.4 instead of the centre point", got)
	}
}

func TestMeterFrameRejectsEmptyFrame(t *testing.T) {
	e := task.NewHdrExposure()
	if _, err := e.MeterFrame([]float64{0.5}); err != nil {
		t.Fatalf("MeterFrame rejected a single sample frame: %v", err)
	}
	if _, err := e.MeterFrame(nil); err == nil {
		t.Fatalf("MeterFrame accepted an empty frame")
	}
}

func TestMeterFrameAdvancesIndex(t *testing.T) {
	e := task.NewHdrExposure()
	if got := e.MeterFrameIndex(); got != 0 {
		t.Fatalf("MeterFrameIndex() = %d on a fresh guard, want 0", got)
	}
	if _, err := e.MeterFrame([]float64{0.5}); err != nil {
		t.Fatalf("MeterFrame returned error: %v", err)
	}
	if _, err := e.MeterFrame([]float64{0.6}); err != nil {
		t.Fatalf("MeterFrame returned error: %v", err)
	}
	if got := e.MeterFrameIndex(); got != 2 {
		t.Fatalf("MeterFrameIndex() = %d after two frames, want 2", got)
	}
}

func TestSmoothExposureIsFrameRateIndependent(t *testing.T) {
	e := task.NewHdrExposure()
	at30 := e.SmoothExposure(0, 1, 1.0/30.0)
	b1 := e.SmoothExposure(0, 1, 1.0/60.0)
	at60 := e.SmoothExposure(b1, 1, 1.0/60.0)
	if at30 <= 0 || at60 <= 0 {
		t.Fatalf("exposure does not adapt: 30Hz=%v 60Hz=%v", at30, at60)
	}
	if !within(at30, at60, 0.02) {
		t.Fatalf("exposure smoothing depends on frame rate: 30Hz=%v 60Hz=%v", at30, at60)
	}
}

func TestHistogramWeightsFavourBrightPixels(t *testing.T) {
	e := task.NewHdrExposure()
	weights, err := e.HistogramWeights([]float64{0.1, 1.0})
	if err != nil {
		t.Fatalf("HistogramWeights returned error: %v", err)
	}
	if len(weights) != 2 {
		t.Fatalf("HistogramWeights returned %d weights, want 2", len(weights))
	}
	if weights[1] <= weights[0] {
		t.Fatalf("bright pixel weight %v is not above dark pixel weight %v", weights[1], weights[0])
	}
}

func TestHistogramWeightsRejectEmptyInput(t *testing.T) {
	e := task.NewHdrExposure()
	if _, err := e.HistogramWeights([]float64{0.5}); err != nil {
		t.Fatalf("HistogramWeights rejected a single sample: %v", err)
	}
	if _, err := e.HistogramWeights(nil); err == nil {
		t.Fatalf("HistogramWeights accepted an empty frame")
	}
}

func TestTonemapHighlightRollsOff(t *testing.T) {
	e := task.NewHdrExposure()
	got := e.TonemapHighlight(task.RGB{R: 10, G: 10, B: 10})
	if got.R > 1 || got.G > 1 || got.B > 1 {
		t.Fatalf("tonemapped highlight %+v still exceeds 1.0 and clips to white", got)
	}
	if got.R < 0.5 {
		t.Fatalf("tonemapped highlight %v is crushed to black", got.R)
	}
}

func TestUIExemptFromTonemap(t *testing.T) {
	e := task.NewHdrExposure()
	if e.AppliesTonemapToUI() {
		t.Fatalf("the UI pass is tonemapped with the scene, HUD colours distort")
	}
}

func TestOutputGammaMatchesSRGB(t *testing.T) {
	e := task.NewHdrExposure()
	if !e.OutputGammaIsSRGB() {
		t.Fatalf("the SDR output does not use the sRGB curve, HDR and SDR gamma disagree")
	}
}

func TestNegativeCompensationDoesNotBlackOut(t *testing.T) {
	e := task.NewHdrExposure()
	half, err := e.ExposureCompensation(-1, 2)
	if err != nil {
		t.Fatalf("ExposureCompensation returned error: %v", err)
	}
	if !within(half, 1, 1e-9) {
		t.Fatalf("-1 EV on base 2 gives %v, want 1", half)
	}
	dark, err := e.ExposureCompensation(-20, 1)
	if err != nil {
		t.Fatalf("ExposureCompensation returned error: %v", err)
	}
	if dark <= 0 {
		t.Fatalf("-20 EV gives %v, a negative compensation blacks the screen out", dark)
	}
}

func TestResetMeterClearsMetering(t *testing.T) {
	e := task.NewHdrExposure()
	if _, err := e.MeterFrame([]float64{0.5}); err != nil {
		t.Fatalf("MeterFrame returned error: %v", err)
	}
	if err := e.ResetMeter(); err != nil {
		t.Fatalf("ResetMeter returned error: %v", err)
	}
	if got := e.MeterFrameIndex(); got != 0 {
		t.Fatalf("MeterFrameIndex() = %d after ResetMeter, the old metering survived the scene change", got)
	}
}

func TestLutRunsAfterExposure(t *testing.T) {
	e := task.NewHdrExposure()
	if e.LutBeforeExposure() {
		t.Fatalf("the colour LUT runs before exposure, the LUT input range is wrong")
	}
}

func TestCameraSwitchResetsMetering(t *testing.T) {
	e := task.NewHdrExposure()
	if _, err := e.MeterFrame([]float64{0.5}); err != nil {
		t.Fatalf("MeterFrame returned error: %v", err)
	}
	before := e.ExposureGeneration()
	if err := e.UseCameraExposure(1); err != nil {
		t.Fatalf("UseCameraExposure returned error: %v", err)
	}
	if after := e.ExposureGeneration(); after == before {
		t.Fatalf("exposure generation stayed %d after switching cameras", after)
	}
	if got := e.MeterFrameIndex(); got != 0 {
		t.Fatalf("MeterFrameIndex() = %d after a camera switch, the previous camera exposure leaked", got)
	}
}

func TestClearBuffersMarksClean(t *testing.T) {
	e := task.NewHdrExposure()
	if err := e.ClearBuffers(); err != nil {
		t.Fatalf("ClearBuffers returned error: %v", err)
	}
	if !e.BuffersClean() {
		t.Fatalf("exposure buffers still hold data after ClearBuffers")
	}
}

func TestParticlesAreTonemappedOnce(t *testing.T) {
	e := task.NewHdrExposure()
	if e.TonemapPerParticleLayer() {
		t.Fatalf("every transparent particle layer is tonemapped separately")
	}
}
