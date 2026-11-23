# GSB Bugfix Task

## 题面

Landlock 沙箱在 Go 服务里偶发失效，进程能读到沙箱外的文件。规则集在 fork 后没有继承，子进程回到无限制；路径规则按字符串前缀匹配，/data 放行了 /database；文件描述符在规则应用前已经打开，绕过了检查；规则里同时允许读和写时只保留了最后一个；ABI 版本低于要求时静默降级；规则集句柄没有设置 no_new_privs；多 goroutine 并发应用规则，部分线程没生效；符号链接没有解析 realpath；挂载点变化后规则没有重算；规则应用失败后继续运行；端口规则只按协议不按端口；规则集释放后仍被引用。

## 运行方式

```bash
go test ./...
```

## 平台字段

- domain: 安全
- sub: 系统安全
- type: Bugfix
- difficulty: 地狱
