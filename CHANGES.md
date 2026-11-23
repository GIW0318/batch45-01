# 改动说明

唯一改动点：`mail_batch_dispatcher.go`（新增 `verify/main.go` 为自验程序，未触碰任何测试文件）。

## 修复点

- 邮件 ID：`MailID` 是批次与规范化收件人的纯函数（`batchID:recipient`），重试不会重新生成。
- 收件人规范化：`Normalize` 先去首尾空格再转小写；`Plan` 按规范化结果去重，`Player-1` 与 `player-1` 视为同一人；`Plan` 幂等，重复规划不重置进度。
- 成功回写：`RecordSuccess` 只把该收件人记入已投递集合，重复回写是 no-op；`Pending` = 已规划 - 已投递。
- 并发约束：`MaxConcurrent` 恒为 1；`TryAcquireSend`/`ReleaseSend` 提供批次级发送槽，同批次串行、跨批次并行，单批卡住不阻塞其他批次。
- 附件：`RetryAttachments` 无论重试几次恒返回 1。
- 离线保留：`KeepsOffline` 恒为 true。
- 过期时间：`ExpiryFrom` = 实际投递时刻 + 30 天（2592000 秒）。
- 分片：`PlanShards` 按收件人 FNV-1a 哈希固定归属；扩容时只有哈希取模结果变化的收件人迁移，已投递记录不参与重算；`CompleteShard`/`ShardProgress` 记录每片进度，分片独立续跑，单批失败不回滚其他分片。
- 队列背压：`Enqueue` 在队列达到容量时当场拒收并返回 `ErrQueueFull`，不覆盖旧邮件；`Dequeue` 释放槽位。
- 路由：`Route` 空服返回 `local`，其余按服路由。
- 死信：`MarkFailed`/`MarkFailedMail` 记录永久失败邮件，`DeadLetters` 按批次计数，`ExportDeadLetters` 按批次导出便于补发复核。
- 状态：`StatusAfterSend` 只暴露 `pending` 与 `sent` 两态。
- 重试上限：`RetryAllowed` 上限 3 次（`maxRetryAttempts`）。

## 重启续跑

`Snapshot`/`Restore` 以 JSON 可序列化的 `DispatcherState` 持久化已规划邮件、已投递记录、分片进度与死信；重启后凭快照续跑，已成功收件人不会收到第二封。

## 验证

- `go test ./...`：15 条用例全部通过（含 `-race`），日志见 `artifacts/test_log.txt`。
- `go run ./verify`：并发批次、扩容迁移、重启续跑三种情形全部通过。
