# 文件传输功能 · 测试需求

本文件定义 cross-clipboard「跨设备文件复制/粘贴」功能的测试需求,作为实现完成后的验收依据。功能来源:把 zero-share 的文件传输能力用 Go 移植进 cross-clipboard(libp2p 传输 + PGP/AES 加密)。

## 1. 测试目标

在一台设备**复制**一个文件,另一台设备**粘贴**出该文件,内容逐字节一致;传输全程加密;仅 mutually-trusted 设备可收发。

核心断言:
- 收到的文件 SHA-256 == 原文件 SHA-256
- 文件内容在链路上不可被第三方读取(端到端加密)
- 未通过 trust 的设备收不到文件

## 2. 测试环境

| 项 | 本地 Linux(复制侧) | Windows(粘贴侧) |
|---|---|---|
| 主机 | zsx-linux-pc | 192.168.68.97 |
| 源码目录 | `/home/zsx/tmp/cross-device-sync/cross-clipboard` | `C:\Users\Administrator\cross-clipboard-build` |
| 二进制 | `cross-clipboard` | `cross-clipboard.exe` |
| Go | `/home/linuxbrew/.linuxbrew/bin/go` 1.26.2 | `C:\Program Files\Go\bin\go.exe` 1.26.2 |
| 收方临时目录 | `<repo>/incoming` | `C:\Users\Administrator\cross-clipboard-build\incoming` |
| 剪贴板工具 | `xclip`、`xdotool`,`DISPLAY=:1` | PowerShell / native(`CF_HDROP`、`SendInput`) |
| 远程驱动 | 本地直接执行 | `sshpass -p <WIN_PASS> ssh Administrator@192.168.68.97` |
| 凭据 | — | `WIN_PASS` 环境变量(通过环境变量传入,勿提交) |

网络:两台同处 `192.168.68.0/24`,mDNS 可互发发现。

## 3. 前置条件

1. 文件传输功能已实现:
   - `pkg/protobuf/data.proto` 含 zero-share 的 `MetaData` / `ReceiveEvent` / `Message`(已 `make protogen`)。
   - `pkg/filetransfer`(sender / receiver / AES / PGP 包钥匙)。
   - `pkg/stream` 识别 `DataTypeFileMessage` 并路由到 filetransfer。
   - OS 文件剪贴板集成(Linux `xclip` 读 `text/uri-list`;Windows `CF_HDROP` 写入 + 可选 `auto_paste`)。
2. 两台 `group_name` 相同且 `auto_trust=true`(或已手动互信)。
3. 两台已各自构建最新二进制。
4. Linux 侧 `DISPLAY` 可用、`xclip`/`xdotool` 在 PATH。
5. Windows 侧 SSH 可达、Go 在 PATH。

## 4. 测试用例

> 每个用例标注覆盖的 zero-share 协议元素,确保移植忠实度。

### TC-01 主流程:Linux 复制 → Windows 粘贴
- **步骤**:Linux 生成测试文件 → 用 `xclip` 写入 `text/uri-list` 触发发送 → Windows 侧轮询收方临时目录 → 等待文件出现 → 计算两端 SHA-256。
- **预期**:Windows 收到同名文件;`SHA256(linux) == SHA256(windows)`;文件名与原名一致。
- **覆盖**:MetaData 发送、AES 分块、RECEIVED_CHUNK 握手。

### TC-02 反向:Windows 复制 → Linux 粘贴
- **步骤**:Windows 把文件写入剪贴板(`CF_HDROP`)→ Linux 侧轮询收方临时目录 → 校验 SHA-256。
- **预期**:同 TC-01,方向相反。
- **覆盖**:双向对称性。

### TC-03 端到端加密
- **步骤**:在传输过程中于链路抓包(libp2p 流)或检查 sender 发出的字节。
- **预期**:文件名与文件内容均为密文;AES 钥匙经 PGP 包裹(非明文);只有持有对应 PGP 私钥的收方能解出。
- **覆盖**:PGP 包 AES key、AES-GCM 分块加密。

### TC-04 大文件 / 多分块
- **步骤**:Linux 发送一个 ~50 MiB 文件(远大于单块 32 KiB,触发多轮 stop-and-wait)。
- **预期**:收方分块重组后 SHA-256 一致;过程中终端显示进度与速率。
- **覆盖**:stop-and-wait 流控、分块累加、进度上报。

### TC-05 接受 / 拒绝握手
- **步骤 A(auto_accept=true)**:收方自动回 `ACCEPT`,传输正常完成。
- **步骤 B(auto_accept=false,拒收)**:收方回 `REJECT`;发方停止发送,不产生残留文件。
- **预期**:握手状态机与 zero-share `ReceiveEvent` 一致。
- **覆盖**:EVENT_RECEIVER_ACCEPT / EVENT_RECEIVER_REJECT。

### TC-06 校验失败 → 拒绝
- **步骤**:人为篡改链路上的某个 chunk 内容。
- **预期**:收方校验 SHA-256 不匹配 → 回 `VALIDATE_ERROR` → 丢弃临时文件;原文件不残留。
- **覆盖**:EVENT_VALIDATE_ERROR、SHA-256 校验。

### TC-07 超限拒绝
- **步骤**:发送一个 > `max_file_size`(默认 1 GiB)的文件。
- **预期**:收方在 MetaData 阶段即拒绝,不写入磁盘。
- **覆盖**:`validateFileMetadata(maxSize)`。

### TC-08 防回环
- **步骤**:Windows 收到文件后写入自己剪贴板 → 观察 Windows 本地 watcher 是否再次回发给 Linux。
- **预期**:短时间窗口内(self-set guard)本地 watcher 忽略刚写入的文件,不发回对端;无 ping-pong。
- **覆盖**:self-set 去重窗口。

### TC-09 非信任设备不可收
- **步骤**:A 与 B 未互信(`auto_trust=false` 且未手动 trust)→ A 发文件。
- **预期**:B 不接收;不产生密文泄露;文件通道仅对 `StatusConnected` 且持有 PGP 加密器的设备开放。
- **覆盖**:trust 闸门。

### TC-10 文件名安全
- **步骤**:发送文件名为 `../../../../etc/passwd` 或含空格/中文/特殊字符。
- **预期**:收方落盘时经 `SafeFileName` 处理,仅取 basename、禁止路径穿越;文件落在临时目录内、不越界。
- **覆盖**:文件名净化。

## 5. 验收标准

- TC-01 ~ TC-10 全部通过。
- Linux 与 Windows 两端均 `go build ./...` 通过、`go test ./...` 通过。
- 主流程(TC-01)与反向(TC-02)文件 SHA-256 严格一致。

## 6. 单元测试覆盖要求(`pkg/filetransfer/*_test.go`)

- AES-GCM-128 加密/解密 round-trip(IV 12B 前置,同 zero-share `encryptAesGcm`)。
- PGP 包裹 / 拆解 AES 钥匙(用 `PGPEncrypter`/`PGPDecrypter`)。
- sender 分块发送 + receiver 重组 == 原字节。
- SHA-256 校验通过 / 失败分支。
- `ReceiveEvent` 状态机:ACCEPT → 发块 → RECEIVED_CHUNK → 下一块 → 完成。
- stop-and-wait:发方在未收到 `RECEIVED_CHUNK` 时不发下一块。
- `SafeFileName` 路径穿越防护(表驱动)。
- Dedup 防回环窗口命中 / 过期。

## 7. 执行方式

- 单元测试:`cd /home/zsx/tmp/cross-device-sync/cross-clipboard && go test -v ./pkg/filetransfer/...`
- 全量:`go build ./... && go test ./...`
- e2e:实现完成后,在 Linux 侧运行编排脚本,自动驱动 Linux 本地与 Windows(SSH),按 TC-01/TC-02 执行并比对 SHA-256。脚本待随实现一并提交。
