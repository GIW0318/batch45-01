# GSB Bugfix Task

## 题面

TLS 1.3 会话恢复在滚动发布后出现跨版本串会话。Session ticket 的加密密钥没有按版本隔离，新版本能解旧 ticket；ticket 里的 ALPN 和客户端当前值不比对，换协议也能恢复；0-RTT 允许的早期数据套件没有随策略更新；ticket 生命周期按签发时间算而不是按首次使用时间算；同一 client 的多个 ticket 并发恢复，后完成的覆盖先完成；集群节点各自生成 ticket 密钥，负载均衡后解不开；ticket 被重放时没有吊销对应 PSK；恢复握手没有校验 SNI 与证书一致性；会话缓存键漏了客户端证书指纹；ticket 轮换时旧密钥立即删除，在途恢复全部失败；会话恢复后没有重新协商密钥更新；缓存满了直接淘汰活跃会话。

## 运行方式

```bash
go test ./...
```

## 平台字段

- domain: 安全
- sub: 网络与通信安全
- type: Bugfix
- difficulty: 地狱
