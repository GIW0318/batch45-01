# 测试记录

## 环境

- 工作目录：`/home/cat/gsb2/uid5208`
- Go 版本：`go1.23`
- 模块：`gsb/batch45-game-a-21`

## 既有 15 条用例

命令：

```bash
go test -count=1 -v ./...
```

结果：15 条既有用例全部 PASS，未修改既有测试文件。

```
--- PASS: TestMergedRolesShareSingleClaim
--- PASS: TestDifferentAccountsClaimIndependently
--- PASS: TestMakeUpDeductsOneTicket
--- PASS: TestMakeUpRejectedWithoutTicket
--- PASS: TestDayBoundaryUsesServerClock
--- PASS: TestRewardMailSentOncePerDay
--- PASS: TestReconnectResubmitIsIdempotent
--- PASS: TestStreakResetsAfterGap
--- PASS: TestStreakIncrementsOnConsecutiveDays
--- PASS: TestCalendarCacheRefreshedAfterClaim
--- PASS: TestCrossServerSyncBlocksSecondClaim
--- PASS: TestMakeUpLimitEnforced
--- PASS: TestEventCounterIndependentOfStreak
--- PASS: TestSnapshotRestoreKeepsState
--- PASS: TestNextServerDayAllowsNewClaim
PASS
```

## 并发与重启补充验证

新增补充测试文件：`tests/test_login_streak_guard_hardening_test.go`。

覆盖内容：

- 32 个 goroutine 同账号同日并发 `Claim`：仅 1 次成功入账、1 封邮件、所有返回 `MailID` 相同。
- 快照恢复后绑定仍可用、同日 `Claim` 判重、补签次数上限仍生效、日历包含当日。
- 补签填补缺口后连续天数与日历正确。
- 模拟 720 天连续签到，连续天数保持 720，快照保持在 32 KiB 以内且恢复后计数不变；过期赛季日历为空、当前赛季可查。
- 未绑定角色和跨服未知账号返回 `ErrUnknownAccount`。

命令：

```bash
go test -count=1 -v ./...
go test -race -count=1 ./...
go vet ./...
```

结果：

```
ok   gsb/batch45-game-a-21/tests
ok   gsb/batch45-game-a-21/tests   (race)
go vet: no issues
```
