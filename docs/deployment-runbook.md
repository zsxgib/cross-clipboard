# cross-clipboard 部署与运维手册

> 双机环境：Linux 开发机 ↔ Windows 桌面机（同一局域网，mDNS 自动发现）。
> 本文记录稳定部署方式与排查过的坑（2026-09 实测）。

## 拓扑与配置

| 项 | Linux 本机 | Windows 对面 |
|---|---|---|
| IP | 172.18.16.18x（DHCP，会变） | 172.18.16.197（固定） |
| 访问 | — | SSH `Administrator@172.18.16.197`（密码 5566） |
| 配置目录 | `~/.config/cross-clipboard/` | `C:\Users\Administrator\.config\cross-clipboard\` |
| 二进制 | 仓库根 `cross-clipboard` | `C:\Users\Administrator\cross-clipboard-build\cross-clipboard.exe` |

两边 `config.yaml`：`auto_trust: true`（首次连接自动信任，不弹确认）、`listen_port: 4001`。

## 启动方式（必须用这两种，别的都踩过坑）

### Linux 本机（-t 终端模式，日志落盘）

```bash
cd ~/tmp/cross-device-sync/cross-clipboard
DISPLAY=:1 setsid nohup ./cross-clipboard -t </dev/null >/tmp/cc-live.log 2>&1 &
```

- 需要 X display（xclipboard 用 X11 读剪贴板），无头环境先起 Xvfb
- 日志实时看：`tail -f /tmp/cc-live.log`

### Windows 对面（schtasks 计划任务，-t 模式）

```powershell
# 传新版本前先杀进程（exe 被占用无法覆盖）
Stop-Process -Name cross-clipboard -Force
scp cross-clipboard.exe Administrator@172.18.16.197:"C:/Users/Administrator/cross-clipboard-build/cross-clipboard.exe"

# 创建并运行（ONCE 任务不会自动重复跑）
schtasks /Create /TN ccb-sync /TR "C:\Users\Administrator\cross-clipboard-build\cross-clipboard.exe -t" /SC ONCE /ST 00:00 /F
schtasks /Run /TN ccb-sync
```

**必须带 `-t`**：GUI 模式（无参数）在计划任务/无控制台环境会因 tview 打不开终端直接 panic。

### 验证连通

```bash
tail -f /tmp/cc-live.log        # 应看到 discovered / connected / trusted
python3 -c "import json; d=json.load(open('$HOME/.config/cross-clipboard/devices.json')); [print(k,v.get('name'),v.get('status')) for k,v in d.items()]"
```

## 交叉编译 Windows 版

```bash
cd cross-clipboard
GOOS=windows GOARCH=amd64 go build -o /tmp/cross-clipboard.exe .
```

## 常见坑（全部实测踩过）

### 1. 设备变 blocked（自动拉黑）— 已修复
- **原因（已修，commit 74683b4）**：旧版收到 >1MB（`limitDataSize`，注释误写 100MB）单块剪贴板数据就把发送方**永久拉黑写盘**；而发送上限 max_size=5MB，>1MB 的大图/大文本是正常流量。逻辑顺序 bug 导致 5MB 的"丢弃"分支永远执行不到。
- **修复**：超 max_size 改为丢弃数据不拉黑；`limitDataSize` 提到 64MB 只做内存防线，超限断连（error）不拉黑。blocked 现在只来自手动操作。
- **手动 blocked 的坑**：终端模式 `wanted to connect (Y/n)` 按 `n` = 永久拉黑（不是"这次拒绝"）。auto_trust=true 时一般不会弹。
- **解除 blocked**：改 `devices.json` 里该设备 `"status": "disconnected"`（保留 publicKey），重启后自动重连恢复 connected。
- **UI 颜色混淆**：`ui/device.go` 中 `error` 和 `blocked` 都渲染红色，对面 GUI 看到红色未必是 blocked。

### 2. Windows 剪贴板按会话隔离（Session 0 vs Session 1）
- SSH/计划任务非交互进程跑在 Session 0，桌面程序在 Session 1，**剪贴板互不相通**。
- 用 SSH 的 PowerShell `Get-Clipboard` 读不到桌面剪贴板 ≠ 同步失败。验证桌面剪贴板要借道计划任务：
  ```powershell
  schtasks /Create /TN ccb-readclip /TR "powershell -Command Get-Clipboard | Out-File C:\...\clip.txt" /SC ONCE /ST 00:00 /F
  schtasks /Run /TN ccb-readclip
  ```

### 3. 进程单实例纪律（重要）
- **多个实例同时监听 4001**（Windows 允许多进程绑同端口）会导致连接被随机抢走、推送"失灵"、状态混乱。对面残留的 SSH 前台测试进程会一直活着（timeout 只杀 ssh 客户端，杀不掉对面 sshd 的子进程！）。
- 操作前先确认：`Get-Process cross-clipboard` 应只有 1 个；Linux `ps aux | grep cross-clipboard` 同理。
- 本机多实例时日志互相覆盖截断，表现为"日志为空但进程在跑"。

### 4. X11 剪贴板 owner 挂起导致启动卡死
- 残留的 `xclip` 进程若持有 CLIPBOARD selection 但不响应（写大内容 INCR 传输挂起），cross-clipboard 启动时 `xclipboard.Read`（cgo 同步调用）会**永久阻塞**，进程无日志无监听。
- 排查：`ps aux | grep xclip`，杀掉残留 `pkill -9 -x xclip` 后重启。
- 用 xclip 写入大内容（>1MB）不可靠（INCR 问题），测试大内容同步请从 Windows 侧发起。

### 5. QQ 占用 4001（无害但易误判）
- QQ 监听 `127.0.0.1:4001`（仅回环），cross-clipboard 监听 `0.0.0.0:4001`，二者共存不冲突，局域网连接不受影响。
- 4000/4001 是腾讯老端口习惯，不是故障。

### 6. 路由器 AP 隔离会导致"ping 不通但路由器能通"（根因与治本方案）
- 现象：无线客户端之间互不可达（ARP 都失败，`Destination Host Unreachable`），但路由器 ping 目标正常、SSH/3389 也不通。
- **根因（已定位到代码）**：OpenWrt/LiBwrt 的 `/lib/netifd/netifd-wireless.sh` 中 `_wireless_set_brsnoop_isolation()`：只要 bridge 的 `multicast_to_unicast` 生效（默认行为），netifd 会给无线接口**强制 `isolate=1`** → hostapd 生成 `ap_isolate=1`；而 br-lan 各端口 `hairpin_mode=0`，客户端流量无法经 bridge 回流 → 彻底隔离。
- 常见的错误修法（不生效）：把 `isolate` 设在 `wireless.radioX`（那是 wifi-device 段，正确位置是 wifi-iface `wireless.default_radioX`）；或在 wifi-iface 里写 `option ap_isolate`（选项名错了，hostapd.sh 只读 `isolate`）。
- **治本方案（重启路由器后不复发）**：
  ```bash
  uci set network.@device[0].multicast_to_unicast='0'   # br-lan 设备段
  uci commit network
  ubus call network.wireless reconf                      # 重新生成 hostapd 配置
  # 验证：grep ap_isolate /var/run/hostapd-phy*.conf  → 应无输出
  ```
  注意 `reconf` 会踢掉所有无线客户端（Windows 约 20-30 秒后自动重连）。
- 该固件的 `wifi reload` 不会重新生成 hostapd 配置；`ubus call network.wireless reconf` 才会（配置由 netifd 经 ubus `hostapd config_set` 热加载，无需重启 hostapd 进程）。

### 7. 其他小坑
- Linux 用 `pkill -f "cross-clipboard -t"` 会匹配并杀死自己的 shell（命令行含同样字符串），用精确 PID 或 `pkill -x`。
- devices.json 空文件/握手中断脏记录导致启动崩溃：已修复（commit 99e2ef2），Load 容错 + Save 只落盘完成握手的设备。
- 本机 IP 是 DHCP（172.18.16.181 → .182 变过），mDNS 自动发现，不用改配置。

## 状态速查

```bash
# 本机
ps aux | grep [c]ross-clipboard && ss -tnp | grep 4001
# 对面
sshpass -p 5566 ssh Administrator@172.18.16.197 "powershell Get-Process cross-clipboard | Select Id,SessionId"
# 两端信任状态
cat ~/.config/cross-clipboard/devices.json
```
