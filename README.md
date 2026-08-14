# Eloqui

跨平台桌面语音输入工具（开发中）。

按下全局热键说话，语音被转成文字，自动写入剪贴板或直接「上屏」到当前焦点窗口。

## 状态

- [x] P0 项目骨架
- [ ] P1 Linux 最小闭环（代码已完成，待真机验证）
- [ ] P2 状态机与健壮性
- [ ] P3 macOS / Windows
- [ ] P4 TUI + 配置热重载 + doctor
- [ ] P5 体验完善
- [ ] P6 工程化与发布

## 构建

```bash
export GOCACHE="$PWD/.buildcache" GOMODCACHE="$PWD/.buildcache/mod"  # 本机缓存目录只读时需加
go build ./...
```

## 运行（Linux 真机验证）

1. 安装依赖（按桌面环境选）：
   - 录音：`arecord`（alsa-utils）
   - Wayland 剪贴板：`wl-clipboard`；X11：`xclip`
   - 自动上屏：Wayland 用 `wtype`，X11 用 `xdotool`
   - Wayland 热键：把当前用户加入 `input` 组后重新登录（否则打不开 `/dev/input/event*`）
   - X11 热键（cgo 构建）：`libx11-dev`
2. 准备配置：`cp eloqi.toml.example ~/.config/eloqi/config.toml`，填好 `[asr]` 的
   endpoint / api_key / model。
3. 运行：

   ```bash
   go run ./cmd/eloqi --config ~/.config/eloqi/config.toml
   ```

4. 按热键（默认 Ctrl+Alt+F1，hold 模式按住说话），松开后识别结果写入剪贴板或上屏。
   按 Ctrl+C 退出。

## 测试

```bash
go test -race ./...
```

## 许可证

MIT
