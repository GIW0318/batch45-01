# GSB Bugfix Task

## 题面

SPIFFE 工作负载证书轮换时，边车代理出现短暂全量 401。新 SVID 还没挂载旧 SVID 就被删；bundle 更新不是原子替换，校验方读到半新半旧；同一 workload 的并发轮换产生两套私钥，后写覆盖；SVID 过期后没有提前续签；bundle 端点返回部分信任根时整体覆盖了好缓存；不同 trust domain 的 SVID 混用；轮换期间旧 SVID 签发的令牌仍被接受但无法撤销；证书链校验没有检查 SPIFFE ID 前缀；sidecar 重启后从磁盘恢复的顺序不对，先起监听后加载证书；轮换失败后没有回滚，工作负载一直用坏证书；bundle 刷新没有限流，上游被打爆；多副本各自轮换，负载均衡后身份不一致。

## 运行方式

```bash
go test ./...
```

## 平台字段

- domain: 安全
- sub: 身份认证
- type: Bugfix
- difficulty: 地狱
