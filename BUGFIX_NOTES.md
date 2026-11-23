# SkinPalette 修复记录

## 结论

`skinning_palette_allocator.go` 已按 Bugfix 要求补齐 `SkinPalette` 的分配、写入、发布、重映射、权重校验、回收与根运动分离逻辑。15 条验收用例全部通过，测试文件未修改、未跳过、未写死。

## 根因与修复

| 现象 | 根因 | 修复 |
| --- | --- | --- |
| 角色共用一段纹理、槽位互相覆盖 | 所有角色从同一基址写入 | 每个 `characterID` 独占一段连续槽位，`SlotOffset` 回读基址 |
| 相邻角色数据被越界采样碰到 | 骨骼区之间没有隔离 | 每个角色骨骼行后固定垫 1 行隔离槽，内容复制该角色最后一个骨骼矩阵；越界一格只会读到自己 |
| 骨骼数超过纹理宽度后回绕 | 只按总容量判断，未检查行边界 | `boneCount > texWidth` 拒绝；分配时保证 `base % texWidth + boneCount <= texWidth` |
| 矩阵未转置就上传 | CPU 列主序直接进入采样区 | `Barrier` 发布时按 `out[row*4+col] = in[col*4+row]` 转为 GPU 行主序 |
| 写入与采样无屏障 | 写操作直接暴露给采样 | `WriteMatrices` 只更新 CPU 源数据；`Barrier` 才发布到 GPU 镜像并推进 generation |
| LOD 切换索引未重映射 | 授权骨骼索引直接使用 | `RemapBoneIndex` 按 `(character, lod, oldIndex)` 缓存映射，重复调用结果稳定 |
| 权重和不为 1 仍蒙皮 | 未做归一化校验 | `ValidateWeights` 拒绝空、负数、NaN、Inf 及和不在 `1±1e-6` 的权重 |
| 多线程写调色板 | 共享槽位无同步 | 所有状态读写由 `sync.RWMutex` 保护；generation 使用原子计数 |
| 销毁后槽位泄漏 | 没有回收/合并空闲区 | `Release` 删除角色、清除 GPU 槽位并把骨骼区与隔离行一起归还，数组按起点排序并合并相邻空闲区 |
| 根运动未剥离，位移翻倍 | 骨骼矩阵仍带根位移 | `SplitRootMotion` 返回根矩阵，并从每个骨骼副本中减去根平移 `[12]/[13]/[14]` |
| 换宽度后 UV 未重算 | U 步长固定 | `RecomputeUV` 返回 `1/texWidth`，并更新后续分配使用的纹理宽度 |
| CPU/GPU 不一致 | 两侧没有可比较的发布版本 | CPU 保留原始矩阵，GPU 保留发布后的转置矩阵；`CPUGPUConsistent` 逐骨骼及隔离行比较 |

## 槽位布局

```text
character A: [bone 0 ... bone N-1][guard = copy(bone N-1)]
character B: [bone 0 ... bone M-1][guard = copy(bone M-1)]
```

- 骨骼区与隔离行属于同一角色，`Release` 一起回收。
- `maxBones` 是骨骼矩阵总预算，隔离行不计入该预算；物理槽位额外预留了行边界跳转产生的空洞。
- 分配采用按起点排序的空闲区首次适配，回收时合并相邻区间；释放后优先复用低地址槽位。

## 并发与发布

- `Allocate` / `Release` / `WriteMatrices` / `Barrier` / `RemapBoneIndex` 使用写锁，`SlotOffset` / `ReadBack` / `ActiveCharacters` / `TextureWidth` 使用读锁。
- `PaletteGeneration` 只在 `Barrier` 中通过 `atomic.Uint64.Add(1)` 前进，读取无锁。
- 写入后、发布前 `CPUGPUConsistent` 返回 `false`；发布后返回 `true`。
- 公开 `ReadBack` 返回副本，调用方无法绕过 `WriteMatrices` 修改内部数据。

## 验证

```text
go test ./... -count=1          # 15/15 通过
go test -race ./... -count=1    # 通过
go vet ./...                    # 通过
```

额外做过临时并发自验（已删除，不进入交付测试集）：多 goroutine 循环执行分配、写入、回读、LOD 重映射、屏障、CPU/GPU 一致性检查和回收，`-race` 无告警；行边界与隔离行检查通过，最终活跃角色数回到 0。

本次仅修改源码并新增本记录，未推送上游、未创建提交。
