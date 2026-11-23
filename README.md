# GSB Bugfix Task

## 题面

WebAuthn 服务端升级后发现同一认证器能注册到两个账号。credential ID 唯一索引只在本租户内生效；resident key 的 user handle 没有和账号绑定；attestation 链没有评估，自签也接受；UV 标志没置位也放行；备份可用标志被忽略，跨设备同步凭证当成单设备；挑战在 RP ID 哈希不匹配后仍可重试；同一凭证并发注册产生两条记录；签名计数器相等值没有拒绝；origin 只比对域名后缀；frozen 凭证还能认证；重启后公钥表没落盘；发现请求返回了别人的条目。

## 运行方式

```bash
go test ./...
```

## 平台字段

- domain: 安全
- sub: 身份认证
- type: Bugfix
- difficulty: 地狱
