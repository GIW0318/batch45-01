# 权威移动（MoveGuard）修复说明

## 现象与根因
上下坡、载具场景下角色被来回拉扯、网络抖动即瞬移，根因是：输入无序号、同序号重复执行、位移按帧累计、校正无阈值直接贴回、缺少窗口裁剪与可恢复的重演机制。

## 修复要点（authoritative_move_guard.go，签名全部保持不变）
1. **输入带序号**：`EncodeInput` 原样封装 `Seq/DX/DY`；`Apply` 在互斥锁内维护去重集合，同序号只生效一次，返回值是“迄今真正执行过”的累计条数，多路网络协程并发调用安全（`-race` 验证通过）。
2. **帧率无关位移**：`Step` 与 `Integrate` 一律按 `speed * 毫秒 / 1000` 折算，25×10ms 与 5×50ms 结果一致。
3. **阈值校正而非瞬移**：`WithinTolerance` 判定阈值内抖动，`ShouldCorrect` 仅在三维欧氏距离超阈值时触发；`Blend` 对 X/Y/Z 三分量按 alpha 线性插值；`ClampDrift` 在超限漂移时整份采信服务器位置，未超限保留预测。
4. **延迟补偿**：`LagCompensate` 回溯到输入产生时刻，并夹在 `[nowMs-maxMs, nowMs]` 区间，绝不早于 `nowMs-maxMs`。
5. **载具与垂直**：`OnVehicle` 为载具位置与本地偏移逐分量相加；`Vertical` 仅改 Z（`base + vy*dt/1000`），水平分量不动。
6. **回滚与 tick**：有回滚帧时动画固定回 `idle_restart`，无回滚返回空串；`TickDelta` 如实返回 `serverTick-clientTick`；`Blocked` 在客户端发移动前做墙体判定。
7. **200ms 重演窗口**：`LogInput` 记录带时间戳的输入日志，`trimLocked` 按 200ms 窗口裁剪；`Resimulate` 只统计 `lastAcked` 之后的输入；`ResimulateSince` 仅回放窗口内未确认输入，窗口外直接采信 `Confirm` 记录的服务器位置，避免卡顿时长尾重放。
8. **同帧去重**：同一帧（相同 `TimeMs`）内相同的水平输入只保留一条，只产生一次位移。
9. **重启恢复**：`Snapshot/Restore` 持久化已确认快照（序号、位置、时间）、去重集合与输入日志；纯函数 `ReplayPath` 保证相同快照+日志重演出的状态序列与中断前逐帧一致。
10. **内存有界**：输入日志按窗口裁剪；`Confirm` 后已确认序号离开去重集合，并用已确认水位 `ackedSeq` 让迟到重复包保持幂等，内存不随在线时长增长。

## 验证记录
- `go test ./...`：15/15 用例通过（未改测试、未跳过、无硬编码期望值），`go vet` 干净，`go test -race` 无数据竞争。
- 临时程序（验证后已删除）覆盖：200 协程并发重复 `Apply`/`LogInput` 严格只生效一次、200ms 窗口裁剪与窗口外采信服务器位置、模拟重启后 `reflect.DeepEqual` 比对重演状态序列完全一致、同帧重复水平输入只计一次、百万帧模拟下历史长度始终有界。
