# GSB Bugfix Task

## 题面

Go 调色 LUT 在图集边缘出现色带和串格。LUT 采样没有钳位到边界；三线性插值跨了相邻格；LUT 尺寸为 0 时没有拒绝；LUT 更新不是原子的，采样到半新半旧；LUT 的 sRGB 和线性空间混用；不同场景共用一张 LUT，切换后残留；LUT 预览和成片用的采样不同；LUT 动画帧按渲染帧累加；LUT 图集页分配冲突；LUT 导出丢失精度；多线程更新同一 LUT；LUT 在 UI 之后应用。

## 运行方式

go test ./...

## 平台字段

- domain: 游戏、图形与多媒体
- sub: 图形渲染
- type: Bugfix
- difficulty: 地狱
