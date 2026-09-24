# CI06 真实 Runner 作业令牌跨仓验收

日期：2026-09-24。隔离实例 `127.0.0.1:13043` 的当前固定入口指向 v13 macOS arm64 开发构建，实测 SHA-256 `4d70a5a01367f3c33e289395416470fd05a7343b6a86e30c949ff80697c56ae0`。本次只用普通 `phase1-owner`（用户 2、非站点管理员）及现有在线根 Runner 2 `phase1-root-trusted`，其自定义标签是 `phase1-isolated`。未操作浏览器或 PostgreSQL，未重启运行实例，也未修改原十仓、原父级配置或成员／共享关系；仅在该隔离实例内创建并配置下列专用对象。

## 授权模型与隔离对象

源码确认：`models/perm/access/repo_permission.go` 的作业身份先重检当前任务凭据和触发者来源读取资格；同 Owner 私有跨仓需该 Owner 配置的 `AllowedCrossRepoIDs`，fork PR 不可借此读取。`models/actions/token_permissions.go` 对来源及目标仓库、Owner 祖先取绝对权限上限交集；跨仓用 `MakeRestrictedPermissions` 钳制。该集合只保留 Code、Packages、Releases 的 `read`，**Actions 为 `none`**。共享关系不作为配置继承边。YAML 显式 `permissions: {}` 全部为 `none`。因此 Code 跨仓读正例与 Actions 产物跨仓拒绝是两个不同权限结果，不应混记。

只创建以下专用私有对象，并保留供复核：

| 对象 | ID／路径 | 本次设置 |
| --- | --- | --- |
| 来源子群组 | 19，`phase1-parent/phase1-ci06-1790185738`；原生 Owner 名 `g_b001db7da33343fd865b1236a1938422` | 先只将本组仓库 16、18 加入跨仓 allow-list；在 Run 75／82 后才把本组 Code 上限设为 `none`、Actions 上限设为 `read`。 |
| 来源仓库 | 15，`ci06-source` | 专用工作流与合成 `ci06-data.txt`。 |
| 允许目标 | 16，`ci06-allowed` | allow-list 私有目标；v3 目标产物实际上传。 |
| 未授权目标 | 17，`ci06-denied` | 私有且不在 allow-list。 |
| 上限目标 | 18，`ci06-capped` | 在 allow-list，但仓库级 token 绝对上限为全 `none`。 |
| 后代群组／仓库 | 群组 20 `.../ci06-ceiling`；仓库 **20** `ci06-ancestor` | 新祖先组 19 的 Code `none`、Actions `read` 对后代仓库生效。仓库 19 属另一独立样本，本任务未触碰。 |

## 真实执行结果

来源仓库 [Run 75／Job 81、82](http://127.0.0.1:13043/phase1-parent/phase1-ci06-1790185738/ci06-source/actions/runs/75/jobs/81) 均 `completed/success`。Job 81 的工作流以 `secrets.GITEA_TOKEN` 请求实际 REST API；`github.token` 与该值相等的布尔检查为 `true`，只打印状态和布尔值：

| 探针 | 状态 | 判定 |
| --- | --- | --- |
| 本仓 Code 内容 | 200 | 有效任务可读自身仓库。 |
| allow-list 私有目标 Code 内容 | 200 | 当前授权允许跨仓只读。 |
| 未授权私有目标 Code 内容 | 404 | 未列入目标许可。 |
| 已列入目标但目标上限全 `none` 的 Code 内容 | 404 | allow-list 不能越过目标上限。 |
| allow-list 目标创建文件 | 403 | 请求的 Code `write` 被跨仓只读上限收紧；未创建文件。 |
| 本仓 Actions 产物列表 | 200 | 本仓 Actions `read` 有效；这不等于已有可下载产物。 |
| allow-list 私有目标 Actions 产物列表 | 403 | 跨仓固定上限的 Actions 为 `none`，即使目标仓已许可 Code 读。 |
| 未授权／目标全 `none` 的 Actions 产物列表 | 404／404 | 两类拒绝均生效。 |
| Job 82 显式 `permissions: {}` 本仓／目标 Code | 404／404 | 显式空声明保持全 `none`。 |

目标 [Run 70／Job 76](http://127.0.0.1:13043/phase1-parent/phase1-ci06-1790185738/ci06-allowed/actions/runs/70/jobs/76) `success`，官方 `actions/upload-artifact` v3.1.3 固定提交的作业日志确认专用 `ci06-target-artifact` 已完成上传。但仓库 REST 产物列表对该 Run 返回 200、数量 0；源码的仓库 REST 列表仅枚举 v4 格式。随后来源 [Run 86／Job 94](http://127.0.0.1:13043/phase1-parent/phase1-ci06-1790185738/ci06-source/actions/runs/86/jobs/94) 用 `Bearer GITEA_TOKEN` 请求真实目标 Run 70 的 v3 runtime 产物路由，返回 **400**，响应正文精确为 `run-id does not match`。这不是令牌认证失败：`ArtifactContexter` 先尝试 JWT，旧 Runner 的作业令牌走 `GetRunningTaskByToken` 回退并复查任务有效性；随后 `validateRunID` 要求 URL Run ID 等于当前任务的 Run ID，拒绝跨 Run。`ACTIONS_RUNTIME_TOKEN` 与 `GITEA_TOKEN` 是不同凭据，不能把此 400 说成两个 token 等价。

官方 [upload-artifact v4.6.2](https://github.com/actions/upload-artifact/releases/tag/v4.6.2) 只在此新目标仓试跑 [Run 73／Job 79](http://127.0.0.1:13043/phase1-parent/phase1-ci06-1790185738/ci06-allowed/actions/runs/73/jobs/79)，结果 `failure`；原生日志首错为 `GHESNotSupportedError: @actions/artifact v2.0.0+, upload-artifact@v4+ and download-artifact@v4+ are not currently supported on GHES.`。没有修改官方 action 或绕过检查，也没有取得 v4 目标 Artifact ID；**跨仓产物下载正例未验证，当前跨仓 Actions 权限实际拒绝**。该失败是本机 Runner 与本次官方 v4 action 组合的兼容性观察，不能外推到所有上传客户端。

设置新祖先组上限之后，后代仓库 [Run 84／Job 92](http://127.0.0.1:13043/phase1-parent/phase1-ci06-1790185738/ci06-ceiling/ci06-ancestor/actions/runs/84/jobs/92) `success`；其工作流声明 Code／Actions 都要 `read`，实际本仓 Code 返回 403、Actions 返回 200，证明新祖先的 Code `none` 独立限制后代。Run 86 比 Run 75 晚，继承这一新上限，故其普通 Code 状态不能用来否定 Run 75 的先前跨仓正例。

## 首错与边界

首轮脚本把调度响应字段 `workflow_run_id` 误按 `id` 读取，调度本身已返回 200；从列表恢复时又把同仓祖先强制工作流的 push Run 71 误当专用 workflow_dispatch Run 70。后续按 `event` 与工作流 `path` 找到 Run 70，并对真实 Run 重新跑 v3 产物请求；原始错选及更正均保留在 JSON。产物日志解析曾漏掉含数字的 `target_v3_runtime` 键，修正解析后只重新读取原 Job 日志，没有为该解析问题重跑作业。随后仅对真实目标 Run 70 发出一条新探针 Run 86。脚本首错不作为产品失败。

命令：`python3 outputs/group-subgroup-phase1-20260923/ci06-runner-token-client.py` 与 `python3 outputs/group-subgroup-phase1-20260923/ci06-runner-token-followup.py`，密码在 PTY 的 `getpass` 提示无回显输入；首脚本纠正前的尝试退出 1，完成来源 Run 75 的那次退出 0；第二脚本最终退出 0。脚本只保存 ID、状态、布尔量和无敏感信息的错误原因。原始状态文件 [ci06-runner-token-raw.json](ci06-runner-token-raw.json)，SHA-256 `ea3f772cc6bb2e2c39c81f25abc90d6a397799d4339bc634e66a750920c309da`；已检查不含测试密码、Authorization 值或令牌原文。没有输出完整 Job 日志或响应主体中的通用 payload。

本次实际通过的是同 Owner 私有仓 Code 只读、未授权／上限／声明拒绝及后代祖先上限。跨仓 Actions 产物读、v4 实物下载、Packages 令牌协议、目标不同 Owner、fork、任务中撤权／转移与并发重检均未由本次真实客户端覆盖。对已走通链路未发现成立的 P0／P1；这些未验项不计入一期完成率。
