# GSB Bugfix Task

## 题面

Go 实现的 IKEv2 响应端在弱网下出现 SA 双发和重放。IKE_SA_INIT 的 cookie 没有绑定源地址；同一 SPIi 并发发起产生两套 SA；重传的 IKE_AUTH 被当成新请求；NONCE 没有一次性使用；DH 组不匹配时降级；SA 生命周期没有按双方较小值取；CHILD_SA 删除后父 SA 没有清理；EAP 身份和证书身份没有绑定；NAT 探测载荷没有校验；流量选择器允许了 0.0.0.0/0；重钥时旧 SA 立即删除；重启后 SA 没有恢复。

## 运行方式

```bash
go test ./...
```

## 平台字段

- domain: 安全
- sub: 网络与通信安全
- type: Bugfix
- difficulty: 地狱
