# HijackGuard 状态机

## 状态

| 状态 | 含义 |
| --- | --- |
| `Absent` | 该前缀没有已安装路由，但可能保留最新 `Seq` 和 flap 计数 |
| `Installed` | 路由已进入 RIB，记录 peer、origin、priority、next-hop 和 seq |
| `Rejected` | 本次公告未安装；只追加拒绝历史，RIB 不变 |
| `Damped` | `flaps(prefix) >= FlapThreshold`，后续公告先被 `HIJACK_DAMPED` 拦截 |
| `Tombstoned` | ROA 撤销后前缀被锁定，任何未获当前 ROA 授权的公告都不能安装 |

`Rejected` 不是一个持久前缀状态，而是每次 `Announce` 的失败结果。九个拒绝码依次为：
`RPKI_INVALID`、`PRIVATE_AS`、`AS_SET`、`COMMUNITY`、`DEFAULT_DENIED`、
`AS_PATH_TOO_LONG`、`STALE_UPDATE`、`MOAS_CONFLICT`、`DAMPED`。

## Announce 判定顺序

```text
lock
  -> Received++
  -> DAMPED?                  reject HIJACK_DAMPED
  -> RPKI invalid / ROA?      reject HIJACK_RPKI_INVALID
  -> private ASN?             reject HIJACK_PRIVATE_AS
  -> AS_SET?                  reject HIJACK_AS_SET
  -> community whitelist?     reject HIJACK_COMMUNITY
  -> default not allowed?     reject HIJACK_DEFAULT_DENIED
  -> len(path)+prepend?       reject HIJACK_AS_PATH_TOO_LONG
  -> seq <= latest seq?       reject HIJACK_STALE_UPDATE
  -> MOAS same prefix and
     different origin and
     priority <= installed?   reject HIJACK_MOAS_CONFLICT
  -> install route, update seq, clear tombstone if ROA-authorized
unlock
```

拒绝路径只更新 `Rejected`、拒绝历史和 flap 之外的状态，绝不改写 RIB。
同一 origin 的新 `Seq` 可以刷新路由；不同 origin 必须同时满足
`Seq > latest` 且 `Priority > installed.Priority` 才能替换。

## 关键迁移

```text
Absent --Announce(valid)--> Installed
Installed --Announce(valid, newer seq, same origin)--> Installed (replace)
Installed --Announce(valid, newer seq, higher priority, other origin)--> Installed (replace)
Installed --Announce(any rejection)--> Rejected (Installed unchanged)
Installed --Withdraw(owner peer)--> Absent   [flaps++, Withdrawn++]
Absent    --Withdraw(any peer)--> Absent     [flaps++, idempotent success]
Installed --SessionDown(peer)--> Absent for that peer only [Withdrawn++]
Absent/Installed --flaps >= threshold--> Damped
Damped --Announce(any)--> Rejected(HIJACK_DAMPED)
```

`Seq` 在路由撤销后仍保留。因此会话 down、普通 withdraw 或进程重启后，
旧序号回放不会把旧路由重新装回。

## 快照与重启

`Snapshot()` 在读锁内一次性复制：RIB、拒绝历史、每个前缀最新 `Seq`、
flap 计数、墓碑集合以及每条路由安装时的 ROA 授权结果。因此不会出现
“路由已复制、序号未复制”的半更新快照。

`NewGuardFromSnapshot` 的恢复流程：

1. 恢复拒绝历史，并裁剪到最新 4096 条。
2. 恢复 `Seq`、flap 和墓碑；拒绝历史仍按最旧到最新排列。
3. 对每条快照路由用**当前配置**的 ROA 重新校验。
4. 仍被 ROA 授权：恢复为 `Installed`。
5. 曾被 ROA 授权、但当前无覆盖 ROA；或当前有覆盖 ROA 却不授权该 origin/长度：
   丢弃路由并转为 `Tombstoned`，保留最新 `Seq`。
6. 原本就没有 ROA 覆盖的路由：按原样恢复。

`Tombstoned` 前缀收到更大 `Seq` 的公告时，先被 RPKI 门禁拒绝为
`HIJACK_RPKI_INVALID`。只有当前配置出现覆盖该前缀和 origin 的 ROA，
公告通过全部门禁并成功安装后，墓碑才被清除。

## 并发

所有公开方法使用同一把读写锁：`Announce/Withdraw/SessionDown` 串行化，
`Lookup/Routes/Rejects/Stats/FlapCount/Snapshot` 使用读锁。相同前缀的
比较、替换和序号推进在同一次临界区内完成，因此旧公告不能覆盖新公告，
快照也看不到中间状态。
