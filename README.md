# GSB Bugfix Task

## 题面

RADIUS 代理记账在 Go 服务里出现重复计费和用户串会话。Acct-Session-Id 没有和 NAS 地址绑定，两台 NAS 同 ID 互相覆盖；代理重启后未完成的记账请求丢失；重传的 Accounting-Request 被当成新会话；Start/Interim/Stop 顺序没有校验，Stop 先到也接受；同一用户并发登录没有会话上限；代理转发时改了 Acct-Delay-Time 但没有更新原始时间戳；共享密钥按域而不是按 NAS；请求认证器校验失败仍转发；计费响应丢失后没有重试队列；代理链路上重复转发没有去重；会话超时后没有自动 Stop；重启后会话表没有恢复。

## 运行方式

```
go test ./...
```

## 平台字段

- domain: 安全
- sub: 网络与通信安全
- type: Bugfix
- difficulty: 地狱
