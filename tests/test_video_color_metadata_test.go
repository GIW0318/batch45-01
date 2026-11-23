package task

import (
	"testing"

	task "gsb/batch45-game-b-23"
)

func near(a, b, eps float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

func TestMasteringDisplayRoundTrip(t *testing.T) {
	c := task.NewColorMetadata()
	md := task.MasteringDisplay{Primaries: "bt2020", MaxLuminance: 1000, MinLuminance: 0.005}
	if err := c.AttachMastering(md); err != nil {
		t.Fatalf("AttachMastering returned error: %v", err)
	}
	got, ok := c.Mastering()
	if !ok {
		t.Fatal("mastering display metadata was dropped")
	}
	if got.Primaries != "bt2020" || !near(got.MaxLuminance, 1000, 1e-9) {
		t.Fatalf("Mastering = %+v, want bt2020 at 1000 nits", got)
	}
}

func TestContentLightRoundTrip(t *testing.T) {
	c := task.NewColorMetadata()
	if err := c.AttachContentLight(task.ContentLight{MaxCLL: 1000, MaxFALL: 400}); err != nil {
		t.Fatalf("AttachContentLight returned error: %v", err)
	}
	got, ok := c.ContentLightLevels()
	if !ok {
		t.Fatal("content light metadata was dropped")
	}
	if got.MaxCLL != 1000 || got.MaxFALL != 400 {
		t.Fatalf("ContentLightLevels = %+v, want MaxCLL 1000 and MaxFALL 400", got)
	}
}

func TestPrimariesConversionName(t *testing.T) {
	c := task.NewColorMetadata()
	got, err := c.PrimariesConversion("bt2020", "bt709")
	if err != nil {
		t.Fatalf("PrimariesConversion returned error: %v", err)
	}
	if got != "bt2020-to-bt709" {
		t.Fatalf("PrimariesConversion = %q, want bt2020-to-bt709", got)
	}
}

func TestTonemapPeakMapsToReference(t *testing.T) {
	c := task.NewColorMetadata()
	if got := c.TonemapCurve(1000, 1000); !near(got, 100, 1e-6) {
		t.Fatalf("TonemapCurve(peak, peak) = %v, want 100", got)
	}
}

func TestTonemapMidTone(t *testing.T) {
	c := task.NewColorMetadata()
	if got := c.TonemapCurve(250, 1000); !near(got, 50, 1e-6) {
		t.Fatalf("TonemapCurve(250, 1000) = %v, want 50", got)
	}
}

func TestChromaLocationPreserved(t *testing.T) {
	c := task.NewColorMetadata()
	if got := c.ChromaLocation("center"); got != "center" {
		t.Fatalf("ChromaLocation(center) = %q, want center", got)
	}
}

func TestColorRangeLimitedTag(t *testing.T) {
	c := task.NewColorMetadata()
	if got := c.ColorRangeTag(false); got != "limited" {
		t.Fatalf("ColorRangeTag(false) = %q, want limited", got)
	}
}

func TestAllTracksProcessed(t *testing.T) {
	c := task.NewColorMetadata()
	got := c.ProcessedTracks(3)
	if len(got) != 3 {
		t.Fatalf("ProcessedTracks(3) = %v, want three tracks", got)
	}
	for i, idx := range got {
		if idx != i {
			t.Fatalf("ProcessedTracks(3) = %v, want [0 1 2]", got)
		}
	}
}

func TestMuxKeepsMetadata(t *testing.T) {
	c := task.NewColorMetadata()
	in := map[string]string{"mastering": "bt2020", "cll": "1000"}
	got := c.MuxMetadata(in)
	if len(got) != 2 {
		t.Fatalf("MuxMetadata kept %d keys, want 2", len(got))
	}
	if got["mastering"] != "bt2020" {
		t.Fatalf("MuxMetadata dropped the mastering key: %v", got)
	}
}

func TestFilterOrderBeforeTonemap(t *testing.T) {
	c := task.NewColorMetadata()
	got := c.FilterOrder()
	if len(got) != 3 {
		t.Fatalf("FilterOrder = %v, want three stages", got)
	}
	if got[0] != "colorspace" || got[1] != "tonemap" {
		t.Fatalf("FilterOrder = %v, want colorspace before tonemap", got)
	}
}

func TestDitherClampsAtMax(t *testing.T) {
	c := task.NewColorMetadata()
	if got := c.Dither10to8(1023, 1); got != 255 {
		t.Fatalf("Dither10to8(1023) = %d, want 255", got)
	}
}

func TestDitherNotTruncation(t *testing.T) {
	c := task.NewColorMetadata()
	differs := false
	for seed := uint32(0); seed < 8; seed++ {
		got := int(c.Dither10to8(512, seed))
		if got < 127 || got > 129 {
			t.Fatalf("Dither10to8(512, %d) = %d, want within one LSB of 128", seed, got)
		}
		if got != 128 {
			differs = true
		}
	}
	if !differs {
		t.Fatal("10 bit to 8 bit conversion is plain truncation, no dither was applied")
	}
}

func TestEncoderTagIncludesPrimaries(t *testing.T) {
	c := task.NewColorMetadata()
	if got := c.EncoderTag("hevc", "bt2020"); got != "hevc/bt2020" {
		t.Fatalf("EncoderTag = %q, want hevc/bt2020", got)
	}
}

func TestCommitOutputNoPartialFile(t *testing.T) {
	c := task.NewColorMetadata()
	path, err := c.CommitOutput(true, "out.mp4")
	if err != nil {
		t.Fatalf("CommitOutput(success) returned error: %v", err)
	}
	if path != "out.mp4" {
		t.Fatalf("CommitOutput(success) = %q, want out.mp4", path)
	}
	bad, err := c.CommitOutput(false, "out.mp4")
	if err == nil {
		t.Fatal("CommitOutput reported success for a failed transcode")
	}
	if bad != "" {
		t.Fatalf("CommitOutput published the half written file %q", bad)
	}
}

func TestThumbnailFollowsColorConversion(t *testing.T) {
	c := task.NewColorMetadata()
	if got := c.ThumbnailPrimaries("bt2020"); got != "bt709" {
		t.Fatalf("ThumbnailPrimaries(bt2020) = %q, want bt709", got)
	}
}
