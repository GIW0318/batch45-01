# GSB Bugfix Task

## 题面

XFRM IPsec 策略在 Go 控制器里出现策略和 SA 不匹配，部分隧道明文放行。策略添加和 SA 添加不是原子操作，中间窗口没有加密；策略的 src/dst 没有规范化，CIDR 重叠时顺序错；SA 的 SPI 冲突时后加覆盖先加；replay window 只在新 SA 上设置，旧 SA 没更新；策略删除后 SA 没有删，资源泄漏；隧道模式没有校验外层地址；AEAD 算法允许了不合规组合；PFS 组没有按策略校验；SA 过期后没有重新协商；并发添加同一隧道产生两套 SA；XFRM 状态查询读到半更新；控制器重启后策略和 SA 没有对账。

## 运行方式

```
go test ./...
```

## 平台字段

- domain: 安全
- sub: 网络与通信安全
- type: Bugfix
- difficulty: 地狱
