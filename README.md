# GSB Bugfix Task

## 题面

gRPC 网关升级后发现租户能通过 metadata 覆盖上游身份。小写和大写 metadata key 被当成两个字段，鉴权只看了一个；二进制 metadata 没有长度限制，超长值截断后仍转发；重复 key 保留第一个而不是拒绝；metadata 里的 authorization 可以覆盖网关注入的凭据；grpc-timeout 被客户端改大，后端一直占用；user-agent 被伪造后绕过客户端白名单；压缩标志和 payload 不一致也转发；trailer 里的错误码被中间件改写；metadata 大小按压缩后计算；跨租户的 trace-id 被复用；反射服务暴露了内部方法；健康检查接口不鉴权。

## 运行方式

```
go test ./...
```

## 平台字段

- domain: 安全
- sub: 网络与通信安全
- type: Bugfix
- difficulty: 地狱
