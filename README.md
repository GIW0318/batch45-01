# GSB Bugfix Task

## 题面

Go 表情混合在对话系统里出现面部扭曲。Morph target 的权重和超过 1 没有归一；同一帧多个表情叠加顺序不稳定；Morph 和骨骼动画的时间轴不同步；表情切换没有过渡；Morph target 的顶点索引在 LOD 之间不一致；权重为负没有拒绝；表情缓存没有按角色隔离；多线程更新同一权重数组；表情播放完毕没有复位；Morph 数据压缩后精度丢失；表情和语音不同步；表情在阴影 pass 没有应用。

## 运行方式

go test ./...

## 平台字段

- domain: 游戏、图形与多媒体
- sub: 图形渲染
- type: Bugfix
- difficulty: 地狱
