# 仓库贡献指南

本指南面向 **cross-clipboard** 的贡献者。这是一个基于 Go 的 P2P
剪贴板共享工具，基于 libp2p、mDNS、OpenPGP 和 `tview` TUI 构建。

## 项目结构与模块组织

- `main.go` — 程序入口。解析命令行参数（`-t` 进入 TUI 模式）并组装各个包。
- `pkg/` — 核心库，按职责拆分为独立包：
  - `clipboard`、`clipboardfile` — 操作系统剪贴板访问（文本与文件 URI）。
  - `crossclipboard` — 协调器，统一调度 peer、加密、传输。
  - `crypto` — OpenPGP 加密与设备身份（`id.go`、`pgp.go`）。
  - `device`、`devicemanager` — peer 设备模型与持久化。
  - `discovery` — mDNS 节点发现。
  - `filetransfer` — 节点间文件传输。
  - `stream` — 编码与解码工具。
  - `protobuf` — 由 protoc 生成的 gRPC 代码（通过 `make protogen` 重新生成）。
  - `config`、`xerror` — 配置加载与错误类型。
- `ui/` — `tview` 页面与弹窗，包括 `home_page.go`、`setting_page.go`、
  `device.go`、`log_page.go`，以及 `confirm_modal.go`、`trust_modal.go`。
- `mobile/` — Android 平台的 gomobile 绑定（iOS 进行中）。
- `scripts/` — 端到端测试脚本：`e2e-test.sh`、`x11_fileclip_helper.py`。
- `docs/` — README 配图与 `wsl2.md` 配置说明。

## 构建、测试与开发命令

所有目标定义在 `Makefile` 中；`run.sh` 用于在虚拟显示中启动程序。

- `go run ./main.go` — 以 GUI 模式运行。
- `make run-terminal`（或 `go run ./main.go -t`）— 以 TUI 模式运行。
- `make build` — 编译生成 `cross-clipboard` 可执行文件。
- `make release` — 通过 `goreleaser --snapshot --clean` 生成本地快照。
- `make protogen` — 重新生成 `pkg/protobuf/` 中的 protobuf Go 文件。
- `go test ./...` — 运行单元测试（CI 使用的也是此命令）。
- `make build-mobile` — 执行 `gomobile build ./mobile/...`（需安装 NDK）。

## 代码风格与命名规范

- 采用标准 Go 风格；提交前请运行 `gofmt`/`goimports`。
- 包名简短、全小写、不含下划线（例如 `devicemanager`）。
- 文件名与主类型保持一致（如 `clipboard_manager.go`、`pgp_encrypt.go`）。
- 测试文件以 `_test.go` 结尾，与被测代码同目录
  （例如 `pkg/filetransfer/roundtrip_test.go`）。
- 平台相关代码放在 `linux.go` / `windows.go` 等同名兄弟文件中。
- 提交信息遵循 Conventional Commits，仓库历史中常见的格式包括
  `feat:`、`fix:`、`chore:`、`test(e2e):`、`fix(win):`、`fix(linux):`、`diag:`。

## 测试规范

- 使用标准 `testing` 包；通过 `go test -v ./...` 运行
  （与 `.github/workflows/go.yml` 中的命令一致）。
- 为 `stream/`、`crypto/` 等编解码逻辑编写表驱动测试。
- 端到端剪贴板流程位于 `scripts/e2e-test.sh`，依赖 Xvfb
  （`run.sh` 会设置 `DISPLAY=:99.0`）以及 Linux 上的 `xclip`。

## 提交与 Pull Request 规范

- 每次提交只包含一项逻辑变更；使用括号注明作用域
  （例如 `fix(win): use native Win32 SetClipboardData`）。
- Pull Request 请合并至 `master`（CI 唯一构建的分支）。请在描述中
  说明改动内容、测试过的平台（Windows/Linux/Darwin），并关联相关 issue。
- 涉及 UI 或剪贴板行为变更时，请附上截图或终端录屏，并写明
  手工复现步骤。
- 提交评审前请确保 `go build ./...` 与 `go test ./...` 均通过。

## 安全与配置提示

- 严禁提交真实的 GPG 密钥或设备身份信息；`pkg/crypto` 会在运行时
  生成并保存到操作系统的用户配置目录。
- 在无头 Linux 环境下，请先安装 `libx11-dev xvfb` 并设置
  `DISPLAY=:99.0`（参考 `run.sh` 和 `docs/wsl2.md`）。
- 生成的 protobuf 文件已纳入版本控制；只有当 `pkg/protobuf/data.proto`
  发生变化时才需要执行 `make protogen` 重新生成。
