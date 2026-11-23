# IoUringGuard: user_data 布局与销毁时序

## user_data 布局

每个 `IoUringGuard` 内部维护一张租户注册表。某个租户第一次出现时分配一个
非零的 32 位 `TenantID`（从 1 开始递增），字符串只保存在该 ring 的映射表中。
`user_data` 固定为：

```
 63                         32 31                          0
+-----------------------------+-----------------------------+
|        TenantID (32)        |       TenantSeq (32)        |
+-----------------------------+-----------------------------+
```

- `TenantID` 在单个 ring 内唯一，`0` 保留为“无 owner”，用于拒绝伪造值。
- `TenantSeq` 是每个租户独立递增的提交序号，首个提交为 1。
- 不同租户即使 `TenantSeq` 相同，高 32 位也不同，因此不会串号。
- `DecodeUserData` 反查 `TenantID -> tenant string`，未知高 32 位返回
  `ErrUnknownRequest`。
- 序号回绕时跳过 0，并跳过仍存在于 pending 表中的值，避免覆盖在途请求。
- `Submit`、`SubmitChain`、`SubmitTimeout` 都通过同一分配函数生成
  `user_data`；CQE 的 `TenantID` 与 `Seq` 直接取自请求记录，而不是重新解析
  调用方输入。

## 销毁时序

`Destroy` 与 `Submit`、`Cancel`、`Reap` 共用同一把互斥锁，顺序如下：

1. 加锁；若已关闭则幂等返回。
2. 标记 `draining`，阻止新的提交进入。
3. 由于提交在锁内同步执行，拿到锁时此前已接受的请求都已到达终态；逐个消费
   completion 队列，递减租户在途计数与缓冲区引用计数。
4. 防御性清空 pending 表，确保 `InFlight`、`TenantInFlight`、
   `TenantUsage` 均归零且不会为负。
5. 清空 completion 队列，设置 `closed = true`，释放锁。

销毁之后 `Submit`/`RegisterFiles`/`RegisterBuffers`/`AfterFork` 返回
`ErrClosed`，`Cancel` 对已排空请求返回 `ErrUnknownRequest`，`Reap` 返回空
结果，再次 `Destroy` 仍返回 nil。并发 `Submit` 要么在加锁前完成并被本次销毁
排空，要么在锁释放后看到 `ErrClosed`，不会留下正的在途计数。

## 关联规则

- `Reap(n<=0)` 排空当前全部 completion；`n>0` 最多返回 n 个。
- 请求在被 `Reap` 消费前仍占用队列槽位和租户配额，因此满队列提交立即返回
  `ErrQueueFull`，不会阻塞线程。
- `Cancel` 只按完整 `user_data` 精确查 pending 表；同一请求第二次取消返回
  错误，不重复递减计数。
- 链式提交只把失败点之前（含失败点）的 SQE 放入队列，失败点之后的
  `user_data` 仍返回给调用方做关联，但不会执行、不会产生 CQE。
- `AfterFork` 返回独立 ring 视图：pending/计数为空，父进程固定 fd 集合被标记
  为 stale；子进程提交这些 fd 返回 `ErrStaleFD`，销毁子视图不影响父 ring。
- 单 ring 注册缓冲总字节数由 `RingConfig.MaxBufferBytes` 限制，未设置时使用
  `DefaultMaxBufferBytes`；超限后带 `BufID` 的提交立即返回包含租户名的
  `ErrQuotaExceeded`。
