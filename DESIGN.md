# AuditPipeline 设计说明（一页）

## 事务提交点

- `Begin` 在内存中登记一个事务状态；`AppendTx` 只把事件**暂存**到该事务，分配 `TxID`，不占序号、不落盘、不进队列。
- **提交点 = `Commit` 返回成功**：此时为事务内全部事件一次性分配连续 Seq、脱敏、转 UTC，放入已提交待写区，并做磁盘配额（`MaxBytes`）预检，超额整体拒绝并返回 `ErrDiskFull`，事务保持打开可重试或回滚。事务事件不受租户队列容量限制——业务已提交，审计不得因队列满丢失。
- `Rollback` 直接丢弃暂存事件，磁盘上不留痕迹。
- 事务结束后（Commit/Rollback 已执行）再调用 `AppendTx`/`Commit`/`Rollback` 一律返回 `ErrTxClosed`。
- 落盘时机：`Commit` 只保证事件进入已提交待写区；`Flush`/`ReadAll`/`Query`/`Rotate` 触发统一写盘。Append 与 Commit 交错时，所有待写事件在 Flush 内**按 Seq 全局排序后一次性缓冲写入**（单互斥锁保护），文件里不会出现半行或乱序。

## 轮转（Rotate）

- `Rotate` 先 Flush 排空待写事件，关闭当前文件，`rename(Path, Path+".1")`，再以原权限（默认 `0600`）重新打开 `Path`。
- 之后的写入落在**新 inode**，不会写进已轮转的 `.1` 文件。
- **Seq 计数器不重置**，`seen` 去重集合保留；当前段的内存索引（`stored`/`written`）清零，`ReadAll`/`Query` 只反映当前段，历史在 `Path+".1"`。
- 重启续号由 `Restore(events)` 显式完成：取历史事件最大 Seq 作为起点，后续事件序号连续。

## 租户队列

- 队列**按 Actor 拆分**，每个租户独立计数，容量为 `QueueSize`。
- 某租户刷爆时，只有它自己的 `Append` 返回 `ErrQueueFull`；其他租户的入队、Flush 完全不受影响，互不拖慢。
- 任何情况下都**不静默丢事件**：`Dropped` 恒为 0，队列满、磁盘满都以显式错误返回给调用方。
- Seq 在入队时于全局锁内分配，保证同一 goroutine 内顺序保序、跨 goroutine 全局有序；Flush 时归并各租户队列并按 Seq 排序写盘。

## 其他安全点

- 脱敏：`token`/`password`/`secret`/`authorization`（大小写不敏感）在入队/提交时改写为 `***`，磁盘与 `Query` 结果中均不出现明文；`Redact` 处理 `k=v&k=v` 字符串。
- 时间戳统一存 UTC；文件权限默认 `0600`（打开后强制 `Chmod`）；重复事件 ID 幂等抑制；syslog 断线时事件进入本地 backlog，`Flush` 在恢复连通后排空。
