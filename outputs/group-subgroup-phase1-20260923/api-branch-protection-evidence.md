# 群组基础分支保护：API 与 HTTP Git 实测

本次仅写入本机隔离服务 `127.0.0.1:13043` 的 `phase1-parent/root-policy`（仓库 6）。沿用群组 4 已有规则：`phase1-protected/*` 禁止推送，`phase1-secret-positive` 允许 Developer 及以上正常推送但禁止强推。未修改规则、成员、强制工作流或现有 `main`。调用使用测试账号的进程内凭据；证据不含密码、令牌或 Secret 明文。原始逐步记录见 `api-branch-protection-runtime.json`。

版本边界：表中前七项 API／HTTP Git 正反例及 Run 53—59 在 v12（SHA-256 `e80d141963ce43ff15e9d3e0695eede84062f8a8a803380b025fb4b7069a098f`）完成；最后一项保持 HEAD 的受控 API 正例及 Run 60／61 在 v13（SHA-256 `4d70a5a01367f3c33e289395416470fd05a7343b6a86e30c949ff80697c56ae0`）完成。旧结果没有冒充 v13 重新执行。

| 实测动作 | 角色与入口 | 结果及同对象对照 |
| --- | --- | --- |
| 私有仓库发现 | Owner、无关账号分别 GET 仓库 API | Owner 200；无关账号 404。 |
| 创建及修改普通分支文件 | Owner `POST`、`PUT /api/v1/repos/phase1-parent/root-policy/contents/{path}` | 新建 `phase1-api-20260924012145` 返回 201；随后修改返回 200；两次均以同路径 GET 200 核对对应内容。 |
| 无关账号修改普通分支 | 无关账号 `PUT` 同一文件 | 404；Owner 立即 GET 200，内容仍为合法修改后的版本。 |
| 推送无人规则 | Owner 经内容 API 创建 `phase1-protected/api-20260924012145`，再经 HTTP Git 创建同名引用 | API 403；Git pre-receive 拒绝（退出码 1）；远端该引用不存在。Owner 身份不越过 `push_role=none`。 |
| 允许的受保护分支正常写 | Owner 经内容 API 创建、修改 `phase1-secret-positive`；经 HTTP Git 正常推送 | API 依次 201、200，Git 推送成功；每步均以同路径 API GET 核对。此规则允许 Developer 推送，因此 Owner 成功符合配置，未声称 Owner 可绕过禁推规则。 |
| 普通分支正常推送与强推 | Owner 经 HTTP Git 推送、再以本地旧提交生成分叉并 `--force` 推送 | 两次均成功；远端最终 SHA `e8e7b6e85dad1ffe1fb27a469eaf1338f793b51d`，API 同文件 GET 200，内容为强推版本。仅重置过临时本地克隆。 |
| 受保护分支强推 | Owner 对 `phase1-secret-positive` 生成分叉后经 HTTP Git `--force` | pre-receive 拒绝（退出码 1）；当时远端仍为正常推送 SHA `10210f5419c1cd4b7a37640e30ad325854cd2d9c`；API 同文件 GET 200，内容仍为正常推送版本。 |
| 受控 API 正例 | Owner 在 `phase1-secret-positive` 单次 `PUT` 文件后保持引用不变 | API 200，提交 `5d216872b2aa626682ba1dc0e159c12787614176`；同文件 GET 200 且内容匹配。对应两个真实运行结束前未再写此引用。 |

最终 `main` 仍为预检 SHA `7d52d2d0d5f3d6f75f768c01acf840829885d19a`。本次留下两个测试分支：`phase1-api-20260924012145`（`e8e7b6e85dad1ffe1fb27a469eaf1338f793b51d`）和 `phase1-secret-positive`（`5d216872b2aa626682ba1dc0e159c12787614176`）；被拒绝的 `phase1-protected/api-20260924012145` 从未建立。测试提交含 `Assisted-by: Codex:gpt-6-sol`。

这两个新分支触发了仓库 3 的祖先 scoped workflow。普通分支最终 SHA 的 Run 56、57 均成功。受保护分支 HTTP Git 推送 SHA `10210f5419c1` 的祖先 Run 58 与凭据探针 Run 59 均成功，使用 Runner 2；Task 58 日志显示受保护凭据存在并通过。中间历史 SHA `dd4e29ca14e5` 的探针 Run 53、55 则失败：日志显示凭据不存在。只读追踪确认两条旧 Run 在 2026-09-23 17:21:49 UTC 创建，但任务直到 17:21:58 和 17:22:00 才领取；新 SHA `10210f5419c1` 的 Run 58、59 于 17:21:52 创建，故旧任务领取时其提交已经不是受保护分支 HEAD。`ProtectedRefTrusted` 要求运行的 SHA 等于当前持久分支 SHA，因而保守拒绝向旧运行下发受保护 Secret；新 SHA 的探针于 17:22:05 领取并成功。两批运行均为完整 `refs/heads/phase1-secret-positive`、`push` 事件、触发者 user 2、同一来源仓库 3 及来源 SHA `b24b95c78d14dea6299b235831ffd8867ee99666`，排除了短引用和来源身份差异。API 与 HTTP Git 均经 push hook、分支表同步和同一 Actions PushCommits 通知路径。

为反证“API 写入必然不给受保护 Secret”，再次仅经 API 修改自己的 `phase1-secret-positive`，并保持新 SHA `5d216872b2aa626682ba1dc0e159c12787614176` 不变直到任务结束。Run 60（祖先检查）与 Run 61（受保护凭据探针）均于 2026-09-23 17:27:01 UTC 创建，分别在 17:27:05 UTC 前成功；探针 Task 60 的脱敏日志仅显示 `凭据存在=true`，并出现“受保护正例通过”。其引用、事件、触发者与工作流来源均与旧运行一致。由此确认旧失败是提交过期的预期拒发，不是 API 与 HTTP Git 授权路径差异。探针同时把 `github.ref_protected` 输出为“不可用”；这里的正例结论只依据实际受保护 Secret 是否下发，不把该上下文字段作为独立成功证据。

本次未验 SSH Git、非 Owner 的有权 Developer 正例、页面操作及其他仓库；未读取 Secret 明文，也未对未知明文做字节级泄漏搜索。受保护 Secret 的判定证据限于工作流脚本与任务日志的布尔输出。
