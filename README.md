# GSB Bugfix Task

## 题面

Go 写的 HTTP/2 网关在压测时被 Rapid Reset 打穿，连接数瞬间翻十倍。服务端收到 HEADERS 后立刻分配 stream，没有检查并发上限；RST_STREAM 到达后 stream 状态没有立即回收，等超时才释放；SETTINGS 里的 MAX_CONCURRENT_STREAMS 只对新连接生效；同一连接内 stream ID 回绕后没有拒绝；GOAWAY 之后还能新建 stream；CONTINUATION 帧可以无限延长 header 块；优先级树被恶意构造，CPU 打满；流控窗口只按连接算，不按 stream 算；收到未知帧类型直接忽略；PUSH_PROMISE 没有校验关联 stream；header 列表大小没有上限；连接迁移后旧 stream 还能写。

## 运行方式

```
go test ./...
```

## 平台字段

- domain: 安全
- sub: 网络与通信安全
- type: Bugfix
- difficulty: 地狱
