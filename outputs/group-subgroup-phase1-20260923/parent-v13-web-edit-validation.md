# 第十三版：Web 文件编辑与祖先分支保护

日期：2026-09-24。父任务使用真实浏览器，在 `127.0.0.1:13043` 的 v13 隔离开发实例验收；构建摘要见[第十三版记录](parent-v13-lifecycle-validation.md)。只新增仓库 6 和仓库 2 的测试分支，没有修改原 `main`、群组规则、成员或工作流来源。

## 页面操作与结果

普通非实例管理员 `phase1-owner` 从根群组的仓库列表进入 `phase1-parent/root-policy`，点击 README 的原生 Edit File。修改 README，填写中文提交说明以及 `Assisted-by: Codex:GPT-6`，保持 Signed-off-by 未勾选，选择新建分支。

| 操作 | 页面观察与实际结果 |
| --- | --- |
| 新分支 `phase1-protected/web-202609240145` 提交 | 同一编辑页面显示服务端拒绝；展开完整原因，明确为不允许向该受保护分支推送。表单内容保留，没有创建分支。Owner 没有绕过父组的 `push_role=none`。 |
| 保留内容，改为普通分支 `phase1-web-202609240145` | 提交成功进入 PR 比较页；显示一个新提交 `174377a8e75b60005823c8f47e8c79046721b067`，差异中有新增验收文本。没有创建或合并 PR。 |
| 实际 CI | 从比较页的状态链接进入 Run 62／Job 68，Actions 页显示 Success，祖先配置、变量及凭据布尔探针、产物生成和上传均成功；页面有一个 46 字节产物。 |
| 无关账号访问原编辑地址 | 切换到 `phase1-outsider` 后访问同一个 `_edit/main/README.md`，最终页面为原生 HTML 404，没有编辑表单。 |

随后通过 Owner 的真实 API 只读佐证：被拒分支查询为 404，普通新分支为 200 且 SHA 与页面一致，新分支 README 包含验收文本；`main` 仍为 `7d52d2d0d5f3d6f75f768c01acf840829885d19a`。四项检查通过，原始中文记录在 `/Users/archer/.cache/gitea-phase1-v5-validation/v13-web-edit-api.json`，未保存认证头或密码。

## 继承 Developer 与 Reporter

父任务另外切换为非实例管理员 `phase1-developer`，从子群组页面进入末级群组的 `ancestor-ci`（仓库 2），使用原生 README 编辑入口。其 Developer 来自 group5 的直接成员关系，向下继承，详见[Developer 协议证据](developer-branch-protection-evidence.md)。

- 向 `phase1-protected/web-developer-202609240152` 提交时，页面保留编辑内容并显示受保护分支禁止推送；没有建立该引用。
- 只将分支改为 `phase1-web-developer-202609240152` 后提交成功，比较页显示提交 `be9c5758c07e61a1a491ffcfea1748a750e0ec73` 及两行新增内容。祖先工作流 Run 72 真实成功。
- 切换为 `phase1-reader`，同一个原仓库编辑地址显示“不能直接编辑，可创建 fork 提议修改”，没有直接编辑表单。没有点击创建 fork，不能把这个合法提议入口误报为直接写权限。
- Reporter 通过 Actions 导航看到 Run 72 Success；详情有产物下载，没有重跑或删除控件，且工作流来源文件明确提示无权查看。
- Reporter API 的四项只读佐证通过：被拒引用 404，普通引用及内容 200，`main` 仍为 `da33580fc4d4eb315a02943ab207047ae0b784da`。原始记录在 `/Users/archer/.cache/gitea-phase1-v5-validation/v13-web-developer-api.json`。

## 边界

本例补齐已配置群组规则下 Owner 与继承 Developer 的 Web 文件修改、新建普通／禁推分支，以及 Reporter 的原仓只读和无关身份拒绝。删除文件、签名／CODEOWNERS、全部重叠规则或权限变化交错未验。Owner 样本在仓库 6，Developer／Reporter 样本在仓库 2，不合并成单一对象的全角色矩阵。

本次没有修改业务代码；页面与实际 Git／Actions 消费者结果一致，没有据此发现成立的 P0／P1。
