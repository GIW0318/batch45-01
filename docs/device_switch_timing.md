# 输出设备切换时序说明

## 涉及接口

- `SwitchOutputDevice(device string) error`：请求切换监听/输出设备。
- `OutputDevice() string`：读取当前绑定的设备名。
- `RunAudioCallback(render func(device string, frames int) []float32, frames int) []float32`：以一次完整音频回调的粒度执行渲染。

## 锁模型

设备状态由独立的 `deviceMu sync.RWMutex` 保护，与混音数据锁 `mu`（槽表、
样本表、预算、播放位置）分离，避免设备切换与实时回调互相阻塞混音逻辑。

- 回调路径：进入 `RunAudioCallback` 时持有 `deviceMu` 的**读锁**，在锁内
  一次性读取 `outputDevice`，然后整段 `render` 都在同一把读锁保护下执行，
  渲染结束后才释放读锁。
- 切换路径：`SwitchOutputDevice` 申请 `deviceMu` 的**写锁**后才改写
  `outputDevice`。

Go 的 `RWMutex` 保证写锁与任何读锁互斥，因此：

1. 回调进行中，写锁申请会阻塞，切换只能在该回调释放读锁后生效。
2. 切换进行中，新回调的读锁申请会阻塞，直到设备名改写完成。

## 时序

```
音频回调线程                     设备管理线程
------------------------------   ------------------------------
RunAudioCallback 开始
RLock(deviceMu)
device := outputDevice ("spk")
render(device, frames) 渲染中...
                                 SwitchOutputDevice("hp")
                                 Lock(deviceMu)  <-- 阻塞等待
渲染结束
RUnlock(deviceMu)
                                 Lock 获得，outputDevice = "hp"
                                 Unlock
下一次 RunAudioCallback
RLock 获得，读到 "hp"，整帧按 hp 渲染
```

## 不变量

- 一次回调内 `render` 只看到一个设备名，产出的所有帧属于同一设备，不存在
  半帧旧设备、半帧新设备的拼接。
- 设备切换严格发生在两次回调之间：要么在回调 N 开始前完成（回调 N 全部用
  新设备），要么在回调 N 结束后完成（回调 N 全部用旧设备，回调 N+1 起用
  新设备）。
- 切换与回调的互斥只针对设备状态；槽位抢占、样本加载、预算扣减、暂停推进
  等仍由 `mu` 独立保护，回调期间这些操作可以照常并发进行。

## 验证方式

临时验证测试（验证后已删除，未改动官方测试）覆盖：

- 回调渲染中调用 `SwitchOutputDevice`，确认调用方阻塞、`OutputDevice()`
  保持旧设备；回调返回后切换才完成，且下一次回调内拿到的设备名恒为新设备。
- 4 路设备切换 + 4 路回调 + 4 路语音分配并发跑 `go test -race -count=20`，
  无数据竞争，每次回调返回的帧数与入参一致。
