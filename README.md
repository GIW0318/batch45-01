# GSB Bugfix Task

## 题面

EVPN VXLAN 控制器在 Go 里同步 MAC/IP 路由时出现跨租户泄漏。RD 和 RT 没有按租户隔离；MAC 路由的 VNI 与 VRF 不匹配也安装；同一 MAC 在多个 VTEP 学习时后到覆盖先到；ARP 代理没有校验 IP-MAC 绑定；Type-5 路由的前缀没有做最长匹配；路由撤销时只删了本地表；多归属 ES 的 DF 选举没有做；别名地址没有去重；控制器重启后从空表开始；BGP 邻居 down 后路由没有撤；并发更新时旧路由复活；RT 导入策略允许了通配。

## 运行方式

```bash
go test ./...
```

## 平台字段

- domain: 安全
- sub: 网络与通信安全
- type: Bugfix
- difficulty: 地狱
