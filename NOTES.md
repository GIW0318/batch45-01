# BloomChain Bugfix 说明

模块 `gsb/batch45-game-b-09`，修复室内 HDR 高光糊屏。全部改动在
`bloom_chain.go`，测试未改动、未跳过、未写死。

## 问题与修复

- **阈值按 SDR 算，HDR 全越界**：`BrightPass` 直接用 Rec.709 亮度与
  HDR 阈值比较，不再先做 SDR 钳制；高于阈值保留原 HDR 值，低于置零。
  `threshold <= 0` 返回错误。
- **降采样直接平均，高光闪**：`Downsample` 每个 2x2 cell 走
  `KarisAverage`，中心 tap 占一半权重，边缘 tap 平分另一半，压制单像素
  高亮闪烁；奇数尺寸边缘 clamp 取样。
- **升采样等权出块**：`UpsampleWeighted` 按权重归一化合并各 mip
  （每层先取均值再加权）；权重个数与层数不符、负权重、权重和为零、
  空层均报错。
- **UI 混进 Bloom**：`IncludesUI` 返回 false，UI 不进 Bloom 链。
- **mip 层数不符**：`NewBloomChain` 记录请求的层数，`MipCount` 原样
  返回；层数封顶 `maxMips = 8`，分辨率封顶 16384，缓冲有界。
- **强度按帧累加，30/60Hz 不一致**：`AccumulateIntensity` 改为按秒的
  指数平滑（`alpha = 1 - exp(-dt/tau)`，`tau = 0.25s`），结果只取决于
  墙钟时间，与帧率无关。
- **高光裁白**：`TonemapHighlight` 用 Reinhard 软滚降 `x/(1+x)`，
  高光平滑压到 1.0 以下而不是硬裁。
- **透明 pass 后合成，深度错**：`CompositeAfterTransparent` 返回
  false，Bloom 在透明 pass 之前合成。
- **缓冲不清，切场景残留**：`ClearBuffers` 清零全部 mip 缓冲并置
  clean 标记，`BuffersClean` 可查询；`AdvanceFrame` 后标记为脏。
- **换相机旧累积还在**：`SetCamera` 重置帧计数、强度累积并清空缓冲；
  负 id 报错。
- **曝光排在 Bloom 后**：`ExposureBeforeBloom` 返回 true。
- **多线程写同一 mip**：所有可变状态由一把 `sync.Mutex` 保护，
  `BuildMipChain` 整条 mip 链在一次持锁内写完，读者（`ReadMip`）
  不会看到半帧新半帧旧；`-race` 干净。

## 缓冲复用约束

`ensureBuffers` 按分辨率建缓冲：同一分辨率下各层缓冲只建一次，之后
每帧复用（`BufferAllocations` 不变）；只有分辨率变化才重建。切场景后
若每帧新建纹理即判缺陷，`cmd/selfcheck` 对此有断言。

## 自验

`cmd/selfcheck/main.go`（`go run -race ./cmd/selfcheck`）覆盖单元测试
未含的两条约束：

1. 分辨率切换：64x64 多帧只分配 1 次，切 128x128 分配第 2 次，之后
   多帧不再分配。
2. 并发：8 个写 goroutine 各写 200 帧不同值的均匀帧，读 goroutine
   持续校验 mip 0 所有 texel 一致（无撕裂帧）；另有强度/相机/清缓冲
   并发压测，race detector 干净。

验收输出见 `RECORD.md`。
