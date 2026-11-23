# 验收记录

日期: 2026-11-24 02:25:48 CST
Go: go version go1.23.4 linux/amd64

## go vet ./...
exit: 0

## go test ./... -v
?   	gsb/batch45-game-b-09	[no test files]
?   	gsb/batch45-game-b-09/cmd/selfcheck	[no test files]
=== RUN   TestBrightPassKeepsHDRHighlights
--- PASS: TestBrightPassKeepsHDRHighlights (0.00s)
=== RUN   TestBrightPassRejectsNonPositiveThreshold
--- PASS: TestBrightPassRejectsNonPositiveThreshold (0.00s)
=== RUN   TestKarisAverageWeightsCentreTap
--- PASS: TestKarisAverageWeightsCentreTap (0.00s)
=== RUN   TestDownsampleHalvesResolution
--- PASS: TestDownsampleHalvesResolution (0.00s)
=== RUN   TestDownsampleAveragesCell
--- PASS: TestDownsampleAveragesCell (0.00s)
=== RUN   TestUpsampleWeightedPrefersBrighterMip
--- PASS: TestUpsampleWeightedPrefersBrighterMip (0.00s)
=== RUN   TestUpsampleRejectsWeightMismatch
--- PASS: TestUpsampleRejectsWeightMismatch (0.00s)
=== RUN   TestUIExcludedFromBloom
--- PASS: TestUIExcludedFromBloom (0.00s)
=== RUN   TestMipCountMatchesRequest
--- PASS: TestMipCountMatchesRequest (0.00s)
=== RUN   TestIntensityIsFrameRateIndependent
--- PASS: TestIntensityIsFrameRateIndependent (0.00s)
=== RUN   TestTonemapHighlightRollsOff
--- PASS: TestTonemapHighlightRollsOff (0.00s)
=== RUN   TestClearBuffersMarksClean
--- PASS: TestClearBuffersMarksClean (0.00s)
=== RUN   TestCameraSwitchResetsAccumulation
--- PASS: TestCameraSwitchResetsAccumulation (0.00s)
=== RUN   TestExposureRunsBeforeBloom
--- PASS: TestExposureRunsBeforeBloom (0.00s)
=== RUN   TestBloomCompositesBeforeTransparent
--- PASS: TestBloomCompositesBeforeTransparent (0.00s)
PASS
ok  	gsb/batch45-game-b-09/tests	0.003s

## go test -race ./...
?   	gsb/batch45-game-b-09	[no test files]
?   	gsb/batch45-game-b-09/cmd/selfcheck	[no test files]
ok  	gsb/batch45-game-b-09/tests	(cached)

## go run -race ./cmd/selfcheck
ok: buffers allocated once per resolution, reused every frame
ok: 8 writers x 200 frames, no torn frame observed
ok: concurrent intensity/camera/buffer access, race detector clean
SELF-CHECK OK
