# DNSSEC Validator 实现记录

实现文件：`dnssec_validator.go`。未修改 `tests/test_dnssec_validator_test.go`。

## 十二处修复

1. `ValidateSignature` 用 `ClockSkew` 检查 `Inception` / `Expiration`，过期返回 `ErrSignatureExpired`，未生效返回 `ErrSignatureNotYetValid`。
2. `CheckSignerZone` 规范化大小写和末尾根点，签名者不属于指定 zone 时返回 `ErrSignerMismatch`。
3. `ValidateSignature` / `Resolve` 同时要求签名者名字覆盖 RRset owner name，防止签名者与 RRset 无关。
4. `CheckKey` 要求 SEP 标志（`0x0001`）、协议 3、算法和 `KeyTag` 与 DS 一致（零值按“未填写”处理）；SEP 缺失或 KeyTag 不同返回 `false, nil`。
5. `CheckDSDigest` 按 `AllowedDigests` 白名单校验，未允许的摘要（默认仅 2/4）返回 `ErrDeprecatedDigest`。
6. `ValidateNSEC3` 在 `Iterations` 超过 `MaxNSEC3Iterations` 时返回 `ErrNSEC3Iterations`。
7. `ValidateWildcard` 缺少 closest encloser 时返回 `ErrWildcardProof`，并校验 wildcard / next-closer 与该 encloser 的从属关系。
8. `ValidateCNAMEChain` 对 `!Signed` 的任一环返回 `ErrUnsignedLink`。
9. `Canonicalize` 复制后按大小写不敏感的规范顺序排序；`ValidateSignature` 验签前调用它，记录顺序不影响结果。
10. `Rollover` 保留旧 DNSKEY 到 `Retired`，重复 KeyTag 以新 key 为准，轮换期旧 key 仍可用于 `CheckKey`。
11. `Resolve` 遇到任何未签名/结构错误的应答返回 `ErrBogus`，不会退回明文 DNS。
12. `Resolve` 在 `Truncated` 时走 TCP 路径并设置 `Result.UsedTCP`；`CachePut` 忽略 `Secure == false` 的负结果。

## 缓存键

```go
type CacheKey struct {
    Name       string // 规范化 owner name（小写、去末尾根点）
    Type       uint16 // RR type
    Zone       string // 规范化签名者 zone
    KeyTag     uint16 // DNSKEY key tag
    Generation uint64 // 入缓存时的 zone/key/tag/global 代数之和
}
```

缓存分区是 `(Name, Type, Zone, KeyTag, Generation)`。`CacheGetByKey` 可精确读取
某个 zone+KeyTag 分区；`CacheGet` 只在所有命中项中返回当前代数且未被撤销的条目。

## 撤销与并发

- 撤销入口：`RevokeKey(zone, keyTag)`；同时支持 `RevokeKey(keyTag)`、
  `RevokeKey(zone)`、`RevokeKey(DNSKEY)` 和 `RevokeKey()`，以及 typed aliases
  `Revoke` / `RevokeKeyTag` / `RevokeDNSKEY` / `RevokeZone` / `InvalidateKey`。
- 每个 `(zone, keyTag)` 维护单调递增的 generation，另有 zone、tag、global
  递增计数器。撤销会递增对应 generation 并删除匹配缓存项，因此旧判定立刻失效。
- 校验先取 generation 快照，完成纯校验后再次确认 generation 与撤销状态；期间发生
  撤销则返回 `ErrBogus`。缓存、撤销表、generation 全部由同一个 `sync.RWMutex` 保护，
  读写不会看到半更新状态。
- 并发/撤销自测使用临时测试文件执行 `go test -race ./...`，通过后已移除该临时文件，
  仓库仍保持原有 15 条测试。

## 验证

```text
go vet ./...
go test ./... -count=1
```

结果：`gsb/batch45-safety-c-05/tests` 全部 15 条通过；临时撤销/分区/并发测试在
`-race` 下通过。
