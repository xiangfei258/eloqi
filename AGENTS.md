# AGENTS.md

Eloqui 项目级开发指令。任何 AI 编码代理（codex / Claude Code 等）在本仓库工作时，必须遵守本文档。

---

## 这是什么项目

Eloqui：跨平台桌面语音输入工具（全局热键 → 录音 → ASR 识别 → 写剪贴板或自动上屏）。

- 语言：Go
- module：`github.com/xiangchang24/eloqi`
- 许可证：MIT（版权行 `Copyright (c) 2026 xiangchang24`）
- 目标平台：Linux（Wayland + X11）、macOS、Windows

---

## 接上下文：先读这些（按顺序，读完再动手）

1. `ELOQUI_DESIGN.md` —— 设计蓝图。重点看第 3 节「分层」、第 4 节「平台矩阵」、第 5 节「关键设计决策」。
2. `TASKS.md` —— 阶段任务与验收标准，逐条对照。
3. `HANDOFF.md` —— 最新进度 + 环境注意事项，**以它为准**。
4. 相关源码与测试。

---

## 合规红线（最高优先级，不可违背）

Eloqui 是「参考 GPL v3 项目 just-talk-go 的功能思想」从零重写的 MIT 原创项目，不是翻译、不是复制。

- **禁止查看、复制、逐行翻译 `/home/xiangchanglin/projects/just-talk-go` 的任何代码。**
- 目录结构、包名、函数名、注释、实现细节都必须用自己的表达重新设计。
- 该参考项目的后台进程已停止，目录保留仅作参照；**仍不得读写、引用它的任何源码文件。**

---

## 环境：构建命令必须加缓存前缀

本机 Go 默认 `GOCACHE` / `GOMODCACHE` 位于 `/home/xiangchanglin/...`，在受限 shell 下只读。**所有 go 命令前先设置：**

```bash
export GOCACHE="$PWD/.buildcache" GOMODCACHE="$PWD/.buildcache/mod"
```

（`.buildcache/` 已在 `.gitignore` 中。）

提交前验证命令（三样都要过）：

```bash
go build ./... && go vet ./... && go test -race ./...
```

---

## 已锁定的平台接口（不得改名、不得加方法、不得删字段）

实现新的平台能力时，必须满足 `internal/platform/` 下已定义的接口（签名已锁定）：

- `Recorder`：`Start() error` / `Read(p []byte) (int, error)` / `Stop() ([]byte, error)` / `Close() error`
- `ASRClient`：`Connect() error` / `Send(audio []byte) error` / `SetResultHandler(h ResultHandler)` / `Finalize() (string, error)` / `Close() error`
  - 类型：`ASRResult{ Text string; Final bool }`、`ResultHandler func(ASRResult)`
- `Clipboard`：`Read() (string, error)` / `Write(text string) error`
- `Autotype`：`Type(text string) error`
- `Hotkey`：`Register(key Key) error` / `Unregister(key Key) error` / `Events() <-chan KeyEvent` / `Close() error`
  - 类型：`Key{ Mods Modifiers; Code KeyCode }`、`KeyEvent{ Key Key; Pressed bool }`

---

## 平台差异

- 用 `//go:build` 标签把平台实现隔离到独立文件（`linux` / `darwin` / `windows`）。
- **Wayland 与 X11 是运行时选择**（探测 `$WAYLAND_DISPLAY` / `$DISPLAY`），不是编译期选择；录音用 `arecord`，与显示服务器无关。
- 每个平台能力都应配一个 mock 实现（见 `internal/platform/mock/`），供跨平台测试核心逻辑。

---

## 提交规范

- 每完成一个可验证的小步单独 commit，message 用 conventional commits（`feat:` / `chore:` / `fix:` 等）。
- 提交前确保 build + vet + test -race 全绿、仅针对仓库 Go 源码的格式检查无输出、工作区干净。不要让 `gofmt` 递归扫描 `.buildcache/mod`：

  ```bash
  unformatted="$(git ls-files -z --cached --others --exclude-standard -- '*.go' | xargs -0 gofmt -l)"
  test -z "$unformatted" || { printf '%s\n' "$unformatted"; exit 1; }
  ```

---

## 可验证 vs 需真机验证

- **必须写单测**：核心逻辑（状态机、双输出去重、配置解析、ASR HTTP 客户端），用 mock + table-driven + `go test -race`。
- **需真机验证**：录音、全局热键、剪贴板、自动上屏、overlay 依赖真实设备或显示服务器。编译 + 单测通过后，交付一份「真机验证清单」，由人工验证——**不要假装已经真机验证过。**
