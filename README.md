# GSB Bugfix Task

## 题面

SRv6 控制器在 Go 里下发段列表后，部分流量被导到了错误节点。SID 没有按 locator 校验归属；段列表长度超过硬件上限没有拒绝；压缩 SID 的解压表不一致；End.DT4 和 End.DX4 行为混用；同一 BSID 被两个策略绑定；候选路径没有做环路检测；SID 列表里的重复段没有去重；封装后的外层 TTL 没有设置；SRH 的 HMAC 没有校验；策略删除后 SID 没有回收；控制器重启后段列表丢失；多租户的 SID 空间冲突。

## 运行方式

```bash
go test ./...
```

## 平台字段

- domain: 安全
- sub: 网络与通信安全
- type: Bugfix
- difficulty: 地狱
