# GSB Bugfix Task

## 题面

Go 边界路由器接入 RPKI 后，仍被上游把流量导到了伪造的起点。入方向只按前缀长度过滤，没有校验 origin AS；AS_PATH 里的私有 AS 被剥掉后仍接受；同一前缀被两个 origin 同时宣告，先到的被后到的覆盖；RPKI 验证失败的路线仍被安装；AS_SET 被当成正常路径；prepend 次数被清空后绕过长度检查；社区属性没有匹配白名单；默认路由被无条件接受；路由抖动没有抑制；并发更新时旧路线覆盖新路线；重启后从空表开始；BGP 会话 down 后路由没有撤。bgp_hijack_guard.go 的 HijackGuard 要在安装前校验 origin AS 与 RPKI、拒绝私有 AS 和 AS_SET、MOAS 冲突按优先级选主、社区白名单、默认路由显式允许、抖动抑制、并发更新串行化、会话 down 撤路由、重启恢复。硬门禁：invalid RPKI 必须拒绝并记 HIJACK_RPKI_INVALID；同一前缀两个 origin 冲突必须拒绝后到并记 HIJACK_MOAS_CONFLICT；默认路由未显式允许必须拒绝并记 HIJACK_DEFAULT_DENIED；会话 down 后相关路由必须撤销。

## 运行方式

```
go test ./...
```

## 平台字段

- domain: 安全
- sub: 网络与通信安全
- type: Bugfix
- difficulty: 地狱
