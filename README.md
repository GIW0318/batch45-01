# GSB Bugfix Task

## 题面

机密计算 attestation 验证器在 Go 里接受了一份过期的 TEE 报告。报告签名链没有验到根；nonce 没有和请求绑定；报告里的 enclave 测量值不在白名单；TCB 版本低于最低要求也放行；报告过期后没有拒绝；同一 nonce 并发验证两次都成功；attestation 服务返回的证书链顺序不对时整体失败；不同 TEE 类型的报告混用；报告里的公钥没有和 TLS 证书绑定；验证缓存没有按测量值分区；服务重启后 nonce 缓存清零；验证失败后回退到软件模式。

## 运行方式

```
go test ./...
```

## 平台字段

- domain: 安全
- sub: 系统安全
- type: Bugfix
- difficulty: 地狱
