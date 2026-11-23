# GSB Bugfix Task

## 题面

Go 后处理 Bloom 在 HDR 场景里高光溢出到整个屏幕。亮度阈值按 SDR 算，HDR 值全过；降采样没有做 Karis 平均，闪烁；升采样没有加权，出现方块；Bloom 和 UI 一起处理；多分辨率 mip 链没有重建；Bloom 强度受帧率影响；高光颜色没有做色调映射；Bloom 在透明物体之后做，深度错误；Bloom 缓冲没有清空；相机切换后旧 Bloom 残留；Bloom 和曝光顺序反了；多线程写同一 mip。

## 运行方式

go test ./...

## 平台字段

- domain: 游戏、图形与多媒体
- sub: 图形渲染
- type: Bugfix
- difficulty: 地狱
