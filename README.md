# GSB Bugfix Task

## 题面

Go 容器逃逸检测器在宿主机上误报和漏报同时存在。检测规则只匹配已知二进制路径，改名就绕过；proc 文件读取在容器内被挂载覆盖，读到假信息；capability 位图解析只看了低 32 位；namespace 比较只比 inode，不比 user namespace；mountinfo 解析没处理转义字符；并发扫描时容器已退出，结果错位；检测结果缓存没有失效；特权容器白名单过宽；seccomp 状态没有解析；cgroup 路径变化后规则失效；宿主机和容器同名进程混淆；检测器重启后基线丢失。

## 运行方式

```
go test ./...
```

## 平台字段

- domain: 安全
- sub: 系统安全
- type: Bugfix
- difficulty: 地狱
