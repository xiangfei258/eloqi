# Eloqui P2–P6 真机回归清单

> 当前状态：**Linux Wayland 已部分执行（2026-08-15）**，Linux X11 / macOS / Windows 未执行。
>
> 本清单由用户在真实设备上执行。mock、交叉编译、Xvfb、单元测试或远端 CI 都不能替代麦克风、全局热键、剪贴板、自动上屏、系统权限和 overlay 的人工验证。

---

## 1. 验收规则与记录模板

每个平台/桌面组合单独保存一份结果，不要把 Linux Wayland 与 X11、macOS Intel 与 Apple Silicon 合并为一次。

```text
日期：
执行人：
Eloqui commit/tag：
eloqi --version：
操作系统及版本：
CPU 架构：
桌面环境/显示协议：
ASR endpoint 类型（不要记录 key）：
ASR model：
热键/模式/stop_delay_ms/auto_type：

结果：通过 / 失败 / 未执行
失败步骤：
复现步骤：
日志路径：
截图或录屏：
备注：
```

判定要求：

- 每项只能写“通过 / 失败 / 未执行”，没有证据时保持“未执行”。
- 失败项修复后必须用同一环境重跑，不得只凭代码推断通过。
- 日志、截图和录屏不得包含 API key 或敏感转写正文。
- 一个平台只有在通用闭环、P2 状态机、P4 热重载、P5 统计/overlay 和对应平台专项全部通过后，才能标为真机通过。

### 1.1 Linux Wayland 已执行记录（2026-08-15）

```text
日期：2026-08-15
执行人：xiangchang24（用户）
Eloqui commit：39f7443（含 P2–P6）
eloqi --version：eloqi dev
操作系统：Ubuntu（Linux amd64）
桌面环境：GNOME Wayland（wayland-0）
ASR：本机 sglang-omni（Docker 容器 moss-transcribe），model=/models/MOSS-Transcribe-Diarize
热键/模式/停止延迟/上屏：Alt+Super / toggle / 800ms / auto_type=false（剪贴板模式）

结果：部分通过
通过项：通用闭环（Unicode+剪贴板+防重）、P2 停止缓冲/恢复录音/Esc 取消/R 重试、
       P4 TUI 保存+热重载+API key 不回显、P5 stats 持久化+notify-send 提示、
       doctor 七项全绿（evdev 可读 /dev/input/event3）。
未执行项：hold 模式、0ms 停止延迟、modifier+Tab 反例、锁定键、自动重复、左右修饰键、
          修饰键先松、auto_type 上屏、overlay 视觉细节（位置/缩放/颜色/点击穿透）。
备注：会话日志 session 编号跳过取消的那次（Esc 生效的旁证）。
```

---

## 2. 自动化前置条件

在对应平台真机开始前记录以下命令结果。仓库开发环境的 Go 命令必须使用 `.buildcache`。

Linux/macOS：

```bash
export GOCACHE="$PWD/.buildcache" GOMODCACHE="$PWD/.buildcache/mod"
unformatted="$(git ls-files -z --cached --others --exclude-standard -- '*.go' | xargs -0 gofmt -l)"
test -z "$unformatted" || { printf '%s\n' "$unformatted"; exit 1; }
go build ./...
go vet ./...
go test -race ./...
golangci-lint run
```

Windows PowerShell：

```powershell
$env:GOCACHE = Join-Path $PWD '.buildcache'
$env:GOMODCACHE = Join-Path $PWD '.buildcache\mod'
go build ./...
go vet ./...
go test ./...
```

macOS 必须在真实 macOS SDK 下执行原生 Objective-C/cgo 构建与测试。Windows/Linux 上的 darwin helper 交叉编译不能代替这一步。

启动前运行：

```text
eloqi --version
eloqi --doctor --config <配置文件>
```

- `[error]` 必须修复后再继续。
- `[warning]` 要逐项确认；权限 warning 只有在系统设置实际授权后才算关闭。
- `--doctor` 通过不代表麦克风、热键或上屏已经真机验证。

---

## 3. 通用端到端闭环

Linux Wayland、Linux X11、macOS、Windows 分别执行。

### 3.1 剪贴板模式

1. 设置 `output.auto_type = false`。
2. 配置 2–3 个明显可辨识的 `asr.hotwords`。
3. 启动 Eloqui，在中文、英文、emoji 可用的纯文本编辑器中准备手动粘贴。
4. 触发一次录音，朗读包含中英文和热词的句子。
5. 停止后手动粘贴，核对完整内容。
6. 确认剪贴板只更新一次，没有重复段落。
7. 再录一次不同内容，确认旧结果被替换而不是拼接。

记录：识别准确性、热词表现、停止到结果的等待时间、剪贴板 Unicode/换行是否完整。

### 3.2 自动上屏模式

1. 先确认 doctor 对自动上屏依赖没有 required error，再设 `output.auto_type = true`。
2. 在普通权限的纯文本编辑器中保持光标焦点。
3. 完成一次录音，确认文本只粘贴一次。
4. 确认 overlay 没有抢走键盘焦点。
5. 在浏览器输入框、聊天输入框各复测一次。
6. 将目标切换为受限/提权窗口时，只记录系统权限边界，不为了通过而提升 Eloqui 权限。

### 3.3 失败与退出

1. 临时使用错误 endpoint 或无效 key，确认进入 error 且提示可见。
2. 修正配置，按 R；确认使用新会话重试，没有沿用旧 Recorder/ASR 状态。
3. 在录音中、停止缓冲中、等待结果中分别测试 Escape；均不得产生文本输出。
4. 在 ASR 等待期间按主热键，确认不会创建重叠上传。
5. 按 `Ctrl+C` 正常退出；确认麦克风、热键、overlay、窗口/helper 均释放。
6. 立即再次启动，确认没有“设备占用”“热键占用”或残留窗口。

---

## 4. P2 状态机与热键

### 4.1 hold 模式

- [ ] 普通组合键：按下进入 connecting/recording，松开进入 stopping_delayed。
- [ ] 纯修饰键组合：精确组合稳定约 150ms 后才触发；增加任意额外键会取消候选。
- [ ] 快速点按：即使松开早于 ASR 连接完成，也能清理资源并回 idle。
- [ ] stopping_delayed 中重新按下：恢复原会话，不新建会话。
- [ ] 左右同类修饰键同时按住：松开一侧不会误判整组释放。
- [ ] 主修饰键先松、功能键后松：release 边沿仍正确到达。

### 4.2 toggle 模式

- [ ] 第一次按下开始，第二次按下进入停止缓冲。
- [ ] 停止缓冲中再次按下恢复原会话。
- [ ] 连续快速按 10 次后状态可预测，最多一个活动会话。
- [ ] 长按产生的系统自动重复不会生成虚假 press/release。

### 4.3 停止延迟

依次测试：

```toml
stop_delay_ms = 0
stop_delay_ms = 800
stop_delay_ms = 1500
```

- [ ] 0ms 立即进入 stopping，不被默认 800ms 覆盖。
- [ ] 800ms 能保留正常话尾，结果不截断。
- [ ] 1500ms 中重新触发会恢复录音；之后再次停止能正常收尾。
- [ ] 缓冲期间 Escape 立即取消。
- [ ] 较长录音收尾时，主热键事件和界面仍有响应。

### 4.4 防重复与错误恢复

- [ ] 使用会同时返回 final callback 和最终 HTTP 文本的测试后端，结果仍只输出一次。
- [ ] 输出后不会在 ASR Close、超时或退出时再次写入。
- [ ] R 重试成功后只输出重试会话结果。
- [ ] 错误超时后回到 idle，可重新开始普通会话。

---

## 5. P4 配置、TUI、热重载与 doctor

### 5.1 TUI 安全与保存

1. 备份配置文件。
2. 在配置中添加一个 `[plugin.example]` 扩展节、一个 `x_future_setting` 扩展字段和注释；另备一份把 `auto_type` 故意拼成 `auto_typ` 的错误配置。
3. 运行：

   ```text
   eloqi --tui --config <配置文件>
   ```

4. 逐项编辑热键、模式、engine、endpoint、API key、model、auto_type、stop delay、热词、语言。
5. 确认已有 API key 不显示明文；输入新 key 时终端不回显。
6. 保存后确认 TOML 可再次加载，显式扩展节/字段和注释仍在；错误配置必须被拒绝，且不能因默认值继续自动上屏。
7. 确认配置文件不是半写入状态，临时文件没有残留。
8. 再次运行，输入 `:cancel`；确认文件字节未变化。
9. 检查 TUI 期间终端没有混入 JSON 日志；日志只写入默认缓存路径或 `--log-file` 指定位置。

### 5.2 运行时热重载

1. 终端 A 保持 `eloqi --config <配置>` 运行。
2. 终端 B 用 TUI 修改并保存热键，确认无需重启即可使用新热键，旧热键失效。
3. 修改 mode、stop delay、auto_type、model、language、hotwords，逐项确认下一会话使用新值。
4. 用支持“临时文件 + rename”的编辑器保存，确认 watcher 仍发现变化。
5. 写入 TOML 语法错误或不支持的 `asr.engine`，确认旧配置继续工作，日志有拒绝原因。
6. 把热键改成已被另一程序占用的组合，确认应用失败后旧热键恢复。
7. 连续快速保存多次，最终运行配置应收敛到最后一份有效内容。
8. 在 reload 回调发生时退出，确认进程不会死锁。

### 5.3 doctor

- [ ] 缺少必需依赖时是 `[error]`，提示包含可执行的安装/配置动作。
- [ ] `auto_type=false` 时，缺少 wtype/xdotool 只影响可选能力，不阻止剪贴板模式。
- [ ] Wayland 会实际检查至少一个 `/dev/input/event*` 可读；权限不足时提示加入 input 组并重新登录。
- [ ] X11 不显示 evdev/input 组误报。
- [ ] macOS 指向启动 Eloqui 的终端/应用的麦克风、辅助功能/输入监控权限。
- [ ] Windows 指向“允许桌面应用访问麦克风”。

---

## 6. P5 统计与本地数据

默认路径：

- `ELOQUI_STATE_DIR/stats.json`（设置覆盖目录时）；
- Linux：`$XDG_STATE_HOME/eloqi/stats.json`，否则 `~/.local/state/eloqi/stats.json`；
- macOS：`~/Library/Application Support/eloqi/stats.json`；
- Windows：`%AppData%\eloqi\stats.json`。

步骤：

1. 备份/记录当前统计 JSON。
2. 完成两个成功会话，分别包含中文、英文和 emoji。
3. 核对：`recordings`、`total_characters`、`total_duration_ms`、`last_characters`、`last_duration_ms`、`updated_at`。
4. 字符数按 Unicode 字符核对，不按 UTF-8 字节数核对。
5. 退出并重启，再成功录音一次，确认继续累加。
6. 完成一次 Escape 取消和一次 ASR 失败，确认都不增加成功统计。
7. 确认统计 JSON 不包含转写正文或 API key。
8. 类 Unix 系统核对目录/文件仅当前用户可访问；Windows 核对文件位于当前用户配置目录。
9. 临时设置 `ELOQUI_STATE_DIR`，确认新统计只写到覆盖目录。

---

## 7. P5 overlay / 状态提示

每个平台观察：connecting、recording、stopping（停止缓冲）、waiting（等待 ASR）、error；idle 应隐藏。

- [ ] 不抢键盘焦点。
- [ ] 不拦截点击或正常输入。
- [ ] 不影响 Alt+Tab、系统菜单等系统快捷键。
- [ ] 文案、颜色、位置清楚；高 DPI/缩放、多显示器下不越界。
- [ ] 快速切换状态不会残留旧窗口/旧通知。
- [ ] 错误信息是简短单行，不泄露 API key 或完整响应正文。
- [ ] Overlay 后端故障只产生日志 warning，不中断录音/识别/输出。
- [ ] idle 和正常退出后状态提示消失。

Linux 专项：

- [ ] X11：原生无边框置顶窗口不激活、不抢焦点，DISPLAY 断开时退出不死锁。
- [ ] Wayland/GNOME：`notify-send` 同步通知可替换旧状态，并在 idle 时消失。

macOS 专项：

- [ ] NSPanel helper 启动、更新、隐藏、退出均正常，不残留子进程。

Windows 专项：

- [ ] Win32 窗口置顶、无激活、点击穿透；Unicode 错误文案正确；退出无残留窗口线程。

---

## 8. P3 平台专项

### 8.1 Linux Wayland

- [ ] evdev 遍历并读取所有相关键盘设备；不会用 EVIOCGRAB 吞键。
- [ ] Alt+Tab、Super 菜单、锁屏快捷键不受影响。
- [ ] `wl-copy`/`wl-paste` 支持 Unicode 与换行。
- [ ] `wtype` 不可用时，手动设 `auto_type=false` 后剪贴板模式可用。
- [ ] arecord 采样格式正确，停止后麦克风可被其他程序使用。

### 8.2 Linux X11

- [ ] 第二个 Eloqui 抢同一热键时得到明确错误；首实例退出后可重新注册。
- [ ] CapsLock、NumLock、ScrollLock 开关不改变主热键行为。
- [ ] 长按自动重复不产生假 release/press。
- [ ] `xclip` clipboard 行为与手动粘贴一致。
- [ ] `xdotool` 自动上屏只粘贴一次。
- [ ] X11 hotkey 与 overlay 关闭后无残留或死锁。

### 8.3 macOS Intel / Apple Silicon（分别执行）

- [ ] 真实 SDK 下 `CGO_ENABLED=1 go build ./...`、`go vet ./...`、`go test -race ./...`。
- [ ] 拒绝/授予麦克风权限时提示和恢复路径正确。
- [ ] 拒绝/授予辅助功能与输入监控权限时提示和恢复路径正确。
- [ ] CGEventTap 只观察事件，不吞系统快捷键。
- [ ] AudioQueue 16 kHz/16 bit/mono PCM，停止后释放设备。
- [ ] NSPasteboard 支持中文、emoji、换行。
- [ ] Command+V 注入一次，无 Command/V 按键残留。
- [ ] NSPanel 不抢焦点，helper 正常退出。

### 8.4 Windows

- [ ] 普通用户运行，不要求管理员权限。
- [ ] WinMM 16 kHz/16 bit/mono PCM，停止后设备可被其他应用使用。
- [ ] Unicode 剪贴板覆盖中文、emoji、换行。
- [ ] SendInput 只发送一次 Ctrl+V，无 Ctrl/V 残留。
- [ ] 低级钩子只观察并始终传给下一个钩子，Alt+Tab 不受影响。
- [ ] GetAsyncKeyState 轮询与钩子回退不会重复产生边沿。
- [ ] Win32 overlay 无激活、置顶、点击穿透。
- [ ] 退出后窗口线程、waveIn handle、hook 全部释放。

---

## 9. P6 CI 与发布验收

Gitee `origin` 已同步到 GitHub 镜像 [`xiangfei258/eloqi`](https://github.com/xiangfei258/eloqi)。以下 CI 项已由 run [`31866448754`](https://github.com/xiangfei258/eloqi/actions/runs/31866448754) 于 2026-08-15 验证。

### 9.1 CI

- [x] push 触发 Linux、Windows、macOS Intel、macOS Apple Silicon job。
- [x] 四个 runner 的 gofmt、build、vet、race 全绿。
- [x] Linux 在 Xvfb 下通过显示相关测试。
- [x] golangci-lint job 全绿。
- [x] macOS job 实际启用 cgo 并编译 Objective-C 原生文件；Intel 与 Apple Silicon 均通过。

### 9.2 测试 tag

1. 选定可删除/可废弃的预发布 tag，例如 `v0.1.0-rc.1`。
2. 推送 tag，观察 Release workflow，并确认带 `-` 的 SemVer 预发布 tag 被标为 prerelease 且不是 Latest。
3. 核对生成：

   ```text
   eloqi-linux-amd64.tar.gz
   eloqi-darwin-amd64.tar.gz
   eloqi-darwin-arm64.tar.gz
   eloqi-windows-amd64.zip
   SHA256SUMS
   ```

4. 下载四个归档，核对 SHA-256。
5. 解压后分别运行 `eloqi --version`，确认输出与 tag 完全一致。
6. 每个平台至少运行 `--doctor` 和一次最小启动。
7. 确认每个归档只有一个顶层目录，并包含 LICENSE、THIRD_PARTY_NOTICES、双语 README、INSTALL、CHANGELOG、TASKS、ELOQUI_DESIGN、配置示例与 `docs/REAL_DEVICE_CHECKLIST.zh-CN.md`；逐一点击 README 内相对链接，确认没有断链。
8. Release notes 和资产不含 API key、日志或本地配置。

---

## 10. 最终签字

```text
Linux Wayland：通过 / 失败 / 未执行
Linux X11：通过 / 失败 / 未执行
Windows amd64：通过 / 失败 / 未执行
macOS Intel：通过 / 失败 / 未执行
macOS Apple Silicon：通过 / 失败 / 未执行
GitHub CI：通过（run 31866448754，2026-08-15）
测试 tag 发布：通过 / 失败 / 未执行

遗留问题：
阻塞发布的问题：
执行人/日期：
```

只有所有目标平台、CI 和发布流程均有“通过”证据，且阻塞发布的问题为空，才允许把 P3/P6 及“正式可发布”状态标为完成。
