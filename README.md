# GSB Bugfix Task

## 题面

Go 视频转码后 HDR 片源在 SDR 电视上偏灰，颜色元数据丢失。转码没有保留 mastering display 和 content light；色彩原色和传递函数没有转换；HDR 到 SDR 的色调映射用了固定曲线；色度采样位置没有处理；转码后 color range 标记错成 full；多轨道视频只处理了第一轨；元数据在封装时被丢弃；转码和滤镜顺序反了；10bit 转 8bit 没有抖动；不同编码器的色彩标记不一致；转码失败后输出半成品；缩略图没有跟随色彩转换。

## 运行方式

go test ./...

## 平台字段

- domain: 游戏、图形与多媒体
- sub: 音视频处理
- type: Bugfix
- difficulty: 地狱
