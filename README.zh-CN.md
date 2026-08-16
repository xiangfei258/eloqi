# Eloqui

[English](README.md) · [安装说明](INSTALL.md) · [变更记录](CHANGELOG.md) · [阶段任务](TASKS.md) · [真机清单](docs/REAL_DEVICE_CHECKLIST.zh-CN.md)

Eloqui 是用 Go 编写的跨平台桌面语音输入工具：按下全局热键开始录音，由 OpenAI 兼容的语音转文字接口完成识别，再把结果写入剪贴板或粘贴到当前聚焦的应用中。

> Eloqui 仍处于开发阶段。P2–P5 的实现、跨平台 CI 与 `v0.1.0-rc.2` 预发布流水线已经验收，但剩余真机回归尚未完成。不要把预发布当作正式可用版本；请先看下方“验证状态”。

## 主要能力

- `hold`（按住说话）与 `toggle`（按一次开始、再按一次停止）两种热键模式，支持纯修饰键组合。
- 显式 `idle → connecting → recording → stopping_delayed → stopping/error` 状态机。
- 可配置话尾延迟、Escape 取消、R 重试与单次会话防重复输出。
- OpenAI 兼容的转写接口，支持语言提示、热词和可选的说话人标记清理。
- 只写剪贴板或自动粘贴到当前焦点窗口。
- 配置文件热重载；新配置无效或热键注册失败时保留/恢复上一份可用配置。
- 终端配置编辑器、启动环境检查、结构化日志、本地统计与状态 overlay。
- Linux、macOS、Windows 后端用 Go build tags 隔离。

## 验证状态

| 范围 | 当前证据 | 仍需完成 |
|---|---|---|
| Linux | 已在 Linux + Xvfb 下通过全仓 build/vet/race 和 golangci-lint；voice、app 热重载与 X11 还做过定向重复 race。GNOME/KDE Wayland 现已新增带自动化测试的 ydotool/uinput 上屏后端，wlroots 继续使用 wtype。 | 在 Ubuntu 26.04 GNOME Wayland 真机验证新 ydotool 路径，并完成其余 Wayland/X11 麦克风、热键、剪贴板与 overlay 清单。 |
| Windows | 本机已通过 `windows/amd64` 全仓 build、vet、test 和 golangci-lint；完成定向 `386` 测试和 `arm64` 平台包/主程序交叉编译。下载的 `v0.1.0-rc.2` 还在 Windows 11 完成部分真机回归：实体 F8/真实麦克风、0ms/长按、中文/emoji/换行剪贴板与自动上屏、录音中 Escape 取消、首请求 503 后 R 重试、overlay 焦点/点击穿透/Alt+Tab，以及退出重启。 | 真实模型识别、普通用户权限、其余取消/错误/TUI 路径、高 DPI/多显示器视觉及由第二个应用接管麦克风仍待验证。 |
| macOS | 原生 Objective-C/cgo build、vet、race 已在 GitHub 托管的 macOS Intel 与 Apple Silicon runner 上通过。 | 分别完成 Intel/Apple Silicon 的权限、麦克风、热键、剪贴板、自动上屏与 overlay 真机回归。 |
| CI 与发布 | 发布源 commit `2a73fec` 的 [CI run `31868625541`](https://github.com/xiangfei258/eloqi/actions/runs/31868625541) 在四个目标 runner 与 lint 全绿。[`v0.1.0-rc.2`](https://github.com/xiangfei258/eloqi/releases/tag/v0.1.0-rc.2) 随后通过带门禁的 [Release run](https://github.com/xiangfei258/eloqi/actions/runs/31868700354)、四平台原生产物版本断言、独立归档/校验和/结构复核及文档链接检查；Windows 下载包的 `--doctor` 和最小守护进程启动也已通过。 | Ubuntu/macOS 下载包的 `--doctor`/最小启动与剩余真机清单；完成后再发布正式 `v0.1.0`。 |

自动化测试不能替代桌面真机验收。完整步骤见 [docs/REAL_DEVICE_CHECKLIST.zh-CN.md](docs/REAL_DEVICE_CHECKLIST.zh-CN.md)。

## 从源码快速开始

Eloqui 需要 Go 1.25。Linux 构建还需要 X11 开发头文件，运行时需要音频及桌面命令；各平台依赖和权限见 [INSTALL.md](INSTALL.md)。

```bash
git clone <你的-eloqi-仓库地址>
cd eloqi

export GOCACHE="$PWD/.buildcache" GOMODCACHE="$PWD/.buildcache/mod"
cp eloqi.toml.example eloqi.toml
# 继续前请编辑 endpoint、api_key、model 和输出模式。

go build -o eloqi ./cmd/eloqi
./eloqi --doctor --config ./eloqi.toml
./eloqi --config ./eloqi.toml
```

按 `Ctrl+C` 停止守护进程。正常启动也会执行与 `--doctor` 相同的环境检查；必需依赖缺失时会拒绝启动。

## 命令行

```text
eloqi [--config PATH] [--debug]
eloqi --doctor [--config PATH]
eloqi --tui [--config PATH] [--debug] [--log-file PATH]
eloqi --version
```

| 选项 | 行为 |
|---|---|
| `--config PATH` | 指定 TOML 配置文件。 |
| `--doctor` | 打印平台依赖与权限检查结果，然后退出。 |
| `--tui` | 打开终端配置编辑器，原子保存后退出；它不会同时启动语音守护进程。 |
| `--debug` | 启用 debug 级结构化日志。 |
| `--log-file PATH` | 覆盖 TUI 日志路径，主要与 `--tui` 一起使用。 |
| `-v`、`--version` | 打印发布构建注入的版本；本地构建显示 `dev`。 |

没有提供 `--config` 时，Eloqui 依次采用：`ELOQUI_CONFIG`、已存在的 `~/.config/eloqi/config.toml`、当前目录的 `./eloqi.toml`。

终端编辑器不会显示已有 API key；在交互式终端输入新 key 时会关闭回显，并保留注释及显式扩展（`[plugin.*]`/`[x-*]` section 和 `x_` 字段）。其他未知 section、未知 key 或拼错的布尔值会直接报错，不会被静默忽略。编辑器支持：

- 留空：保留当前值；
- `-`：清空允许为空的字段（包括热词）；
- `:cancel` 或 `:quit`：不保存退出。

验证热重载时，让守护进程和编辑器分别运行：

```bash
./eloqi --config ./eloqi.toml
# 另开终端：
./eloqi --tui --config ./eloqi.toml
```

watcher 监听配置所在目录，支持编辑器通过临时文件 + rename 原子替换；它会防抖、校验新快照，并在重载失败时保留或恢复上一份可用配置。

同一用户/会话只允许一个守护进程运行。第二个实例会在打开统计文件和平台设备前退出，避免重复录音、重复 ASR 计费或重复上屏；一次性的 `--doctor`、`--tui`、`--version` 不占用守护进程锁。

## 配置字段

完整注释示例见 [eloqi.toml.example](eloqi.toml.example)。

| 字段 | 含义 | 默认值 |
|---|---|---|
| `hotkey.mods` | 用 `+` 分隔的 Ctrl、Alt、Super、Shift。 | `Ctrl+Alt` |
| `hotkey.key` | F1–F24、Tab、CapsLock、方向/编辑键或 Num0–Num9；留空表示纯修饰键组合。Apple 未公开 F21–F24 的虚拟键码，因此 macOS 支持到 F20。 | `F1` |
| `hotkey.mode` | `hold` 或 `toggle`。 | `hold` |
| `hotkey.stop_delay_ms` | 停止手势后的话尾收录时长；`0` 关闭延迟。 | `800` |
| `asr.engine` | ASR 实现；目前只支持 `openai-compatible`。 | `openai-compatible` |
| `asr.endpoint` | 带 host 的绝对 `http://` 或 `https://` 音频转写地址；相对地址、其他 scheme 和 fragment 会在启动时拒绝。 | 必填 |
| `asr.api_key` | Bearer 令牌；可信的本地无鉴权端点也必须提供一个非空占位值。 | 必填 |
| `asr.model` | 后端模型名。 | `whisper-1` |
| `asr.language` | 后端可识别的可选语言提示。 | 自动检测 |
| `asr.hotwords` | TOML 字符串数组；去空白/去重后作为识别 prompt 发送，合并上限 8192 字节。 | `[]` |
| `asr.strip_diarization` | 清理兼容转写结果中完整的 `[时间][说话人]` 标记。 | `false` |
| `output.auto_type` | `true` 时粘贴到焦点应用；否则只写剪贴板。 | `true` |

GNOME/KDE Wayland 自动上屏需要单独安装 `ydotool` 包，启动其 `ydotool.service` 用户单元（由它运行 ydotoold），并具备 `/dev/uinput` 权限；Sway/wlroots 使用 `wtype`，X11 使用 `xdotool`。开启前请先运行 `eloqi --doctor` 并按 [INSTALL.md](INSTALL.md) 配置。Ubuntu 26.04 新路径仍待用户真机验收。

配置文件包含 ASR 密钥，请妥善保护。在类 Unix 系统上建议执行 `chmod 600 eloqi.toml`。

## 本地数据与隐私

Eloqui 只在本地记录成功会话的统计：录音次数、Unicode 字符数、时长以及最近一次会话数据。统计文件与正常完成日志都不保存转写正文。

统计文件路径：

- 设置 `ELOQUI_STATE_DIR` 时：`ELOQUI_STATE_DIR/stats.json`；
- Linux：`$XDG_STATE_HOME/eloqi/stats.json`，未设置时为 `~/.local/state/eloqi/stats.json`；
- macOS：`~/Library/Application Support/eloqi/stats.json`；
- Windows：`%AppData%\eloqi\stats.json`。

TUI 日志可用 `--log-file` 覆盖；默认路径通常为 Linux 的 `$XDG_CACHE_HOME/eloqi/eloqi.log`（未设置时 `~/.cache/eloqi/eloqi.log`）、macOS 的 `~/Library/Caches/eloqi/eloqi.log`、Windows 的 `%LocalAppData%\eloqi\eloqi.log`。守护进程的普通日志写到 stderr。Overlay 是可选能力，显示失败只记 warning，不会中断语音主流程。

## 开发与发布

仓库验证命令：

```bash
export GOCACHE="$PWD/.buildcache" GOMODCACHE="$PWD/.buildcache/mod"
unformatted="$(git ls-files -z --cached --others --exclude-standard -- '*.go' | xargs -0 gofmt -l)"
test -z "$unformatted" || { printf '%s\n' "$unformatted"; exit 1; }
go build ./...
go vet ./...
go test -race ./...
golangci-lint run
```

没有真实 X server 时，Linux 显示相关测试应通过 Xvfb 运行：`xvfb-run -a go test -race ./...`。

GitHub 工作流目前配置为：

- 在 Linux、Windows、macOS Intel 与 macOS Apple Silicon 上 build、vet、race test，并在 Linux 上执行 lint；
- `v*` tag 构建 Linux amd64、macOS amd64/arm64、Windows amd64 归档；
- 随 GitHub Release 发布 `SHA256SUMS`。

发布源 commit `2a73fec` 已由 [CI run `31868625541`](https://github.com/xiangfei258/eloqi/actions/runs/31868625541) 验证，tag 驱动流水线已由 [`v0.1.0-rc.2` Release run](https://github.com/xiangfei258/eloqi/actions/runs/31868700354) 验证；依赖硬件的真机验收仍是正式发布前的独立步骤。

## 许可证与原创性

Eloqui 是 MIT 许可的原创实现，详见 [LICENSE](LICENSE)；二进制再分发同时附带 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。项目只参考交接文档中所述 GPL 项目的功能思想，没有复制或逐行翻译其源码。
