# 非管理员 Developer 分支保护实测

本次在运行中的 v13 隔离实例 `127.0.0.1:13043` 验证仓库 2 `phase1-parent/child/deep/ancestor-ci`。仓库 6 `phase1-parent/root-policy` 仅有普通 Owner user 2 的现有授权，没有非管理员 Developer，因此本次没有向仓库 6 增加成员，也没有把两仓库的结果混作同对象证据。仓库 2 属 root4 子树，沿用 root4 已有 `phase1-protected/*` 禁推规则；未修改任何规则、成员、来源仓库或强制工作流。

授权来源经只读库及实际仓库 API 交叉核对：user 10 `phase1-developer` 在 group5 具有未到期直接 Developer（角色 30），向下继承至仓库 2，非实例管理员；`GET /api/v1/repos/phase1-parent/child/deep/ancestor-ci` 返回 200，权限 `admin=false,push=true,pull=true`。user 11 `phase1-reader` 在 group5 具有未到期直接 Reporter（角色 20），同一路径返回 200，权限 `admin=false,push=false,pull=true`。本仓库的 API 路径使用完整群组路径；API 响应的内部 `full_name` 为 `g_52a719c9404845c3b138950af203f979/ancestor-ci`，本次未依赖内部名写入。

| 操作 | 实际入口与结果 | 同对象反证 |
| --- | --- | --- |
| Developer API 创建普通分支 | `POST /api/v1/repos/phase1-parent/child/deep/ancestor-ci/contents/phase1-developer-20260924014733.txt`，`branch=main,new_branch=phase1-developer-20260924014733`，HTTP 201，提交 `555908d828ee08fc3f3de31ebe62e626594cd089` | 同文件 `GET .../contents/{path}?ref=phase1-developer-20260924014733` 为 200，内容与提交一致。 |
| Developer API 修改普通分支 | 同文件 `PUT`，HTTP 200，提交 `59808183f5787a72bb26dd71c75d9168075dcf30` | 同文件 GET 200，内容为修改版。 |
| Reporter API 同对象拒写 | user 11 对上述同一分支、同一文件发 `PUT`，HTTP 403 | user 11 仍可 GET 200；user 10 的 GET 200 与之相同，内容保持 Developer 的修改版。 |
| Developer API 禁推分支 | `POST .../contents/phase1-developer-20260924014733-protected.txt`，`branch=main,new_branch=phase1-protected/developer-20260924014733` | HTTP 403，`git ls-remote --heads` 确认该远端引用不存在。 |
| Developer 真实 HTTP Git 正常推送 | `git clone --single-branch --branch phase1-developer-20260924014733 http://127.0.0.1:13043/phase1-parent/child/deep/ancestor-ci.git`；本地修改上述同一文件、提交后 `git push origin HEAD:refs/heads/phase1-developer-20260924014733` | clone 与 push 均退出 0；远端引用与本地提交同为 `62b38f3601b81efb436afee8db28b41e47a5dbe9`；同文件 API GET 200，内容为 HTTP Git 推送版。 |
| Developer 真实 HTTP Git 禁推分支 | `git push origin HEAD:refs/heads/phase1-protected/developer-20260924014733` | 退出 1，pre-receive 拒绝；远端引用仍不存在。 |
| Reporter 真实 HTTP Git 同对象拒写 | user 11 对普通分支 clone 退出 0，在临时克隆中新增提交并向同一分支 push | push 退出 1；远端仍为 `62b38f...`；user 11 的同文件 API GET 200 且内容保持 Developer 推送版。 |

所有 HTTP Git 凭据仅放在进程环境供临时 `GIT_ASKPASS` 读取，远端 URL、仓库配置与证据均无密码或令牌。临时克隆已删除；提交含 `Assisted-by: Codex:gpt-6-sol`。原始逐步状态见 [运行记录](developer-branch-protection-runtime.json)，未保存原始认证响应。

预检时仓库 2 `main` 为 `da33580fc4d4eb315a02943ab207047ae0b784da`，结束仍相同。仅留下新普通分支 `phase1-developer-20260924014733`，最终 SHA `62b38f3601b81efb436afee8db28b41e47a5dbe9`；禁推目标 `phase1-protected/developer-20260924014733` 从未建立。未写 `phase1-secret-positive` 或其他已有分支。最终 SHA 触发祖先工作流 Run 63、64、65，均为 `push`、来源仓库 3 的提交 `b24b95c78d14dea6299b235831ffd8867ee99666`，由 Runner 2 执行成功；其工作流仅比较受保护值摘要并打印成功标记，三个任务日志未发现 Secret/Token/Password 赋值文本，未输出凭据值。日志检查没有读取 Secret 明文，不能据此宣称做过未知值的字节级全量搜索。

SSH Git 未测：当前 user 10 与 user 11 均无已登记公钥，本次不擅自改变账号密钥。受保护分支允许 Developer 推送的单独规则 `phase1-secret-positive` 因要求不写既有分支而未作本次新提交；本次验证的是普通分支正例与 `phase1-protected/*` 禁推负例。页面操作、其他仓库及其他角色未纳入本次结果。
