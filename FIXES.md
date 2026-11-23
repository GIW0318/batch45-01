# SudoersGuard 修复说明

落点：`sudoers_guard.go`，全部方法保留既有签名，`SudoersGuard` 保持无状态。

## 逐项修复

- 别名展开：`ExpandAlias` 递归展开嵌套别名，深度上限 32，自引用/环报错。
- 命令匹配：`MatchCommand` 改为完整路径精确匹配（规则路径含 `*`/`?` 时走 `path.Match` glob），不再按前缀放行。
- 参数匹配：`MatchArgs` 按空白分词后逐 token 比对，含 shell 元字符直接拒绝。
- 组嵌套：`ExpandGroup` 递归展开组成员中的子组（`%` 前缀可选），同样有深度上限与环检测。
- Host 校验：`CheckHost` 要求请求主机在规则列表中（支持 `*` 通配），`Evaluate` 不再忽略 Host。
- NOPASSWD：`ResolveNoPasswd` 只取实际命中规则 `rules[matchIndex]` 的标签，索引越界报错。
- runas：`ResolveRunAs` 对空列表返回 Deny，不再默认 root。
- include：`ValidateInclude` 将路径 Clean 后限制在 drop-in 根目录之内，拒绝绝对路径外的引用与 `..` 逃逸。
- 否定规则：`Evaluate` 采用"最后命中规则生效"，命中的否定规则行使否决权。
- shell 注入：`SanitizeCommand` 拒绝 `;|&$\`<>(){}[]*?!#~` 等元字符，`Evaluate` 入口先消毒请求命令。
- 缓存失效：`InvalidateCache` 在策略变更后丢弃全部缓存决策，返回空表。
- 解析回退：`ParsePolicy` 对括号不配对、缺 `host=`、缺命令列表等畸形输入返回 Deny，不再回退到允许。

## 约束的落实方式

- 重载原子性：guard 无状态，调用方以不可变快照（如 `atomic.Pointer[[]Rule]`）整体换入新规则，在途判断只会看到完整的旧规则或完整的新规则；已用 `go test -race` 下的并发读写临时用例验证（验证后删除，未混入交付测试）。
- 展开上限：别名与组共用 `maxExpandDepth = 32`，环通过展开链上的 active 集合检测并报错。
- 结果一致性：同一请求对同一规则快照求值结果确定；缓存键需包含策略版本号（如 `v<version>|user|command`），版本随换快照递增，配合 `InvalidateCache` 避免跨版本命中。

## 验证

`go test ./... -v -count=1` 15 个用例全部通过，完整输出见 `artifacts/test-output.txt`。
