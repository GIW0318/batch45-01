# GSB Bugfix Task

## 题面

OAuth 令牌交换接上 DPoP 后，安全团队用重放的 proof 换到了新令牌。proof 的 jti 没有落库，窗口内可重复使用；htm 和 htu 只比对前缀，改路径也能过；ath 没有和 access token 绑定；proof 签名算法允许 none；公钥指纹和令牌绑定的 thumbprint 不一致也接受；时钟偏差留了十分钟；同一 jti 并发提交两次，两边都换到令牌；刷新令牌轮换时旧 proof 还能用；DPoP nonce 由服务端下发但没有校验；跨租户的 proof 能换别家令牌；错误响应区分 jti 重放和签名失败；服务重启后 jti 缓存清零。

## 运行方式

```bash
go test ./...
```

## 平台字段

- domain: 安全
- sub: 身份认证
- type: Bugfix
- difficulty: 地狱
