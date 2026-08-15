# Eloqui installation / Eloqui 安装

[English README](README.md) · [中文 README](README.zh-CN.md) · [Example configuration](eloqi.toml.example)

Eloqui is currently distributed from source. GitHub release packaging has been configured but has not yet completed a verified tag run. Do not assume downloadable archives exist until a GitHub Release and its checksum file are visible.

Eloqui 目前以源码构建为准。GitHub 发布打包已经配置，但尚未完成一次经过验证的 tag 运行；在 GitHub Release 页面实际出现归档和校验和之前，不要假定已经有可下载的正式安装包。

## 1. Common requirements / 通用要求

- Go 1.25 or newer in the Go 1.25 series / Go 1.25 或同系列更新版本。
- An absolute `http://` or `https://` OpenAI-compatible audio-transcription endpoint with a host, a model name, and a non-empty API key / 带 host 的绝对 `http://` 或 `https://` OpenAI 兼容音频转写地址、模型名和非空 API key。
- A microphone and a graphical desktop session for real use / 真机使用需要麦克风和图形桌面会话。

Clone or open the repository, then keep Go's caches inside the checkout when working in the restricted project environment:

克隆或打开仓库；在本项目的受限开发环境中，所有 Go 命令都应把缓存放到仓库内：

```bash
export GOCACHE="$PWD/.buildcache" GOMODCACHE="$PWD/.buildcache/mod"
```

PowerShell equivalent / PowerShell 等价写法：

```powershell
$env:GOCACHE = Join-Path $PWD '.buildcache'
$env:GOMODCACHE = Join-Path $PWD '.buildcache\mod'
```

## 2. Linux

### Build dependencies / 构建依赖

Linux uses cgo and Xlib for the X11 hotkey and overlay implementation, even when the eventual runtime session is Wayland. On Debian/Ubuntu:

Linux 的 X11 热键和 overlay 使用 cgo/Xlib，因此即使最终在 Wayland 运行，构建时也需要 X11 开发包。Debian/Ubuntu 可执行：

```bash
sudo apt update
sudo apt install build-essential pkg-config libx11-dev
```

### Runtime dependencies / 运行依赖

Audio is captured with `arecord` in both Wayland and X11 sessions:

Wayland 与 X11 都通过 `arecord` 录音：

```bash
sudo apt install alsa-utils
```

For Wayland / Wayland 会话：

```bash
sudo apt install wl-clipboard wtype libnotify-bin
```

- `wl-copy` and `wl-paste` are required for clipboard access / 剪贴板需要 `wl-copy`、`wl-paste`。
- `wtype` is required only when `output.auto_type = true`; set it to `false` to use clipboard-only mode / 只有自动上屏为 `true` 时才必须安装 `wtype`；设为 `false` 可仅使用剪贴板。
- `notify-send` is an optional Wayland status fallback. If absent, voice input still works without an overlay / `notify-send` 是可选的 Wayland 状态提示回退；缺失时语音主流程仍可工作。

The Wayland hotkey backend reads `/dev/input/event*`. If `eloqi --doctor` reports that none are readable, add the current user to the `input` group and fully sign out and back in:

Wayland 热键后端读取 `/dev/input/event*`。如果 doctor 报告没有可读设备，可把当前用户加入 `input` 组，然后完整注销并重新登录：

```bash
sudo usermod -aG input "$USER"
```

Membership in `input` permits reading raw input events. Apply it only on a trusted personal system and follow your distribution's security policy.

`input` 组可读取原始输入事件，只应在可信个人设备上按发行版安全策略授予。

For X11 / X11 会话：

```bash
sudo apt install xclip xdotool
```

- `xclip` provides clipboard access / `xclip` 提供剪贴板能力。
- `xdotool` is required only for `output.auto_type = true` / `xdotool` 仅在自动上屏开启时必需。

### Build / 构建

```bash
export GOCACHE="$PWD/.buildcache" GOMODCACHE="$PWD/.buildcache/mod"
go build -o eloqi ./cmd/eloqi
```

## 3. macOS

The macOS backend uses Objective-C/cgo with Cocoa, ApplicationServices, AudioToolbox, CoreFoundation, and QuartzCore. Install Xcode Command Line Tools and build on macOS itself:

macOS 后端使用 Objective-C/cgo，并链接 Cocoa、ApplicationServices、AudioToolbox、CoreFoundation、QuartzCore。请安装 Xcode Command Line Tools，并在 macOS 真机上构建：

```bash
xcode-select --install
export GOCACHE="$PWD/.buildcache" GOMODCACHE="$PWD/.buildcache/mod"
CGO_ENABLED=1 go build -o eloqi ./cmd/eloqi
```

No external clipboard or audio command is required. On first use, grant permissions to the terminal or app that launches Eloqui:

不需要外部剪贴板或录音命令。首次使用时，请给“启动 Eloqui 的终端或应用”授予：

- Microphone / 麦克风权限；
- Accessibility / 辅助功能权限；
- Input Monitoring if macOS requests it for the CGEventTap hotkey / 若系统为 CGEventTap 热键请求，则授予输入监控权限。

The native macOS bridge has not yet been compiled in this development cycle with a real macOS SDK. Treat the command above as the required acceptance step, not a previously confirmed result.

本轮开发尚未用真实 macOS SDK 编译原生桥；上面的命令属于待执行验收步骤，不是已经确认的结果。

## 4. Windows

Windows uses native Win32/WinMM APIs and does not require cgo, ffmpeg, SoX, or administrator rights.

Windows 使用原生 Win32/WinMM API，不需要 cgo、ffmpeg、SoX 或管理员权限。

```powershell
$env:GOCACHE = Join-Path $PWD '.buildcache'
$env:GOMODCACHE = Join-Path $PWD '.buildcache\mod'
go build -o eloqi.exe ./cmd/eloqi
```

In Windows Settings, allow desktop applications to use the microphone. Accessibility-style permission is not normally required, but elevated target applications may reject input from a non-elevated Eloqui process due to Windows integrity boundaries.

请在 Windows 设置中允许桌面应用访问麦克风。通常不需要辅助功能类权限；但受 Windows 完整性级别限制，普通权限的 Eloqui 可能无法向管理员权限窗口注入输入。

## 5. Configuration / 配置

Copy the annotated example and edit it before normal startup:

复制带注释的示例，并在正常启动前编辑：

```bash
cp eloqi.toml.example eloqi.toml
```

At minimum, set:

至少需要设置：

```toml
[asr]
engine = "openai-compatible"
endpoint = "https://api.openai.com/v1/audio/transcriptions"
api_key = "replace-me"
model = "whisper-1"

[output]
auto_type = false
```

Clipboard-only mode (`auto_type = false`) is the safest first run. Enable automatic typing after `--doctor` confirms its dependency and after verifying the focused target application.

首次运行建议用剪贴板模式（`auto_type = false`）。等 doctor 确认自动上屏依赖，并确认当前焦点应用后，再开启自动上屏。

Configuration resolution order / 配置查找顺序：

1. `--config PATH`;
2. `ELOQUI_CONFIG`;
3. an existing `~/.config/eloqi/config.toml` / 已存在的该文件；
4. `./eloqi.toml`.

The file contains an API credential. On Unix-like systems, use `chmod 600 <path>` and do not commit a real key.

配置文件包含 API 凭据；类 Unix 系统请设为 `chmod 600 <路径>`，不要把真实 key 提交进 Git。

## 6. First run / 首次运行

Run the environment check first:

先运行环境检查：

```bash
./eloqi --doctor --config ./eloqi.toml
```

PowerShell / PowerShell：

```powershell
.\eloqi.exe --doctor --config .\eloqi.toml
```

An `[error]` finding blocks normal startup. An `[warning]` finding describes an optional capability or permission that must be confirmed manually.

`[error]` 会阻止正常启动；`[warning]` 表示可选能力或需要人工确认的权限。

Start the daemon / 启动守护进程：

```bash
./eloqi --config ./eloqi.toml
```

Open the terminal editor and exit after saving / 打开终端编辑器，保存后退出：

```bash
./eloqi --tui --config ./eloqi.toml
```

To exercise hot reload, keep the daemon running in one terminal and run the editor in another. Invalid new configuration is rejected; a runtime registration failure attempts to restore the previous working configuration.

验证热重载时，守护进程与编辑器应分别运行在两个终端。无效配置会被拒绝；运行期注册失败时会尝试恢复上一份可用配置。

## 7. Runtime files / 运行时文件

Statistics / 统计：

- override / 覆盖路径：`ELOQUI_STATE_DIR/stats.json`；
- Linux：`$XDG_STATE_HOME/eloqi/stats.json`，或 `~/.local/state/eloqi/stats.json`；
- macOS：`~/Library/Application Support/eloqi/stats.json`；
- Windows：`%AppData%\eloqi\stats.json`。

Only successful sessions are counted. Transcript text is not stored in the statistics JSON.

只有成功会话会计入统计；统计 JSON 不保存转写正文。

TUI log / TUI 日志（可用 `--log-file` 覆盖）：

- Linux：`$XDG_CACHE_HOME/eloqi/eloqi.log`，未设置时通常为 `~/.cache/eloqi/eloqi.log`；
- macOS：`~/Library/Caches/eloqi/eloqi.log`；
- Windows：`%LocalAppData%\eloqi\eloqi.log`。

## 8. Developer verification / 开发验证

Before committing, run all checks on a supported native host. Linux without a display should wrap the race test with Xvfb:

提交前，应在受支持的原生系统执行全部检查。Linux 没有显示服务时，用 Xvfb 包裹 race 测试：

```bash
export GOCACHE="$PWD/.buildcache" GOMODCACHE="$PWD/.buildcache/mod"
unformatted="$(git ls-files -z --cached --others --exclude-standard -- '*.go' | xargs -0 gofmt -l)"
test -z "$unformatted" || { printf '%s\n' "$unformatted"; exit 1; }
go build ./...
go vet ./...
xvfb-run -a go test -race ./...
golangci-lint run
```

macOS must run its own cgo build/tests with a real SDK. Real microphones, global hotkeys, clipboard injection, automatic typing, permissions, and overlays must then follow [docs/REAL_DEVICE_CHECKLIST.zh-CN.md](docs/REAL_DEVICE_CHECKLIST.zh-CN.md).

macOS 必须用真实 SDK 执行本机 cgo 构建/测试；麦克风、全局热键、剪贴板注入、自动上屏、权限与 overlay 还必须按真机清单验收。

## 9. Release archives / 发布归档

The prepared GitHub workflow reacts to `v*` tags and is intended to create:

当前 GitHub 工作流在 `v*` tag 时计划生成：

- `eloqi-linux-amd64.tar.gz`;
- `eloqi-darwin-amd64.tar.gz`;
- `eloqi-darwin-arm64.tar.gz`;
- `eloqi-windows-amd64.zip`;
- `SHA256SUMS`.

Each platform archive has one top-level directory and preserves `docs/`; it includes the binary, configuration example, LICENSE, THIRD_PARTY_NOTICES, both READMEs, INSTALL, CHANGELOG, TASKS, ELOQUI_DESIGN, and the real-device checklist.

每个平台归档都保留一个顶层目录和 `docs/` 层级，包含二进制、配置示例、LICENSE、THIRD_PARTY_NOTICES、双语 README、INSTALL、CHANGELOG、TASKS、ELOQUI_DESIGN 与真机清单。

The current repository remote is Gitee, so these GitHub Actions do not run there. A GitHub repository or mirror, a green CI run, a real tag publication, checksum verification, and the manual device checklist are all still required before calling the release pipeline accepted.

当前仓库远端是 Gitee，无法直接运行这些 GitHub Actions。发布流水线要验收，仍需 GitHub 仓库或镜像、绿色 CI、真实 tag 发布、校验和核对以及真机回归。
