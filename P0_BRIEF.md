# P0 超详细执行简报（给执行者）

> 本文件是 TASKS.md 中 P0 的展开版，含具体命令和文件内容建议。
> 按顺序逐条执行，最后对照验收标准自检。
>
> 占位符已填充：用户名 xiangchang24，年份 2026，版权持有人 xiangchang24。

---

## 步骤 1：初始化 git

```bash
cd /home/xiangchanglin/projects/eloqi
git init
```

## 步骤 2：创建 .gitignore

新建 `.gitignore`，内容：

```gitignore
# Build outputs
/build/
/dist/
*.exe

# Local runtime files
*.log
*.tmp

# Local configuration and state
config.toml
.env

# Editor and OS noise
.DS_Store
Thumbs.db
```

## 步骤 3：创建 MIT LICENSE

新建 `LICENSE`，内容：

```text
MIT License

Copyright (c) 2026 xiangchang24

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## 步骤 4：初始化 Go module

```bash
go mod init github.com/xiangchang24/eloqi
```

## 步骤 5：建目录骨架 + 最小入口

```bash
mkdir -p cmd/eloqi internal plugins
```

新建 `cmd/eloqi/main.go`，内容（最小可运行入口，后续阶段再扩展）：

```go
package main

import (
	"fmt"
	"os"
)

const appVersion = "0.1.0"

func main() {
	fmt.Fprintf(os.Stdout, "Eloqi %s — 桌面语音输入工具（开发中）\n", appVersion)
}
```

## 步骤 6：创建 README.md 骨架

新建 `README.md`，内容：

```markdown
# Eloqui

跨平台桌面语音输入工具（开发中）。

## 状态

- [x] P0 项目骨架
- [ ] P1 Linux 最小闭环
- [ ] P2 状态机与健壮性
- [ ] P3 macOS / Windows
- [ ] P4 TUI + 配置热重载 + doctor
- [ ] P5 体验完善
- [ ] P6 工程化与发布

## 构建

```bash
go build ./...
```

## 许可证

MIT
```

## 步骤 7：首次提交

```bash
go build ./...
go vet ./...
git add -A
git commit -m "chore: initialize Eloqui project skeleton"
```

---

## 验收标准（全部满足才算 P0 完成）

- [ ] `go build ./...` 通过，无报错。
- [ ] `go vet ./...` 无输出。
- [ ] `git status` 工作区干净。
- [ ] `git log` 有一条初始提交。
- [ ] 目录结构：`cmd/eloqi/main.go`、`internal/`、`plugins/`、`.gitignore`、`LICENSE`、`README.md`、`go.mod` 齐全。

---

## 完成后

进入 P1（见 TASKS.md）：先定义平台能力接口 + mock，再打通 Linux 最小闭环。
