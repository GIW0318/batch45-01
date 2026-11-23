# PityLedger Bugfix 交付说明

## 改动文件

- `gacha_pity_ledger.go`：PityLedger 计数、跨版本/概率期口径、并发串行、设备守卫、快照恢复、种子推导与流水记账实现。
- `BUGFIX_REPORT.md`：本说明。

原测试文件 `tests/test_gacha_pity_ledger_test.go` 未做任何修改。

## 现象逐条对应

1. **跨版本后保底失效**：`CarryOver` 读取同一账号/卡池的既有计数并原样返回，不创建新的零计数桶，版本参数不重置保底。
2. **计数按账号而非卡池隔离**：统一使用 `PoolKey(accountID, poolID)`，格式为 `accountID:poolID`，所有计数、触发、命中和流水均以该键隔离。
3. **单抽/十连计数口径**：`OnDraw` 按传入抽数累加；单抽传入 1 得 +1，十连传入 10 得 +10。
4. **保底触发后未清零**：`Triggered` 在计数 `>= Threshold` 时为真；`OnHit` 返回命中的旧计数并将当前 Pity 置零。
5. **跨版本继承被重置**：见第 1 条，`CarryOver` 不清空、不回退计数。
6. **概率提升期结束计数错位**：`RateUpEnd` 只结束概率期，返回并保留原计数。
7. **同卡池并发计数互相覆盖**：所有计数变更共享同一把互斥锁；`OnDraw` 的检查、流水写入与计数发布在同一临界区完成，`ConcurrentDraw` 的八路并发最终准确增加 8。
8. **保底奖励被普通掉落顶掉**：`ResolveReward(true, ...)` 固定返回 `pity_reward`，普通掉落只可能在未触发保底时返回。
9. **伪随机种子按时间生成无法复盘**：`Seed` 对会话 ID 做 SHA-256 推导，同一会话结果稳定可复现，零种子映射为非零常量。
10. **保底计数没有持久化**：`Snapshot` 导出全部账号/卡池计数副本；`Restore` 以快照替换计数状态，恢复后逐项一致。
11. **多设备同时抽卡重复计数**：`DeviceGuard` 用锁记录首台设备，后续不同设备返回拒绝；计数写入本身也被串行化，不会因并发覆盖。
12. **阈值热更新后计数错位**：`Reload` 只更新全局阈值，保留账号/卡池原计数；`Threshold` 返回新值。
13. **邮件补发保底奖励没有标记**：保底分支使用稳定的 `pity_reward` 标记，便于补发链路识别；普通掉落不会覆盖该标记。

## 流水与事务约束

- 每次 `OnDraw` 生成一条 `JournalEntry`，包含账号、卡池、`PoolKey`、序号、抽数、计数前值 `Before` 和计数后值 `After`。
- `OnHit` 同样记录前后值，运营可按账号从流水还原整段计数变化。
- `commitLocked` 先校验 `Before` 与当前计数一致，再写流水，最后发布新计数；任何校验/记账失败都不会改动计数。
- 单账号流水上限为 `JournalPerAccountLimit = 256` 条，全账本上限为 `JournalTotalLimit = 4096` 条；超限按最旧顺序淘汰，内存占用不随在线时长无限增长。
- `Restore` 会清除旧的内存流水，避免重启前的旧记录与恢复后的计数交叉；恢复后的第一条新流水以快照计数作为 `Before`，计数链从恢复点继续。
- `Journal(accountID)` 与 `JournalSize()` 可用于运营侧逐条核对。

## 验证结果

```text
$ go test ./... -count=1 -v
?   	gsb/batch45-game-a-07	[no test files]
=== RUN   TestPoolKeyIsPerAccountAndPool
--- PASS: TestPoolKeyIsPerAccountAndPool (0.00s)
=== RUN   TestPityCountsPerPoolNotPerAccount
--- PASS: TestPityCountsPerPoolNotPerAccount (0.00s)
=== RUN   TestSingleDrawIncrementsByOne
--- PASS: TestSingleDrawIncrementsByOne (0.00s)
=== RUN   TestTenPullIncrementsByTen
--- PASS: TestTenPullIncrementsByTen (0.00s)
=== RUN   TestPityTriggersAtThreshold
--- PASS: TestPityTriggersAtThreshold (0.00s)
=== RUN   TestHitResetsCounter
--- PASS: TestHitResetsCounter (0.00s)
=== RUN   TestCarryOverKeepsCounterAcrossVersion
--- PASS: TestCarryOverKeepsCounterAcrossVersion (0.00s)
=== RUN   TestRateUpEndReturnsBankedCounter
--- PASS: TestRateUpEndReturnsBankedCounter (0.00s)
=== RUN   TestConcurrentDrawSerialisesCounter
--- PASS: TestConcurrentDrawSerialisesCounter (0.00s)
=== RUN   TestSeedIsReproducible
--- PASS: TestSeedIsReproducible (0.00s)
=== RUN   TestSnapshotPersistsCounters
--- PASS: TestSnapshotPersistsCounters (0.00s)
=== RUN   TestRestoreReloadsCounters
--- PASS: TestRestoreReloadsCounters (0.00s)
=== RUN   TestDeviceGuardRejectsSecondDevice
--- PASS: TestDeviceGuardRejectsSecondDevice (0.00s)
=== RUN   TestReloadKeepsCounterConsistent
--- PASS: TestReloadKeepsCounterConsistent (0.00s)
=== RUN   TestPityRewardNeverReplacedByNormalDrop
--- PASS: TestPityRewardNeverReplacedByNormalDrop (0.00s)
PASS
ok  	gsb/batch45-game-a-07/tests	0.003s
```

```text
$ go test -race ./... -count=1
?   	gsb/batch45-game-a-07	[no test files]
ok  	gsb/batch45-game-a-07/tests	1.012s

$ go vet ./...
(no output)
```

额外临时自检覆盖了：28 条并发流水序号与前值/后值连续、恢复后从快照计数续写、单账号 256 条/全账本 4096 条上限、两台设备并发只放行一台。临时自检文件已移除，最终测试集保持原 15 条。
