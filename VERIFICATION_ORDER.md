# TEE Attestation Guard：校验顺序与超时说明

## 1. Verify 校验顺序

整个 `Verify` 在同一把互斥锁内完成，保证并发调用串行判定；所有检查全部通过后才写入 nonce，任何失败都不消费 nonce（失败可安全重试，不会被误判为重放）。

| 步骤 | 检查项 | 失败错误码 | 依据字段 |
| --- | --- | --- | --- |
| 1 | TEE 类型必须在白名单 | `ErrTEEType` | `Report.Type` vs `Policy.AllowedTypes` |
| 2 | 证书链必须包含根证书（顺序无关，集合判定） | `ErrChainInvalid` | `Report.Chain` vs `Policy.RootCert` |
| 3 | 报告 nonce 必须等于请求 nonce，且非空 | `ErrNonceMismatch` | `Report.Nonce` vs `expectedNonce` |
| 4 | nonce 未被消费过（单次有效） | `ErrNonceReplay` | 内部 `usedNonce` 集合 |
| 5 | measurement 在白名单且未被吊销 | `ErrMeasurement` | `Policy.AllowedMeasurements` / `Revoke` / `Policy.Revoked` |
| 6 | TCB 版本不低于最低版本（点分数字逐段比较，缺位补 0，边界等值放行） | `ErrTCBTooOld` | `Report.TCBVersion` vs `Policy.MinTCBVersion` |
| 7 | 报告年龄不超过 `MaxAge`（固定时钟 `now`，边界等值放行） | `ErrReportExpired` | `Report.IssuedAt` |
| 8 | 调用方传入 TLS 公钥时，报告公钥必须字节一致；传 `nil` 跳过绑定检查 | `ErrPublicKeyBind` | `Report.PublicKey` vs `tlsPubKey` |
| 9 | 全部通过：写入 nonce，返回 `Result{Verified: true, Measurement, Type, CacheKey}` | — | — |

顺序设计要点：

- 类型与证书链（无需 nonce 状态）先判，nonce 绑定与重放判在产生任何副作用之前。
- nonce 只在第 9 步落库，因此 `ErrNonceMismatch`、`ErrPublicKeyBind` 等失败路径不会污染重放缓存（测试明确校验该行为）。
- 软件回退开关（`SetSoftwareFallback`）只记录状态，不参与任何放行决策；硬件校验失败永远返回错误且 `Result.Verified == false`。
- `Restart` 为模拟重启，刻意不清空 nonce 集合，重放保护跨重启生效。

## 2. 其他接口行为

- `VerifyCertChain`：空链/空根/链中找不到根证书一律 `ErrChainInvalid`；链上证书顺序任意，只要求根证书可达。
- `BindPublicKey`：TLS 公钥为空或与报告公钥不一致返回 `ErrPublicKeyBind`。
- `CacheKey`：`tee:<type>:<measurement>`，measurement 必在 key 中、nonce 不入 key，同报告稳定、不同 measurement 分区；`Result.CacheKey` 与之一致。
- `Revoke`：空 measurement 返回包装后的 `ErrMeasurement`；吊销与白名单检查共用同一把锁。

## 3. 200ms 超时与并发保证

- 常量 `VerifyTimeout = 200 * time.Millisecond`。每次 `Verify` 用 `context.WithTimeout` 包一个 200ms 截止时间，实际校验在独立 goroutine 中运行，结果写入容量为 1 的缓冲 channel。
- 主调用方 `select` 等待：先到校验结果则返回结果；截止时间先到则立即返回 `ErrVerifyTimeout`（可 `errors.Is` 识别的哨兵错误），调用方 goroutine 不会被内部锁竞争挂住。
- 内部 goroutine 在抢锁后、以及提交 nonce 前各检查一次 `ctx.Err()`：超时后即使拿到锁也直接返回 `ErrVerifyTimeout`，不写 nonce、不返回 `Verified`；缓冲 channel 保证其写入不阻塞，无 goroutine 泄漏。
- 互斥锁 + “全部通过才消费 nonce” 的提交方式，保证同一 nonce 并发 N 次验证时恰好 1 次成功、其余 N-1 次为 `ErrNonceReplay`（`go test -race` 验证）。
- 已用临时测试人工制造 500ms 锁阻塞验证过该路径：调用约在 200ms 处返回 `ErrVerifyTimeout`，nonce 未被消费，阻塞解除后同 nonce 可正常成功一次；临时测试随后已删除，仓库测试文件未做任何改动。
