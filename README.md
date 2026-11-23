# GSB Bugfix Task

## 题面

Go SSAO 在远景出现黑边和噪点。采样核没有按深度缩放；半球采样方向没有和法线对齐；深度重建用了错误的投影逆矩阵；SSAO 在透明物体上也算；降噪没有做深度感知；采样半径按屏幕空间固定；SSAO 和间接光重复计算；边界采样越界；SSAO 强度受分辨率影响；相机切换后核没有重建；多线程写同一 AO 缓冲；SSAO 在 tonemap 之后做。

## 运行方式

go test ./...

## 平台字段

- domain: 游戏、图形与多媒体
- sub: 图形渲染
- type: Bugfix
- difficulty: 地狱
