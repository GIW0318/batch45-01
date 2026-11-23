# GSB Bugfix Task

## 题面

文件能力边界在 Go 辅助程序里没有真正收窄，降权后还能把能力加回来。bounding set 丢弃后 permitted 里还留着；setuid 执行时 ambient 没有清空；no_new_privs 下 file caps 仍然生效；execve 前 effective 没有清零；capability 版本和内核不一致时静默降级；并发 fork 和 exec 之间状态互相覆盖；drop 失败没有返回错误；uid 变化后 permitted 没有按 bounding 重算；inheritable 把能力泄漏给子进程；文件能力没有按路径白名单；容器启动时能力集被上游覆盖；重启后能力状态没有恢复。

## 运行方式

```bash
go test ./...
```

## 平台字段

- domain: 安全
- sub: 系统安全
- type: Bugfix
- difficulty: 地狱
