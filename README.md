# GSB Bugfix Task

## 题面

cgroup v2 委派给租户后，一个租户把整台机器的 CPU 打满。委派目录的 subtree_control 没有清空，租户能打开不该有的控制器；cgroup.procs 写入没有检查目标进程是否属于本租户；limits 只写 cpu.max，没有写 cpuset 和 io.max；子 cgroup 可以无限创建，inode 被耗尽；memory.max 设置后没有触发回收，OOM 杀错进程；delegation 包含 controllers 文件，租户能自己加控制器；冻结和杀死操作没有原子性；迁移进程时父 cgroup 的限制没有继承；统计文件读到半更新；cgroup 删除后定时器还引用；嵌套层级超过限制没有报错；重启后委派状态丢失。

## 运行方式

```bash
go test ./...
```

## 平台字段

- domain: 安全
- sub: 系统安全
- type: Bugfix
- difficulty: 地狱
