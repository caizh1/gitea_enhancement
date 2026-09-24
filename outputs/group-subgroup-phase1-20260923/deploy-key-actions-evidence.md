# 部署密钥触发的 Actions 等待队列：修复证据

## 真实首因

v5 隔离环境中，仓库 9 的部署密钥推送成功，但首个工作流尝试（Run 36、Attempt 39、Job 39）一直等待。只读 SQLite 显示首次触发者为组织 6、任务 ID 为 0；两个运行器均在线，祖先运行器 2 具有所需标签。`routers/private/serv.go` 的部署密钥路径把仓库 Owner 作为 Git 推送者；`services/repository/push.go` 传给 Actions 通知，`services/actions/notifier_helper.go` 把该组织记为触发者。领取任务时，`models/actions/task.go` 的 `TaskCredentialValid` 因组织用户非活跃而拒绝；领取事务回滚，任务继续等待。拒发凭据是正确的安全边界，永久等待和“等待运行器可用”的页面解释不正确。

父任务随后在 v5 页面取消首次尝试，由有权用户重新运行，新的 Attempt 40 被祖先运行器 2 成功执行并产出一个构件。这证明现有人工重新授权路径可用；本修复不把组织映射为某个 Owner，也不向组织身份开放祖先变量或 Secrets。

## 最小变更

`services/actions/run.go` 在插入运行前识别组织触发者。运行、首次尝试及其任务在同一事务中直接写为已取消，保留任务模板供有权人类重新运行；不会发布可领取任务，也不会触发运行级或任务级并发取消、展开可复用工作流。普通用户和既有系统定时任务继续原路径。

`routers/web/repo/actions/view.go` 只根据当前查看的尝试身份与取消状态给出原因。摘要和任务详情均提示组织身份不能获得 Actions 凭据，并引导有权用户重新运行；查看成功的人类重跑尝试时不再显示旧原因。中英文文案、前端类型和摘要展示已同步。

## 验证

- 红例：`go test -run '^TestPrepareRunAndInsertOrganizationTriggerCannotQueue$' ./services/actions/` 初次失败，运行、尝试、任务实际均为 Waiting。
- 绿例：同一测试通过，覆盖必选作用域工作流、组织首次尝试取消、无任务领取、人类重跑成为 Waiting、普通用户触发以及系统定时任务仍为 Waiting。
- 当前尝试来源测试：`go test -run '^TestOrganizationTriggerCancellationUsesSelectedAttempt$' ./routers/web/repo/actions/` 通过；旧组织尝试显示原因，新的人类成功尝试不显示。
- `go test ./routers/web/repo/actions/` 通过；定向 Go lint 为零问题，两个变更的前端文件定向 ESLint 通过；中英文 JSON 可解析，`git diff --check` 通过。
- `go test ./services/actions/` 未全绿：`TestGenerateGiteaContextProtectedRefUsesCurrentRules` 和 `TestScopedTaskCredentialRejectsArchivedWorkflowSource` 各自单独运行仍失败，属于本轮并行脏改中的其他测试，尚待相应代码所有者处理。本次定向运行创建与取消测试均通过。

## 未验证边界

该修复尚未进入 v6 运行实例，真实页面需由父任务整合 v7 后验证。没有使用共享 PostgreSQL，也没有改动运行中的浏览器、实例或 SQLite 数据。非组织的人类用户在排队后被停用时，现有领取检查同样拒发凭据；其等待状态的终止策略不属于此次部署密钥触发修复，需另行审查。
