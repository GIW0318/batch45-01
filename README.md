# GSB Bugfix Task

## 题面

netlink 路由监控器在 Go 里漏掉了部分路由变更，策略路由和主表不同步。订阅只挂了 IPv4 组，IPv6 变更丢失；消息分片没有重组，长路由属性被截断；dump 和事件之间没有对齐，dump 期间的变化丢失；路由删除事件没有带表 ID，删错表；同一前缀的多个 nexthop 被合并；策略路由的优先级没有排序；链路 down 后路由没有标记失效；并发处理事件时 map 互相覆盖；netlink 连接断开后没有重连；消息序号回绕没有处理；内核返回 ENODEV 时静默跳过；重启后从空表开始。

## 运行方式

```
go test ./...
```

## 平台字段

- domain: 安全
- sub: 网络与通信安全
- type: Bugfix
- difficulty: 地狱
