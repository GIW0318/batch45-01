# GSB Bugfix Task

## 题面
Go 边缘路由器接入 RPKI 后，伪造起源的前缀仍被装进转发表。宣告只按前缀长度过滤，源 AS 与 ROA 不匹配也接受；同一前缀两个源同时出现时后到覆盖先到；校验失败的路线继续下发；AS 集合被当成单一源；ROA 缓存过期后仍用旧结果；验证器重启后状态清空；并发更新时旧宣告覆盖新宣告；源 AS 为私有号段仍放行；前缀长度比 ROA 更具体时没有拒绝；MOAS 冲突没有按优先级选主；会话断开后相关前缀没有撤销；批量更新时部分失败没有回滚。bgp_hijack_guard.go 的 HijackGuard 要在安装前完成源 AS 与 ROA 校验、拒绝私有 AS 与 AS 集合、MOAS 冲突按优先级选主、ROA 缓存过期重验、并发更新串行化、重启恢复、批量失败回滚。硬门禁：ROA 不匹配必须拒绝并记 HIJACK_RPKI_INVALID；MOAS 冲突必须拒绝后到并记 HIJACK_MOAS_CONFLICT；私有源 AS 必须拒绝并记 HIJACK_PRIVATE_AS；批量更新部分失败必须整体回滚。 同一条路由消息重复抵达仍按一次计，处理位点按内核序号存盘，旧消息回放不许把状态倒回去。

## 运行方式

```
go test ./...
```

## 平台字段

- domain: 安全
- sub: 网络与通信安全
- type: Bugfix
- difficulty: 地狱
