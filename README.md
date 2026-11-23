# GSB Bugfix Task

## 题面

Go 写的 PAM 模块在容器里导致 sshd 全部认证失败。模块返回值没有按 PAM 约定映射，成功被当成失败；对话函数没有处理 NULL 响应；重试次数没有上限；密码在内存里没有清零；并发认证时共享状态互相覆盖；模块顺序错误导致先放行后拒绝；account 阶段没有检查过期；session 打开失败后没有关闭；环境变量被注入 LD_LIBRARY_PATH；日志记录了明文密码；超时后 goroutine 还在跑；配置解析失败后回退到允许。

## 运行方式

```bash
go test ./...
```

## 平台字段

- domain: 安全
- sub: 系统安全
- type: Bugfix
- difficulty: 地狱
