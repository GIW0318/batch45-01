# IntegrityWatcher 事件关联与基线落盘说明

## 事件关联

- **哈希取自事件本身**：每个 `Event` 携带捕获时刻的 `Content`，哈希一律
  `sha256(ev.Content)`，绝不在事件到达后二次读文件，因此并发写不会读到
  半更新内容；全部状态由一把 `sync.Mutex` 保护，`Handle` 并发安全。
- **写事件按事件时刻生效**：`applyWriteLocked` 只在 `ev.At >= 已记录
  UpdatedAt` 时覆盖当前哈希，迟到乱序事件不会回滚新状态；但每次事件都会
  追加进 `history[path]`，`HashAt(path, t)` 返回不晚于 `t` 的最近一次
  历史哈希。所有时间戳统一 `UTC()` 后再存储与比较，跨时区排序不会错。
- **rename → delete 关联**：`OpRename` 把 `files[OldPath]` 整体迁移到新
  路径（保留容器引用与历史），并在 `renamed` 集合中标记新路径；之后
  `OpRemove` 命中标记即记 `Reason="renamed-then-deleted"`，否则记
  `"unknown"`，文件消失原因可追溯。旧路径条目随 rename 删除，不会残留。
- **多容器引用**：`FileState.Containers` 去重追加，`ReleaseContainer(id)`
  只摘除该容器的一份引用，引用清零才删除状态；容器间互不误伤。
- **队列溢出补偿**：`QueueSize>0` 时事件先进有界队列，超出即丢弃并累计
  `Dropped`、置 `Overflowed`；`Compensate(paths)` 把指定路径状态标记为
  `resynced`，随后清空队列并把 `Overflowed/Dropped` 归位，等待下一轮
  溢出周期。
- **目录与符号链接**：`IsDir` 的 create/rename 会递归补挂 watch（受
  `MaxWatches` 约束，超限返回 `ErrWatchLimit`，重复挂载幂等）。符号链接
  目标按路径组件判断是否落在任一 root 内，越界且未开 `FollowSymlink`
  时返回 `ErrSymlinkEscape` 且不记录。排除规则同样按组件比较，
  `/data/tmp` 不会误伤 `/data/tmp2`。

## 基线落盘

- **整份原子替换**：`RebuildBaseline` 先把快照内容逐条算出哈希、整体替换
  内存基线，再持久化：在同目录创建临时文件（`.integrity-baseline-*`），
  完整写入 JSON 后 `fsync`、关闭，最后 `rename` 覆盖正式文件，并
  best-effort `fsync` 目录项。`rename` 是原子操作，断电时读者只能看到
  完整的旧版或完整的新版，绝不出现半截文件。
- **启动加载与损坏检测**：`NewIntegrityWatcher` 构造时读取基线文件；文件
  不存在视为空基线，存在但 JSON 解析失败（如被截断）则直接报错返回，
  不会带伤运行。持久化路径可用 `Options.BaselinePath` 指定，缺省为
  `os.TempDir()/gsb-integrity-baseline.json`。
- **基线不被静默改写**：运行期 `Handle` 只更新当前文件状态，基线仅由
  `RebuildBaseline` 变更，因此 `Baseline()` 与 `HashOf()` 的差异即可用于
  变更检测。
