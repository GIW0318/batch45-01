# RADIUS 记账会话状态机说明

## 会话键与状态

会话以 `NASAddr + SessionID` 为唯一键（同一 `SessionID` 在不同 NAS 下互不覆盖）。
每个会话有三种状态：`started` → `interim` → `stopped`。

## 请求处理流水线（Handle）

1. **NAS 识别**：`NASAddr` 不在 `SecretByNAS` 中，报 `ErrUnknownNAS`，丢弃。
2. **认证器校验**：按 NAS 取共享密钥，计算 SHA-256(secret|nas|sessionID|user|status)
   的前 16 字节并常量时间比较；不匹配报 `ErrBadAuthenticator`，`Dropped=true`，不转发。
3. **去重**：`RequestID` 已转发过（含代理链路上其它 NAS 的重复跳）则回
   `ReplyDuplicate`，不再转发，不算错误；被拒绝的请求不进入去重表，可修正后重发。
4. **状态机迁移**（见下表）；非法迁移报 `ErrOutOfOrder`，丢弃。
5. **会话上限**：仅 `Start` 检查，同用户活跃（非 stopped）会话数达到
   `MaxSessionsPerUser` 报 `ErrSessionLimit`；`0` 表示不限。
6. **转发**：`DelayTime += ForwardDelay`，原始 `Timestamp` 保持不变；
   `ForwardedCount` 加一，记录 `LastForwarded`，`RequestID` 写入去重表。

## 状态迁移表

| 当前状态   | Start                | Interim-Update      | Stop            |
|------------|----------------------|---------------------|-----------------|
| （无会话） | 新建 → started       | ErrOutOfOrder       | ErrOutOfOrder   |
| started    | ErrOutOfOrder        | → interim           | → stopped       |
| interim    | ErrOutOfOrder        | → interim（刷新）   | → stopped       |
| stopped    | 重新开户 → started   | ErrOutOfOrder       | ErrOutOfOrder   |

`Interim`/`Stop` 的 `SessionTime` 只增不减（取较大值），`UpdatedAt` 取请求时间戳。

## 超时与回收（ExpireIdle）

- 活跃会话空闲超过 `SessionTimeout`，自动置为 `stopped` 并返回（补 Stop 语义）。
- 已 `stopped` 超过一个 `SessionTimeout` 的会话被回收，其全部去重条目一并删除。
  因此长跑（如一个月）后，会话表与去重表的驻留量与活跃会话数同量级。

## 重试队列与重启

- `HandleLossy` 模拟响应丢失：正常转发后把报文（已改写 DelayTime 的形式）压入
  重试队列；队列达到 `RetryQueueSize` 后新请求当场失败（`ErrRetryQueueFull`，不转发）。
- `Retry` 重新发送并清空队列，返回重试条数；重试不重复计入 `ForwardedCount`。
- `Restore` 在重启后同时恢复会话表与重试队列，两者缺一不可；恢复后的会话可继续
  接收 `Interim`/`Stop`，队列可由 `Retry` 继续冲刷。

## 并发

全部共享状态由同一把互斥锁保护，校验、迁移、记账、登记去重是原子的：
同一会话的并发 `Start` 只有一个被转发，其余收到 `ReplyDuplicate`。
