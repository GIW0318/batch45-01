# GSB Bugfix Task

## 题面

Go 递归解析器开启 DNSSEC 后，伪造应答仍能通过验证。RRSIG 的 inception/expiration 没有检查；签名者名字没有和 zone 匹配；DNSKEY 的 SEP 标志没有校验；DS 摘要算法允许了废弃算法；NSEC3 的迭代次数没有上限；通配符证明没有校验 closest encloser；CNAME 链中混入未签名记录；签名覆盖的 RRset 顺序被忽略；密钥轮换期间旧 DNSKEY 立即删除；验证失败后回退到不验证；TC 置位后没有重试 TCP；缓存了验证失败的负结果。

## 运行方式

```
go test ./...
```

## 平台字段

- domain: 安全
- sub: 网络与通信安全
- type: Bugfix
- difficulty: 地狱
