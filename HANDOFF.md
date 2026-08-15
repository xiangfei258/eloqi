# Eloqui 项目交接文档

> 最近核对：2026-08-15。
>
> 新对话或新工作目录请按顺序阅读：`ELOQUI_DESIGN.md` → `TASKS.md` → 本文 → 相关源码/测试。阶段勾选与验收边界以 `TASKS.md` 为准。

---

## 1. 项目快照

| 项 | 当前值 |
|---|---|
| 项目 | Eloqui，跨平台桌面语音输入（热键 → 录音 → ASR → 剪贴板/自动上屏） |
| 语言 / module | Go 1.25 / `github.com/xiangchang24/eloqi` |
| 许可证 | MIT，`Copyright (c) 2026 xiangchang24` |
| 目标平台 | Linux Wayland、Linux X11、macOS、Windows |
| 当前 Windows 工作区 | `D:\eloqi` |
| 远端 | `origin = https://gitee.com/xiangchang24/eloqi.git`（Gitee，上游）；`github = https://github.com/xiangfei258/eloqi.git`（Actions/Release 镜像） |
| 主分支 | `master` |
| 当前发布状态 | 尚无经过 CI、tag 和真机共同验收的正式发布 |

---

## 2. 合规红线

Eloqui 只参考 GPL v3 项目 just-talk-go 的功能思想，从零编写 MIT 原创实现。

- 禁止读取、复制、逐行翻译 `/home/xiangchanglin/projects/just-talk-go` 的源码。
- 不得沿用其目录结构、包名、函数名、注释或实现细节。
- 旧目录即使仍存在，也只能视为不可访问边界。
- 平台接口以 `AGENTS.md` 锁定签名为准，不改名、不加方法、不删字段。

---

## 3. 当前架构与入口

```text
cmd/eloqi
  └─ internal/app        CLI、doctor、平台装配、运行时与热重载
       ├─ internal/config   TOML + 目录 watcher
       ├─ internal/doctor   环境与权限检查
       ├─ internal/logging  slog 结构化日志
       ├─ internal/voice    P2 状态机和会话编排
       ├─ internal/asr      OpenAI 兼容非流式 ASR
       ├─ internal/stats    本地持久化统计
       ├─ internal/overlay  voice 状态到平台 overlay 的异步映射
       └─ internal/platform Linux / darwin / windows / mock
```

正常运行路径：加载配置 → doctor → 初始化日志/统计/平台能力/overlay → 启动 voice runtime → 监听配置目录 → 收到退出信号后依次关闭 watcher、voice、overlay 和 hotkey。

CLI：

```text
eloqi [--config PATH] [--debug]
eloqi --doctor [--config PATH]
eloqi --tui [--config PATH] [--debug] [--log-file PATH]
eloqi --version
```

`--tui` 只编辑并保存配置，不启动守护进程。验证热重载时应另开一个终端运行 TUI。

---

## 4. P0–P6 当前进度与证据

### P0 / P1

- 项目骨架、MIT、module、平台接口/mock 和 Linux 最小闭环已完成。
- 2026-08-14 曾在 GNOME Wayland 做过 P1 原始闭环真机验证：`Alt+Super` toggle，本机兼容 ASR，输出到剪贴板。
- 后续已经加固 evdev/X11 热键、arecord 停止/尾音频和错误传播，但这些加固后的真机回归还没做。
- GNOME Wayland 下 `wtype` 存在桌面兼容限制；可设 `output.auto_type=false` 使用剪贴板模式。

### P2

- 显式状态：idle / connecting / recording / stopping_delayed / stopping / error。
- hold/toggle、纯修饰键/功能键、默认 800ms 且允许显式 0 的停止缓冲。
- 会话 ID + 原子认领防止最终回调与 `Finalize` 重复输出。
- Escape 取消、R 使用全新 Recorder/ASR 重试、错误超时回 idle。
- 状态/会话回调按序分发，外部回调不持有 voice 内部锁，`Stop` 等待内部回调排空。
- Linux 定向 `internal/voice go test -race -count=3` 已通过；真机待测。

### P3

Windows 源码和本地自动化：

- GetAsyncKeyState 轮询 + 观察式 `WH_KEYBOARD_LL` 边沿回退；钩子不消费、不重放按键。
- WinMM 16 kHz/16 bit/mono PCM、有界缓冲和 Stop 唤醒。
- Win32 Unicode 剪贴板、SendInput Ctrl+V、非激活/置顶/点击穿透 overlay。
- Windows amd64 全仓 build/vet/test 通过；定向 386 测试和 arm64 平台包/主程序交叉编译通过。
- WinMM 对原生 `WAVEHDR`/样本/Pinner 的所有权只在 unprepare 和 close 成功后释放；失败或 pump 无法在 2 秒内确认退出时保留完整资源并强引用 quarantine，Stop/Close 有界返回。故障注入重复测试、checkptr 和 vet 已通过。

macOS 源码和当前边界：

- CGEventTap、AudioQueue、NSPasteboard、Command+V、NSPanel helper 的 Objective-C/cgo 源码已存在。
- 不依赖 cgo 的热键/overlay 状态逻辑已为 amd64/arm64 交叉编译。
- GitHub Actions 已在 `macos-15-intel` 与 `macos-15`（Apple Silicon）真实 macOS SDK runner 上完成原生 Objective-C/cgo build、vet、race；这证明源码可构建，不等于热键、录音、权限或 overlay 真机通过。

Linux Wayland 已有部分真机证据，但热键细粒度、自动上屏和 overlay 视觉仍未闭环；Linux X11、Windows 与 macOS 真机项尚未执行。

### P4

- 逐项终端配置编辑器已接入：热键、模式、ASR 引擎/endpoint/key/model、自动上屏、停止延迟、热词、语言。
- 交互式终端输入 API key 时关闭回显；已有 key 不输出明文。
- TUI 采用同目录临时文件 + sync + rename 原子保存，并保留注释与显式扩展（`plugin.*`/`x-*` 节、`x_` 字段）；其他未知配置和错误布尔值直接拒绝。
- watcher 监听目录并重开目标路径，支持原子替换；默认 100ms 轮询、200ms 防抖。
- watcher 回调与轮询解耦，阻塞回调不妨碍内部关闭；Close 可并发/重复/在回调内调用。
- runtime 热重载会校验配置；每个运行世代独占 Hotkey provider。旧 Voice 停止并关闭旧 provider 后才创建新世代，失败回滚也使用全新 provider，旧事件尾部不会进入新 Voice。
- doctor 在 Wayland 实际尝试打开 `/dev/input/event*`，X11 不误报 evdev；依赖提示可操作。
- TUI 日志只进系统用户缓存文件；app 装配与 P4 包的集成/race 测试已通过。

仍需在真实桌面人工确认：守护进程 + TUI 双进程热重载、终端不被日志污染、热键资源切换正确。

### P5

- 成功会话统计：次数、总/最近 Unicode 字符数、总/最近录音毫秒数、更新时间。
- Linux 默认 `$XDG_STATE_HOME/eloqi/stats.json`，否则 `~/.local/state/eloqi/stats.json`；macOS 为 `~/Library/Application Support/eloqi/stats.json`；Windows 为 `%AppData%\eloqi\stats.json`；`ELOQUI_STATE_DIR` 可覆盖父目录。
- 统计每次原子持久化，失败回滚内存；取消和失败会话不计入。
- `asr.hotwords` 是 TOML 字符串数组，去空白/去重后作为 OpenAI 兼容 prompt 发送。
- overlay controller 异步映射连接、录音、停止缓冲、等待结果、错误；idle 隐藏。
- Linux X11 用原生 Xlib 胶囊，Wayland 用 `notify-send` 回退；macOS 为 NSPanel helper；Windows 为 Win32 窗口。
- Overlay 不可用或更新失败只记 warning，不中断 voice 主流程。
- 统计、热词、controller 和 Linux Xvfb/Windows overlay 定向测试已通过；所有真实视觉验收待做。

### P6

已落地；CI 已完成远端验收，tag 发布仍待执行：

- `.github/workflows/ci.yml`：Linux/macOS/Windows build、vet、race，Linux Xvfb，golangci lint。
- `.github/workflows/release.yml`：`v*` tag 构建 Linux amd64、macOS amd64/arm64、Windows amd64，归档并生成 `SHA256SUMS`，调用 `gh release`。
- `.golangci.yml`、版本 `-ldflags -X main.appVersion=<tag>`、第三方许可证声明、双语 README、安装、CHANGELOG、配置示例和真机清单。
- Actions 均固定到已核验 commit SHA；工作流默认 `contents: read`，只有 publish job 获得 `contents: write`，且 build/publish 受四个 runner 的 verify 与 Linux lint 门禁。

关键边界：Gitee 仍是 `origin`，另有 GitHub 镜像 [`xiangfei258/eloqi`](https://github.com/xiangfei258/eloqi)。CI run [`31866448754`](https://github.com/xiangfei258/eloqi/actions/runs/31866448754) 已全绿；尚无真实 tag 发布、发布资产下载或真机共同验收证据。

---

## 5. 当前自动化验证记录

已确认：

- Linux + Xvfb：最终快照全仓 `go build ./...`、`go vet ./...`、`go test -race ./...`；Linux 全仓 golangci-lint 为 `0 issues`。
- Linux 定向：voice、app 热键世代隔离、X11/overlay race 重复。
- Windows amd64：最终快照全仓 build/vet/test；Windows 全仓 golangci-lint 为 `0 issues`。
- Windows 386：平台定向测试、checkptr 和 vet。
- Windows arm64：平台测试包和主程序交叉编译。
- macOS amd64/arm64：除 cgo-free helper 交叉编译外，GitHub `macos-15-intel` 与 `macos-15` 已实际完成原生 Objective-C/cgo build、vet、race。
- P6 静态/本地：actionlint v1.7.12 通过；仓库 11 份 Markdown 的 27 个本地链接无缺失；Linux/Windows 归档 smoke 的版本、资产、目录结构、归档内链接和 SHA-256 回验通过。
- 2026-08-15 Linux 工作区独立复核：合并至 `97478b2` 后，Linux 全仓 build/vet/`go test -race` 与 Windows amd64 交叉编译（`CGO_ENABLED=0`）通过；macOS 交叉编译因 cgo/Objective-C 依赖无法在 Linux 完成，符合预期。
- 2026-08-15 GitHub CI：run [`31866448754`](https://github.com/xiangfei258/eloqi/actions/runs/31866448754) 的 Ubuntu + Xvfb、Windows、macOS Intel、macOS Apple Silicon、Linux golangci-lint 全绿；首轮 Windows CRLF 格式失败已由 `.gitattributes` 修复。

尚未确认：

- tag 打包、Release 创建、下载和校验和核对。
- P2–P5 真机清单。

---

## 6. 构建与验证约束

所有 Go 命令先设置仓库内缓存：

```bash
export GOCACHE="$PWD/.buildcache" GOMODCACHE="$PWD/.buildcache/mod"
```

提交前最低检查：

```bash
unformatted="$(git ls-files -z --cached --others --exclude-standard -- '*.go' | xargs -0 gofmt -l)"
test -z "$unformatted" || { printf '%s\n' "$unformatted"; exit 1; }
go build ./...
go vet ./...
go test -race ./...
golangci-lint run
```

Linux 无真实显示服务时使用：

```bash
xvfb-run -a go test -race ./...
```

Windows PowerShell 缓存写法见 `INSTALL.md`。macOS 原生代码只能在带 SDK 的 macOS 上有效构建，不能用 Windows/Linux 的普通交叉编译替代。

---

## 7. 下一步（按顺序）

1. 推送测试 `v*` 预发布 tag，核对四个归档、`eloqi --version`、Release notes 和 `SHA256SUMS`。
2. 按 [docs/REAL_DEVICE_CHECKLIST.zh-CN.md](docs/REAL_DEVICE_CHECKLIST.zh-CN.md) 做 Linux Wayland、Linux X11、Windows、macOS 真机回归；当前只有 Linux Wayland 部分证据，不要代填其余“通过”。
3. 只有 tag 发布与对应真机项闭合后，才把 P3/P6 和正式发布验收标为完成。

---

## 8. 文档索引

| 文件 | 用途 |
|---|---|
| `AGENTS.md` | 代理规则、接口锁定、合规和验证要求 |
| `ELOQUI_DESIGN.md` | 产品/架构/平台设计蓝图 |
| `TASKS.md` | P0–P6 勾选状态与验收边界 |
| `README.md` / `README.zh-CN.md` | 用户入口与当前验证状态 |
| `INSTALL.md` | 三平台依赖、权限、构建、首次运行 |
| `CHANGELOG.md` | Unreleased 变更与验证边界 |
| `eloqi.toml.example` | 完整配置字段示例 |
| `docs/REAL_DEVICE_CHECKLIST.zh-CN.md` | 真实设备/桌面人工回归步骤 |

---

## 9. 不要误报的事项

- Xvfb、mock 或交叉编译不等于真机录音/热键/剪贴板/自动上屏/overlay 验收。
- macOS 原生 CI 编译不等于 macOS 真机录音、热键、权限或 overlay 通过。
- 工作流文件存在不等于 CI 全绿；应引用具体 GitHub run 作为证据。
- Release YAML 存在不等于 tag 已发布或安装包可用。
- doctor 的 warning 不代表权限已授权；必须由用户在系统设置中确认。
