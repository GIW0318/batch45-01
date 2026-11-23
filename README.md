# GSB Bugfix Task

## 题面

ACME 客户端在 Go 服务里做证书续期，攻击者重放 challenge 拿到了别人的证书。challenge token 没有和账户绑定；同一 token 可重复验证；nonce 没有一次性使用；订单状态机允许跳过授权直接签发；HTTP-01 路径没有校验 Host；DNS-01 记录没有校验 TXT 归属；重试没有上限，CA 被刷；账户密钥轮换后旧订单还能用；证书私钥在内存里明文；续期失败后回退到旧证书但不告警；多副本同时续期产生多张证书；吊销后没有清理缓存。

## 运行方式

```
go test ./...
```

## 平台字段

- domain: 安全
- sub: 身份认证
- type: Bugfix
- difficulty: 地狱
