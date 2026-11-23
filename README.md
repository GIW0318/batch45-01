# GSB Bugfix Task

## 题面

Go eBPF 程序在 TC 入口做重定向后，部分 pod 的流量绕过了网络策略。重定向目标 ifindex 没有校验，指向宿主机网卡；map 的 key 用源 IP 而不是五元组，同 IP 不同端口串策略；程序返回 TC_ACT_OK 而不是重定向失败时的 drop；conntrack 状态没有同步，回包走了另一条路径；map 更新不是原子替换，读线程看到半更新；LRU map 满了静默淘汰活跃条目；策略删除后 map 条目没有清理；eBPF 程序加载失败后回退到无策略；多网卡场景只挂了一个 hook；skb 被修改后校验和没有重算；程序里用了不支持的 helper；热更新时新旧程序同时挂载。

## 运行方式

```bash
go test ./...
```

## 平台字段

- domain: 安全
- sub: 网络与通信安全
- type: Bugfix
- difficulty: 地狱
