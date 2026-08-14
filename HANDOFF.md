# 项目交接文档（HANDOFF）

> 用途：新工作目录 / 新对话接入本项目的唯一入口。读完本文即可接上全部上下文，
> 无需再回顾之前的对话。

---

## 1. 项目概况

| 项 | 值 |
|---|---|
| 项目名 | **Eloqui** |
| 定位 | 跨平台桌面语音输入工具（热键 → 录音 → ASR 识别 → 剪贴板/上屏） |
| 语言 | Go |
| 许可证 | **MIT** |
| 目标平台 | Linux（Wayland + X11）、macOS、Windows，三平台全支持 |
| 目录 | `/home/xiangchanglin/projects/eloqi` |
| GitHub 用户名 | `xiangchang24` |
| module path | `github.com/xiangchang24/eloqi` |
| MIT 版权行 | `Copyright (c) 2026 xiangchang24` |

---

## 2. 背景与由来（务必先读）

Eloqui 是**参考一个已存在的 GPL v3 开源项目「just-talk-go」从头重写**的新项目。

关键事实：
- 原项目 just-talk-go 位于 `/home/xiangchanglin/projects/just-talk-go`，
  许可证 **GPL v3**，作者 `whoamihappyhacking`。
- 原项目代码**不能照抄、不能改名换 MIT**——那违反 GPL copyleft，也不符合用户意愿。

**合规红线（不可违背）**：
1. Eloqui 只**参考原项目的功能、架构思想、平台技术选型**（这些属“思想层”，合法）。
2. **代码必须用自己的表达重写**：目录结构、包名、函数名、注释、实现细节都自行设计，
   不逐行翻译、不复制原项目文件。
3. Eloqui 用 **MIT**，因此它必须是**原创代码**，不能是 GPL 代码的衍生品。

---

## 3. 已确定的关键决策（既定事实，勿再推翻）

- 项目名 **Eloqui**（拉丁语“雄辩地说”）。
- 技术栈 **Go**（单二进制、交叉编译、goroutine 契合流式并发；不换 Rust/Electron）。
- 三平台能力全保留。
- 全新 git 历史（不继承原项目 commit）。
- 架构：核心引擎 + 可插拔插件 + 平台能力层（build-tags 隔离平台差异）。

---

## 4. 已产出的文档（都在 eloqi/ 目录）

| 文件 | 作用 |
|---|---|
| `ELOQUI_DESIGN.md` | 设计蓝图：功能规格、架构、平台能力矩阵、关键设计决策 |
| `TASKS.md` | 分阶段任务清单 P0–P6，每阶段带验收标准 |
| `P0_BRIEF.md` | P0 的详细执行简报：含具体命令和文件内容建议 |

执行顺序：先读 `ELOQUI_DESIGN.md` 理解全局，再按 `TASKS.md` 从 P0 开始，
P0 的逐条命令看 `P0_BRIEF.md`。

---

## 5. 当前进度

- [x] 项目命名、许可证、module path 已定。
- [x] 三份文档（设计蓝图 / 任务清单 / P0 简报）已写好，占位符已填充。
- [ ] **P0（项目脚手架）尚未开始**——已交给 codex 执行。
- [ ] 其余阶段（P1–P6）未开始。

当前 `eloqi/` 目录只有三份文档，**还没有 `git init`、没有 `go.mod`、没有代码**。

---

## 6. 下一步（接上后该做什么）

1. 让 codex 读 `TASKS.md` + `P0_BRIEF.md`，执行 **P0**（git init → .gitignore → MIT LICENSE
   → go mod init github.com/xiangchang24/eloqi → 目录骨架 → main.go → README → 首次提交）。
2. P0 验收通过后，进入 **P1**：定义平台能力接口 + mock，打通 Linux 最小闭环。
3. 之后按 P2 → P3 → … 推进，每阶段对照 `TASKS.md` 的验收标准。

---

## 7. 架构要点速记（详细见 ELOQUI_DESIGN.md）

- 分层：入口 → 配置/环境检查 → 热键 Provider 工厂 → 引擎 → 插件（voice、overlay）
  → 平台能力层（hotkey / recorder / clipboard / autotype / overlay）。
- 平台矩阵（技术选型）：Wayland 热键用 evdev、X11 用原生 X11、macOS 用 CGEventTap、
  Windows 用轮询 + 低级钩子；录音分别是 arecord / CoreAudio / WinMM。
- 关键设计：显式录音状态机（idle/connecting/recording/stopping_delayed/stopping/error）、
  停止延迟缓冲、双输出防重、停止动作异步化。

---

## 8. 注意事项

- 旧项目 `just-talk-go` 目录**暂时保留**，后台还有一个 `just-talk --no-tui` 进程在跑，
  与新项目无关；将来删旧目录前先 `kill` 该进程。
- Eloqui 的二进制名、配置目录等，后续实现时统一用小写 `eloqi`。
- 所有文档和代码只做“设计/原创”，**不要引用或复制原项目 GPL 代码**。
