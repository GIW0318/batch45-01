# GSB Bugfix Task

## 题面

内核 keyring 配额在 Go 服务里被绕过，单租户把 key 数量打满。配额按进程而不是按租户统计；key 被链接到会话 keyring 后不计入配额；负 key 和 revoked key 也占配额；配额释放延迟，删除后短时间不能新建；同一 key 被多个 keyring 引用时重复计数；root 租户没有单独配额；key 描述长度没有限制，超长描述占内存；并发创建 key 时配额检查不是原子的；keyring 嵌套深度没有限制；过期 key 没有自动回收；配额统计读到半更新；服务重启后配额计数丢失。

## 运行方式

```bash
go test ./...
```

## 平台字段

- domain: 安全
- sub: 系统安全
- type: Bugfix
- difficulty: 地狱
