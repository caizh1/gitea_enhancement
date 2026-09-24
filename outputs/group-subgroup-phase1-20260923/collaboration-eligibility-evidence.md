# U04 协作资格定向证据

## 范围与实现

本包只修正指派人、审查人候选及治理来源的审查请求操作者资格。候选先从原生协作关系和目标项目、祖先、有限共享链的成员记录取得可能用户，再逐人按当前项目单元权限过滤。共享上限、过期和撤权均由现有 `RepositoryGrants` 与 `GetIndividualUserRepoPermission` 最终判定；没有把共享边当作配置继承边。原生团队仍遵守当前仓库的单元权限。

原生协作者和团队能够修改审查请求的历史判据保持不变；新增治理来源要求 `WritePulls` 且当前 PR 单元达到写权限。共享 Reporter 可以成为审查候选，不能据此修改他人的请求。仓库未启用 PR 单元时，Owner 也不进入审查候选；旧测试相应收紧。

原生 `models/issues/issue_user.go` 仍调用旧 `repo_model.GetRepoAssignees`，用途是新 Issue 的 `issue_user` 已读状态初始化，不决定自动补全、通知接收或最终写入授权。真实候选消费者六处 Web/API 指派入口和审查候选服务已改用当前有效权限来源。

## 固定回归与执行

修改前执行 `go test -run '^TestRepoGetReviewersIncludesGovernanceMember$' ./services/pull/`：失败，治理成员 ID 43 不在返回的 `[2,4]` 中。修改后同例通过。

`TestRepoCollaborationCandidatesRespectAncestorShareAndRevocation` 使用合成根组→子组→仓库、兄弟组及仓库共享场景，验证祖先 Developer 可作为候选并修改请求；共享 Developer 被 Reporter 上限限制后仍可作为候选但不可修改他人请求；改为 Guest 上限后候选消失；兄弟组 Developer 与同根 Guest 均不进入候选；删除祖先成员后候选与操作权一并消失。

执行命令：

```text
/Users/archer/.cache/gitea-governance/go/bin/go test -run '^TestRepoGetReviewers|^TestRepoCollaborationCandidatesRespectAncestorShareAndRevocation$|^TestGovernanceReviewRequestLoadsRepository$|^TestUserRepo$' ./services/pull/ ./services/issue/ ./models/repo/ ./models/perm/access/ ./routers/web/repo/ ./routers/api/v1/repo/
```

结果：六个包均通过。`PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH /Users/archer/go/bin/golangci-lint run --new-from-rev=HEAD ./models/perm/access ./services/pull ./services/issue ./routers/web/repo ./routers/api/v1/repo`：`0 issues`。`git diff --check`：通过。未运行共享 PostgreSQL 测试库，也未进行真实鼠标 UI 与邮件投递验收。

## 只读审查与差异

单个 `@username` 提及在模型解析时逐人检查当前 Issue/PR 读取权；自动补全从修正后的指派候选取得祖先或共享合格用户。`@org/team` 仍是仓库归属组织的原生团队语法，本期不支持祖先群组整组广播；其通知接收者仍需按当前仓库单元权限过滤。旧通知在只撤 Issue/PR 单元、保留 Code 时仍可显示撤权后改动的标题，已交独立门禁工作包处理，此包未修改通知代码。

## 对抗式自审

VERDICT: PASS（仅针对本包代码；通知旧线程的已成立泄漏由独立工作包修复）。

P0/P1 BLOCKERS: 本包未发现成立项。候选源会包含共享链的可能成员，但最终逐人按当前真实权限过滤；撤权和低权限负例通过，未发现候选反向授权路径。

UNVERIFIED RISKS: 大型群组的候选枚举及逐人有效权限查询尚无容量基准；真实 Web/API 自动补全和邮件发送未验收。

NON-BLOCKING FINDINGS: 原生只读协作者／原生 Team 对他人审查请求的历史操作资格保留，新增治理来源更严格，二者存在兼容差异；一期未提供祖先群组整组提及语法。
