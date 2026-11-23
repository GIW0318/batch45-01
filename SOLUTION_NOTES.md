# SVID Rotator 修复记录

## 交付内容

- `spiffe_svid_rotator.go`：完整实现 `SvidRotator`，未修改任何既有测试。
- 本文件：状态链设计、幂等键说明与自测记录。

## 状态链设计

- SVID 轮换：`BeginRotation` 只创建 pending 候选，旧 SVID 继续作为 active；`CommitRotation` 在一次加锁内挂载新 SVID 并切换 active；`RollbackRotation` 丢弃 pending，旧 SVID 不变。提交时若候选已失效或私钥冲突，自动丢弃候选并保留旧 SVID。
- 单轮换门禁：pending 存在时，不同目标版本直接拒绝；相同目标版本返回原 rotation id。
- bundle：`UpdateBundle` 深拷贝后整份替换；`RefreshBundle` 将端点返回的根与缓存根去重合并，版本取较大值，并按 `BundleRefreshInterval` 限流。限流错误为 `ErrBundleRefreshRateLimited`，可用 `errors.Is` 识别。
- 令牌：令牌只保存 SHA-256 摘要；`RevokeTokensForSVID` 通过代际号批量失效该 SVID ID 的既有令牌。私钥材料、原始 bearer token 均不写入导出状态。
- 恢复：`Restore` 先在临时结构中解析并校验全部 SVID、bundle、令牌、pending，再一次性替换状态；`Ready` 仅在存在未过期 active SVID 时返回 true。
- 身份校验：SVID 与 peer chain 均要求 SPIFFE ID 位于 `spiffe://<TrustDomain>/` 前缀下，跨 trust domain 直接拒绝。
- 副本指纹：基于 trust domain 与 workload SPIFFE ID 计算 SHA-256，不依赖 serial 或私钥，因此轮换进度不同的副本仍得到相同身份指纹。

## 幂等键

轮换幂等键为：

```text
workload SPIFFE ID + "\x00" + 目标版本
```

目标版本优先使用 `SVID.Serial`；serial 为空时退化为 `NotAfter` 与私钥引用摘要的组合。语义如下：

- 同一幂等键的并发/重复 `BeginRotation` 返回同一个 rotation id，不创建第二份候选或私钥引用。
- pending 期间不同幂等键返回 `ErrRotationInProgress`。
- 已提交且 active 仍是该目标版本时，重复申请返回原 rotation id；重复 `CommitRotation` 幂等成功。
- 回滚后同一目标再次申请会产生新的 rotation id，旧候选不会进入 `SVIDs()`。

## 隐私边界

- `SVID.PrivateKeyID` 不作为原始值导出，导出状态只包含不可逆 `keyref:` 摘要。
- 错误串只包含 SPIFFE ID、serial、trust domain 等元数据，不包含私钥内容。
- 令牌导出只包含令牌摘要和撤销代际，不包含原始令牌。

## 自测记录

既有测试（未改动）：

```text
go test -v ./...
15/15 PASS

go test -race ./...
ok gsb/batch45-safety-a-06/tests

go test -count=20 ./...
ok gsb/batch45-safety-a-06/tests

go test -race -count=5 ./...
ok gsb/batch45-safety-a-06/tests

go vet ./...
无告警
```

另用临时测试验证后删除，覆盖：

- 24 个并发同目标 `BeginRotation` 全部返回同一 id，提交后只有一套新旧私钥引用。
- pending 状态导出/恢复后 active 仍为旧 SVID，重复申请返回同一 rotation id，恢复后可正常提交。
- 令牌、bundle、撤销代际跨恢复保持一致；原始令牌和私钥引用不出现在导出状态。
- 提交时候选过期会返回 `ErrRotationFailed`，active 保持旧 SVID，且不残留候选。
- 不同 serial 的副本指纹相同。

## 时间处理说明

部分入口没有 `now` 参数。实现维护逻辑观察时钟：显式传入的时间只前进不回退；首次加载若证书时间远离宿主墙钟（固定历史时间夹具或离线恢复），以 `NotAfter - RenewBefore` 建立观察点，避免宿主当前时间把有效夹具误判为过期。`NeedsRenewal` 仍严格使用调用方传入的 `now` 与 `NotAfter - RenewBefore` 比较。
