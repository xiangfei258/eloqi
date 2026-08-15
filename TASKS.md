# Eloqui 实现任务清单

> 配套文档：[ELOQUI_DESIGN.md](ELOQUI_DESIGN.md)（设计蓝图）。本文把蓝图拆成 P0–P6 可交付阶段。
>
> 勾选规则：只有“代码/文档已落地，并有本地自动化、可重复静态证据或可追溯的远端运行证据”才标为 `[x]`。真机、tag 发布等未执行项即使已有代码或工作流，也保持 `[ ]`，避免把实现完成误写成验收完成。

模块路径：`github.com/xiangchang24/eloqi`。

---

## P0 — 项目初始化（脚手架）

目标：一个干净、可编译、有原创 MIT 许可的 Go 项目骨架。

- [x] 初始化 Git，并配置 `.gitignore`。
- [x] 初始化 Go module。
- [x] 添加 MIT `LICENSE`。
- [x] 建立 `cmd/eloqi/`、`internal/` 等目录骨架。
- [x] 添加可运行入口和版本输出。
- [x] 建立 README 与项目文档。
- [x] 建立初始提交历史。

**自动化/静态验收**：

- [x] 仓库有可追溯提交，许可证和 module path 与项目约定一致。
- [x] P0 后续已被 P1–P6 全仓构建覆盖。

---

## P1 — 平台能力接口 + Linux 最小闭环

目标：在 Linux 上跑通“热键 → 录音 → ASR → 剪贴板/上屏”最小闭环。

### P1.1 平台接口与 mock

- [x] Recorder：启动、读取、停止并返回尾音频、关闭。
- [x] ASRClient：连接、发送、结果回调、收尾、关闭。
- [x] Clipboard、Autotype、Hotkey 接口及事件/按键类型。
- [x] `internal/platform/mock/` 对应 mock 与单元测试。

### P1.2 Linux 后端

- [x] `arecord`：16 kHz、16 bit、单声道 raw PCM。
- [x] OpenAI 兼容非流式 ASR 客户端。
- [x] Wayland `wl-copy`/`wl-paste` 与 X11 `xclip`。
- [x] Wayland `wtype` 与 X11 `xdotool` 自动上屏。
- [x] Wayland evdev 与原生 X11 全局热键。

### P1.3 最小闭环与加固

- [x] voice 最小闭环、TOML 配置和应用装配。
- [x] evdev release 配对、modifier-only 观察窗、X11 锁定修饰键变体和自动重复加固。
- [x] arecord 有界缓冲、阻塞 Read 唤醒、尾音频和错误传播。
- [x] diarization 清理改为显式选择且只匹配完整结构。

**验收状态**：

- [x] 2026-08-14 在 GNOME Wayland 完成 P1 原始闭环真机验证：`Alt+Super` toggle、本机兼容 ASR、剪贴板输出。
- [x] Linux 自动化 build/vet/race 已覆盖当前 P1 代码。
- [x] P1 热键复测（Linux Wayland 2026-08-15）：toggle + modifier-only（`Alt+Super`）闭环通过；hold、modifier+Tab 反例、锁定键、自动重复、左右修饰键、修饰键先松仍需专项复测。

> GNOME Wayland 已知边界：`wtype` 在部分桌面不可用；此时使用 `output.auto_type = false`，转写只进入剪贴板。

---

## P2 — 显式状态机 + 关键健壮性

目标：稳定处理快速连按、hold 快松、收尾竞态、取消和重试。

- [x] 显式状态机：idle / connecting / recording / stopping_delayed / stopping / error。
- [x] toggle、hold、modifier-only 与功能键交互。
- [x] 默认 800ms、可配置且可显式设为 0 的停止延迟；缓冲期间可恢复录音。
- [x] 会话 ID + 原子认领，保证最终文本只输出一次。
- [x] 录音启动、停止、尾音频发送、Finalize、Close 与输出从热键事件循环异步解耦。
- [x] 活动态 Escape 取消；错误态 R 用全新 Recorder/ASR 重试；错误超时回 idle。
- [x] 状态和会话钩子使用有序无界分发，`Stop` 返回前排空回调。
- [x] 合法/非法转移、快速连按、快松、取消、重试、防重和回调背压单元测试。

**自动化验收**：

- [x] `internal/voice` 在 Linux 下定向 `go test -race -count=3` 通过。
- [x] 快速连按 10 次与 hold 快速点按测试最终回 idle，且无重复输出。
- [x] Linux + Xvfb 全仓 race 对当前 P2 代码通过。
- [x] P2 真机回归（Linux Wayland 2026-08-15）：toggle 停止缓冲 800ms 不截断、缓冲期恢复录音、Esc 取消、R 重试、防重、notify-send 错误提示、退出后重进无占用均通过；hold 模式与 0ms 延迟待专项复测。

---

## P3 — macOS 与 Windows 平台能力

目标：按 build tags 补齐三平台能力，同时保持已锁定接口不变。

- [x] macOS：CGEventTap、AudioQueue、NSPasteboard、Command+V 注入、NSPanel helper 的源码已实现，并在 macOS Intel/Apple Silicon 托管 runner 上完成原生 Objective-C/cgo build、vet、race；真机能力仍单独验收。
- [x] Windows：GetAsyncKeyState + 观察式低级钩子、WinMM、Unicode 剪贴板、SendInput、非激活 Win32 overlay 已实现并通过本地自动化。
- [x] Linux / macOS / Windows 后端通过 build tags 隔离，锁定平台接口未改名、未加方法、未删字段。
- [x] Windows Unicode、音频格式/缓冲、热键边沿、原生符号和 overlay 状态测试。
- [x] macOS 不依赖 cgo 的热键/overlay 状态辅助测试可为 amd64 与 arm64 交叉编译。

**自动化/构建验收**：

- [x] Linux 全仓 build/vet/race（Xvfb）通过。
- [x] Windows amd64 全仓 build/vet/test 通过；定向 386 测试和 arm64 平台包/主程序交叉编译通过；WinMM 所有权失败、停止唤醒失败和有界 quarantine 路径已做重复故障注入与 checkptr 验证。
- [x] macOS Intel 原生 Objective-C/cgo build、vet、race：GitHub Actions `macos-15-intel` 通过（run [`31866448754`](https://github.com/xiangfei258/eloqi/actions/runs/31866448754)）。
- [x] macOS Apple Silicon 原生 Objective-C/cgo build、vet、race：GitHub Actions `macos-15` 通过（run [`31866448754`](https://github.com/xiangfei258/eloqi/actions/runs/31866448754)）。

**真机验收**：

- [x] Linux Wayland（2026-08-15，剪贴板模式；auto_type 受 GNOME 限制）。
- [ ] Linux X11。
- [ ] macOS Intel。
- [ ] macOS Apple Silicon。
- [ ] Windows（2026-08-15 已完成下载版 rc.2 的部分真机回归：实体 F8/麦克风、0ms 与长按、防重复、剪贴板/自动上屏、overlay 点击穿透/Alt+Tab、退出与重启；真实模型、普通用户、Unicode/权限及其余清单仍待执行）。

---

## P4 — 产品化：TUI + 热重载 + doctor

- [x] 终端配置编辑器覆盖热键、模式、ASR 引擎/密钥、自动上屏、停止延迟、热词和语言。
- [x] 交互式 API key 输入关闭终端回显；已有 key 只显示“已配置”状态。
- [x] TUI 原子保存，并保留注释与显式扩展 TOML 节/字段（`plugin.*`/`x-*`、`x_`）；其他未知项拒绝。
- [x] 目录级配置 watcher：轮询、内容摘要、防抖、校验、异步有界回调与安全 Close。
- [x] 应用运行时热重载；每个运行世代独占 Hotkey provider，新配置无效或新热键注册失败时用全新 provider 恢复旧配置。
- [x] `--doctor` 检查 Linux 依赖、Wayland evdev 实际可读性与 macOS/Windows 权限提示。
- [x] TUI 结构化日志只写用户缓存文件，不污染交互界面。
- [x] CLI 与 app 装配测试覆盖 TUI、doctor、正常运行、热重载和退出清理。

**自动化验收**：

- [x] `internal/config` watcher 在阻塞回调、自 Close、并发 Close 和原子 rename 场景下通过 race 测试。
- [x] `internal/doctor` 对 Wayland/X11 分支、依赖缺失和 evdev 权限提示有单元测试。
- [x] `internal/tui` 覆盖完整字段 round-trip、取消、密钥保护、原子替换和显式扩展字段保留。
- [x] app 集成测试覆盖重载失败回滚、上一份配置继续工作，以及旧 provider 的尾部事件不会被新世代消费。
- [x] Linux Wayland 真机（2026-08-15）：守护进程 + TUI 双进程热重载即时生效（日志 `configuration reloaded`）、TUI API key 不回显、终端无日志污染。

---

## P5 — 体验完善

- [x] 本地统计：成功录音次数、总/最近 Unicode 字符数、总/最近时长和更新时间。
- [x] 统计采用同目录临时文件 + sync + rename；持久化失败时回滚内存值。
- [x] ASR 热词经规范化/去重后作为 OpenAI 兼容 prompt 发送。
- [x] Overlay 抽象、mock 和异步 controller；状态更新不阻塞 voice 主流程。
- [x] Linux X11 原生胶囊、Wayland `notify-send` 回退、macOS NSPanel 和 Windows Win32 后端源码。
- [x] 错误 overlay、R 重试、Escape 取消与取消/失败会话不计统计。

**自动化验收**：

- [x] 统计重载、并发更新、Unicode 字符计数、损坏文件、失败回滚和平台路径测试。
- [x] 热词 prompt、去空白和去重测试。
- [x] Overlay 状态映射、去重、背压、关闭及 Linux Xvfb/Windows 平台定向测试。
- [x] Linux Wayland 真机（2026-08-15）：notify-send 状态提示可用、录音/取消/失败语义正确；位置/缩放/颜色等视觉细节与点击穿透仍需专项验收。
- [x] Linux Wayland 真机（2026-08-15）：`stats.json` 跨会话持久化正常（recordings/字数/时长字段正确）。

---

## P6 — 工程化与发布

- [x] GitHub 镜像 [`xiangfei258/eloqi`](https://github.com/xiangfei258/eloqi) 已建立；发布源 commit `2a73fec` 的 Linux、Windows、macOS Intel、macOS Apple Silicon build/vet/race 与 Linux lint 在 run [`31868625541`](https://github.com/xiangfei258/eloqi/actions/runs/31868625541) 全绿。
- [x] `.golangci.yml` 已添加；Linux 与 Windows 本地全仓 `golangci-lint` 均为 `0 issues`，GitHub lint job 亦已通过。
- [x] `v0.1.0-rc.2` 已真实触发 Linux amd64、macOS amd64/arm64、Windows amd64 打包与 `SHA256SUMS` 发布；Release run [`31868700354`](https://github.com/xiangfei258/eloqi/actions/runs/31868700354) 全绿。
- [x] `README.md` / `README.zh-CN.md`、`INSTALL.md`、`CHANGELOG.md`、配置示例、交接和真机清单已整理。
- [x] 发布构建支持通过 `-ldflags -X main.appVersion=<tag>` 注入版本，本地构建显示 `dev`。
- [x] Linux `tar.gz` 与 Windows `zip` 本地发布 smoke 通过：版本注入、单一顶层目录、必需资产、文档相对链接和 SHA-256 回验均正确。

**发布验收**：

- [x] 仓库已同步到 GitHub，发布源 commit `2a73fec` 的四个目标 runner 与 Linux lint 全绿（run [`31868625541`](https://github.com/xiangfei258/eloqi/actions/runs/31868625541)）。
- [x] 测试 tag [`v0.1.0-rc.2`](https://github.com/xiangfei258/eloqi/releases/tag/v0.1.0-rc.2) 已以 prerelease、非 Latest 发布；四个归档、Release notes 和 `SHA256SUMS` 均生成。
- [x] 四个归档已独立下载；`SHA256SUMS` 四项全匹配，四个原生 build job 均实际执行并断言 `eloqi --version`，Windows/Linux 下载产物也复跑为 `eloqi v0.1.0-rc.2`；每包 11 个必需文件且 26 个本地文档链接无缺失。
- [ ] 从下载归档在各目标真机执行 `--doctor` 和最小守护进程启动；Windows 两项均已通过，Ubuntu/macOS 仍待执行。
- [ ] 完成 [docs/REAL_DEVICE_CHECKLIST.zh-CN.md](docs/REAL_DEVICE_CHECKLIST.zh-CN.md) 对应平台项。

---

## 当前结论（2026-08-15）

- P0、P1、P2 的代码与既有自动化证据已闭合；Linux Wayland 主流程已有部分真机证据，P1 加固后的细粒度热键专项与其余 P2 真机项仍待执行。
- P3 的 Windows 自动化证据与 macOS Intel/Apple Silicon 原生 SDK 构建证据已具备；Windows 已有部分真机证据，但真实模型、权限、Unicode 与完整资源释放等仍是验收阻塞项。
- P4、P5 的实现和自动化覆盖已落地；Windows 已验证直接配置热重载、统计、剪贴板/自动上屏及 overlay 焦点/点击穿透，TUI 双进程、错误路径和其余平台视觉仍待执行。
- P6 工作流、远端 CI、测试 tag、四平台归档、版本注入、下载后校验和/结构复核已闭合；Windows 下载包的 `--doctor` 与最小启动已通过，Ubuntu/macOS 及完整硬件验收仍未闭合，因此尚不发布正式 `v0.1.0`。
