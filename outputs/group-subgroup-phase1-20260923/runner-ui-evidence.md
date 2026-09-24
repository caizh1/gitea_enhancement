# 普通 Owner 与真实 Runner 首次执行记录

日期：2026-09-23。本次所有对象均为 loopback 13043 隔离实例中的一次性测试数据。管理账号 `phase1-owner` 没有实例管理员权限。

## 实际操作

1. 使用真实浏览器从创建仓库页面选择已创建的子群组 `phase1-parent/child`，创建并初始化私有仓库 `runner-safety`。
2. 通过仓库“新建文件”界面创建 `.gitea/workflows/phase1.yml`，提交标题为 `test(actions): 验证隔离 Runner 授权`。测试仓库提交为 `a435e2f6e523e0f09d8827587c184a592a1a45a8`，不是用户工作区提交。
3. 使用仓库已锁定的 `gitea.com/gitea/runner v1.0.8` 模块源码构建实际 Runner。二进制 `--version` 显示 `dev`；不将其冒称为正式发布的 v1.0.8 客户端包。
4. 使用当前子群组 Owner 的注册令牌注册子群组专属 Runner，标签 `phase1-isolated:host`，容量 1、轮询 2 秒、缓存关闭。工作目录位于工作区外，注册文件权限为 0600，未记录令牌值。
5. 工作流执行本地打印和携带内建作业 token 的本机仓库 API 读取请求；没有调用第三方 Actions，也没有配置生产凭据。
6. 浏览器从 Actions 列表进入 Run 1、Job 1，观察到 `Success`。展开执行步骤后，日志确实显示“一期真实 Runner 已执行”；仓库 API 请求使用 `curl --fail` 且步骤成功。

## 证据边界

- 首次执行服务端构建为本批较早的 `gitea-ui-next`，包含领取、凭据及注册令牌修复；不包含之后追加的全部群组移动与定时修复。最终构建复验另行追加。
- 此项证明普通 Owner 可创建子群组仓库、提交工作流，并由**本级子群组 Runner**实际执行，运行中的作业 token 可以访问自己的仓库。
- 此项不证明祖先 Runner、祖先 Secrets、定时、取消、失败、复用、全部重跑或合并门禁已交付。
- UI 页面路径：`/phase1-parent/child/runner-safety/actions/runs/1/jobs/1`。截图由浏览器工具在会话中返回，未另存图片；没有虚构截图文件链接。

## 包含群组移动与定时修复的构建复验

服务端换为 `gitea-phase1-final` 后，浏览器从 Run 1 点击重跑全部任务，得到 Attempt 2 / Job 2 成功；执行步骤和仓库 API 请求再次成功。

随后通过浏览器编辑同一 YAML，增加 `workflow_dispatch` 和每分钟 cron 触发，提交 `698f72b3c91f05e0030dd08426718434f6c1330d`（隔离测试仓库）。Run 2 为 push 成功；实际调度器创建的 Run 3 / Job 4 也成功。UI 明确显示 `Triggered via schedule`、触发身份 `gitea-actions`、`on: schedule`。这证明系统身份修复后的本级计划任务可真实领取和执行，不代表完整祖先继承或调度延迟指标已通过。

应用因会话中断停止后，重新启动 `gitea-phase1-reviewed-final`，继续验证群组移动。在保留子群组专属 Runner 的情况下，移动预览显示原路径和新路径；新增明确的队列取消、旧 Run/定时计划/注册令牌影响及解除条件。普通 Owner 点击确认，原生异步表单显示 `409 Conflict`，具体原因为“请先删除移动群组及后代的专属 Runner，并在移动后重新注册”。群组仍在原父级，移动未完成。

最初普通 POST 表单在本次内置浏览器中点击后未显示错误；复用现有 `form-fetch-action` 后可见上述反馈。没有凭此推断所有浏览器都具有同一故障，也没有把受限组合拒绝当作完整移动正例。

## 仓库转移与旧运行拒绝

同一普通 Owner 从仓库设置打开“转移”，预览从 `phase1-parent/child/runner-safety` 到 `phase1-owner/runner-safety` 后确认。页面跳到新归属并显示 `The repository has been transferred.`，Actions 列表中的原排队 Run 10–14 显示 `Canceled`。进入原 Run 1 点击 `Re-run all jobs`，页面显示 HTTP 409，原因“仓库归属已变化，请从当前仓库重新触发工作流”。

只读数据库佐证：仓库 OwnerID 从 5 改为 2、ActionsScopeRevision 为 1，14 个历史 Run 的 ScopeInvalidated 为 true，定时计划数为 0。数据库未用于补写测试配置。

随后经 UI 预览转回原子群组，提交被 `The new owner already has a repository with same name` 拒绝。排查确认没有同名数据库仓库，原物理目录属于正在转回的仓库本身。服务测试同时复现“转回原归属”与“改回原名称”两项错误，修复后仓库服务完整包退出 0。修复后的浏览器复验另行记录，不能将单测结果冒充 UI 通过。

## 最终构建与 ABA 正反例

更换为 `gitea-phase1-stage-a-i01` 后，因测试实例使用内存 session，重新登录同一普通 Owner。转移预览现在显示等待队列、旧 Run、定时计划、注册令牌和来源注册的影响，以及活动任务、专属 Runner、部署密钥和标签历史的限制。

1. 确认转回原子群组，页面跳到 `/phase1-parent/child/runner-safety/settings` 并显示转移成功。
2. 从 Actions 列表进入旧 Run 1，再点重跑全部，返回 409：“群组归属已变化，请从当前仓库重新触发工作流”。归属回到原值没有恢复旧信任。
3. 从工作流页点击 `Run Workflow`，选择 `main`，手动建立新 Run 15。使用原本属于目标子群组的合法 Runner（从该锁定模块源码构建）执行，Job 16 成功。
4. UI 显示 `Triggered via workflow_dispatch`、`on: workflow_dispatch` 和 `Success`；进入 Job 16 展开步骤，日志显示“一期真实 Runner 已执行”。包含作业 token 的本机 API 请求仍由 `curl --fail` 验证成功，没有输出 token。

最终界面路径：`/phase1-parent/child/runner-safety/actions/runs/15/jobs/16`。此项证明新合法触发正常，不证明旧任务、旧 Runner 或目的组新 Secrets 的所有竞态都已覆盖。
