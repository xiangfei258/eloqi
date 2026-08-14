# Eloqui 实现任务清单

> 配套文档：ELOQUI_DESIGN.md（设计蓝图）。本文档把蓝图拆成可交付的分阶段任务，
> 每个任务都带验收标准。执行时按 P0 → P1 → … 顺序，一个阶段验收通过再进下一个。
>
> 原则：**只参考设计思想，代码用自己的表达重新编写**。目录结构、包名、命名风格
> 均自行设计，保持原创性。

**已填充**：GitHub 用户名为 xiangchang24（module path 用）。

---

## P0 — 项目初始化（脚手架）

目标：一个干净的、可编译的 Go 项目骨架。

- [ ] `git init`，创建 `.gitignore`（忽略 build 产物、二进制、日志、本地配置）。
- [ ] `go mod init github.com/xiangchang24/eloqi`。
- [ ] 写 MIT `LICENSE` 文件。
- [ ] 建目录骨架：`cmd/eloqi/`（入口）、`internal/`（平台能力层）、`plugins/`（业务插件）。
- [ ] `cmd/eloqi/main.go` 写一个最小可运行入口（打印版本、退出）。
- [ ] `README.md` 骨架：项目简介、状态、待办。
- [ ] 首次提交。

**验收标准**：
- `go build ./...` 通过。
- `go vet ./...` 无输出。
- `git log` 有初始提交，工作区干净。

---

## P1 — 平台能力层接口 + Linux 最小闭环

目标：在 Linux 上跑通“按下热键 → 录音 → ASR 识别 → 输出文本”的最小闭环。

### P1.1 定义平台能力接口（纯抽象，无平台实现）
- [x] 定义 recorder 接口：启动、读音频、停止（返回剩余数据）。
- [x] 定义 ASR 接口：连接、发送音频、结果回调、最终文本、关闭。
- [x] 定义 clipboard 接口：读取、写入。
- [x] 定义 autotype 接口：把文本注入当前焦点窗口。
- [x] 定义 hotkey 接口：注册/注销热键、事件回调（按下/抬起边沿）。
- [x] 每个接口配一个 mock 实现，供后续测试用。

> 接口与 mock 位于 `internal/platform/`（接口）与 `internal/platform/mock/`（实现），
> 单元测试覆盖 `go test -race ./...` 通过。

### P1.2 Linux 平台实现
- [x] recorder：调外部 `arecord`（ALSA，16kHz/16bit/mono raw PCM）。
- [x] ASR：实现一个 OpenAI 兼容的非流式实现（录完上传，返回文本）。
- [x] clipboard：`wl-copy`/`wl-paste`（Wayland）、`xclip`（X11）。
- [x] autotype：写剪贴板后模拟粘贴（Wayland 用 wtype，X11 用 xdotool）。
- [x] hotkey：Wayland 走 evdev，X11 走原生 X11（cgo + Xlib）。

### P1.3 串起来（最小闭环）
- [x] 一个极简 voice 逻辑：热键按下开始录音、再按停止、识别后写剪贴板/上屏。
- [x] 配置项先用最简单形式（热键、ASR 地址/密钥，TOML）。

**验收标准**：
- [ ] 在 Linux（先 Wayland 或 X11 其一）真机验证：按热键说话，松开后文本出现在焦点窗口。
- [x] 核心逻辑（状态流转）有单元测试，`go test -race ./...` 通过。

> 代码已完成并提交（见 `internal/platform/linux/`、`internal/asr/`、`internal/config/`、
> `internal/voice/`、`internal/app/`）。**真机验证未做**：录音/热键/剪贴板/上屏依赖真实
> 设备与显示服务器，需人工按下方清单验证后再视为 P1 关闭。

---

## P2 — 显式状态机 + 关键健壮性（重点）

目标：把“快速连按、hold 快松、错误重试”这些易错场景做稳。

- [ ] 引入显式录音状态机：idle / connecting / recording / stopping_delayed / stopping / error。
- [ ] 支持 toggle 和 hold 两种模式，并区分修饰键-only 与功能键热键。
- [ ] 实现停止延迟缓冲（默认约 800ms，可配置），缓冲期间再次按下取消停止。
- [ ] 实现双输出防重：一次会话最终文本只输出一次。
- [ ] 停止动作异步化：热键回调不阻塞，收尾（停录音、发最后音频、等结果、输出）走后台。
- [ ] 出错状态保持一段时间，可重试；录音/收尾期间支持 Esc 取消。
- [ ] 为状态机的全部合法转移写单元测试（table-driven）。

**验收标准**：
- 状态机所有转移有测试覆盖。
- 快速连按 10 次、hold 快速点按，不崩溃、不重复输出、最终回到 idle。
- `go test -race ./...` 通过。

---

## P3 — 补齐 macOS 和 Windows

目标：三平台能力全覆盖（按 ELOQUI_DESIGN.md 第 4 节平台矩阵实现）。

- [ ] macOS：CGEventTap 热键、CoreAudio 录音、NSPasteboard 剪贴板、原生键盘注入、NSPanel 胶囊。
- [ ] Windows：热键轮询 + 低级钩子、WinMM 录音、Unicode 剪贴板、SendInput 上屏、Win32 胶囊。
- [ ] 各平台用 build tags 隔离，共用接口不变。

**验收标准**：
- 三平台各自构建通过（macOS 需在 mac 上 cgo 构建）。
- 每平台至少真机验证一次“录音→识别→输出”闭环。

---

## P4 — 产品化：TUI + 配置热重载 + doctor

- [ ] TUI 配置界面（热键、模式、ASR 引擎/密钥、自动上屏、停止延迟、热词、语言）。
- [ ] 配置热重载（监听目录，配置变化即时生效）。
- [ ] doctor 启动环境检查：检测缺失依赖并给出可操作的修复提示。
- [ ] TUI 模式日志进文件、不污染界面。

**验收标准**：
- 在 TUI 里改配置保存后立即生效，无需重启。
- doctor 在缺依赖时给出明确、可执行的提示。

---

## P5 — 体验完善

- [ ] 录音历史统计（次数、字数、时长）并持久化。
- [ ] 热词增强识别。
- [ ] 录音状态胶囊（overlay）在各平台打磨（位置、缩放、颜色）。
- [ ] 错误提示与重试交互完善。

**验收标准**：
- 统计数据跨重启保留。
- 胶囊在录音全流程状态清晰可见。

---

## P6 — 工程化与发布

- [ ] CI：三平台矩阵（build + test -race + lint）。
- [ ] 引入 golangci-lint，配置合理的 linter 集合。
- [ ] 发布打包（各平台归档 + 校验和）。
- [ ] README 双语、CHANGELOG、安装说明完善。

**验收标准**：
- 推 tag 能自动出三平台安装包。
- CI 全绿。
