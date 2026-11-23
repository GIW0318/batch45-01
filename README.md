# GSB Bugfix Task

## 题面

Go 实现的 IKEv2 响应端在弱网下出现 SA 双发和重放。IKE_SA_INIT 的 cookie 没有绑定源地址；同一 SPIi 并发发起产生两套 SA；重传的 IKE_AUTH 被当成新请求；NONCE 没有一次性使用；DH 组不匹配时降级；SA 生命周期没有按双方较小值取；CHILD_SA 删除后父 SA 没有清理；EAP 身份和证书身份没有绑定；NAT 探测载荷没有校验；流量选择器允许了 0.0.0.0/0；重钥时旧 SA 立即删除；重启后 SA 没有恢复。

## 运行方式

```bash
go test ./...
```

## 修复说明（ikev2_sa_guard.go）

方法签名与公开类型布局保持不变，仅实现各校验逻辑并在 `IkeSaGuard` 内增加非导出的互斥锁与每对端记账字段。

- `VerifyCookie`：cookie 绑定源地址，`PeerAddr` 与 `srcAddr` 不一致直接拒绝；按 `ttl` 判断过期（边界 `age == ttl` 放行），时钟回拨（负 age）同样拒绝。
- `CreateSA`：互斥锁串行化，同一 SPIi 并发创建只有一次成功，已存在的 SPIi 一律拒绝；每个对端半开（未 `Established`）SA 上限为 `maxHalfOpenPerPeer = 8`，触顶时拒绝新建并淘汰该对端最久未活动的半开条目；记账随调用方 SA 表即时剪枝，内存不随握手次数增长。
- `ClassifyAuth`：已处理过的 msgID 判定为重传并拒绝，新 msgID 记入 `seen`。
- `CheckNonce`：一次性 NONCE，复用拒绝，新 NONCE 记入 `used`。
- `NegotiateDH`：只在双方交集内选编号最大（最强）的组，无交集直接报错，不做降级。
- `NegotiateLifetime`：取双方提议的较小值，0 值报错。
- `ParentStateAfterChildDelete`：无子 SA 时父 SA 进入 `ParentIdle`（应拆除），否则保持 `ParentActive`。
- `CheckIdentityBinding`：EAP 身份与证书身份必须完全一致，任一为空也拒绝。
- `CheckNATPayload`：使用常量时间比较 NAT 探测载荷与期望值，防止时序探测。
- `CheckTrafficSelectors`：默认拒绝 `0.0.0.0/0`、`::/0` 等全通配选择器，仅 `allowAny=true` 时放行。
- `Rekey`：新 SA 未 `Established` 前拒绝切换，旧 SA 继续承载流量；新 SA 就绪后才放行。
- `RestoreSAs`：按 SPI 重建与持久化快照完全一致的表；快照缺失 SPI 或存在重复 SPI 时报错，不静默丢状态；空快照返回空表。

## 测试记录

- `go test ./...`：15 个原始用例全部 PASS（测试文件未改动，无跳过、无输入特判）。
- 额外临时自测（验证后已删除，未保留在交付物中）配合 `go test -race -count=5 ./...` 通过：64 goroutine 并发同 SPIi 创建仅 1 次成功；半开触顶拒绝并淘汰最旧条目、已建立 SA 不占半开口额、记账随表剪枝；重启恢复结果与持久化快照逐项一致（含重复/空 SPI 报错路径）。

## 平台字段

- domain: 安全
- sub: 网络与通信安全
- type: Bugfix
- difficulty: 地狱
