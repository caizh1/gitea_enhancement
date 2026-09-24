# 受保护凭据：真实分支与 fork 验收

日期：2026-09-23；构建 v8，真实本机 Runner。该记录不代表全部凭据、事件或生命周期矩阵通过。

## UI 配置

普通父组 Owner 从原生 Secrets 页新增合成凭据 `PHASE1_PROTECTED_TEST_SECRET`，勾选仅可信受保护分支可用。刷新后显示保护标记、描述及掩码。深层仓库 `ancestor-ci` 页面显示来源 `phase1-parent`、本地只读及保护标记，没有返回值。

从群组设置的“基础分支保护”新增精确规则 `phase1-secret-positive`，允许 Developer 及以上推送、合并，不允许强推；刷新后规则保留。负例 `phase1-secret-negative` 不匹配任何保护规则。

父组来源仓库新增 `protected-probe.yaml`，通过真实 Git 推送保存，使用原生 Actions YAML。探针只判断环境中的测试凭据是否为空，输出引用、事件和布尔结果，不输出或写出凭据值。

## 首次失败与 Runner 兼容边界

来源提交 `872f123` 的探针还断言 `github.ref_protected` 的布尔值；Run40、Run42 均失败。查到 Runner v1.0.8 的 `act/model/GithubContext` 没有该字段，`internal/app/run/runner.go` 只拷贝已知字段；`github`、`gitea` 两个表达式别名均指向这一结构，未知属性变为空字符串。服务端虽然在 payload 计算该字段，Runner 中并不可用。

没有修改 module cache、强行升级 Runner、放宽服务端规则或删除失败记录。来源提交 `b24b95c` 将凭据门禁验收和上下文字段兼容分开：仍严格断言正例非空、反例为空，同时明确输出“上下文保护标记=不可用”。因此以下通过仅证明实际凭据边界，不表示 `ref_protected` 表达式已交付。

## 实际运行

| 场景 | 真正执行及浏览器证据 | 结论 |
| --- | --- | --- |
| 受保护分支 push | Run45／Job50，1 秒成功；页面日志 `事件=push；引用=refs/heads/phase1-secret-positive；上下文保护标记=不可用；凭据存在=true` | 本场景允许下发 |
| 未保护分支 push | Run47／Job52，成功；页面日志 `事件=push；引用=refs/heads/phase1-secret-negative；上下文保护标记=不可用；凭据存在=false` | 本场景拒绝下发 |
| Developer 的私有 fork PR | Developer 从 UI fork 为 `phase1-developer/phase1-fork-probe`（仓库11），真实 clone／分支／push，再从 UI 创建目标仓库 PR5；Run49／Job54 确实执行成功，页面日志 `事件=pull_request；引用=refs/pull/5/head；上下文保护标记=不可用；凭据存在=false` | fork 不因祖先来源而取得受保护凭据 |

PR5 同时运行原来的 `ancestor-check.yaml`。它要求原始非受保护 Secret 的精确摘要，fork 中该凭据不可用，故 Run48 失败；页面分别显示探针成功、必选工作流失败及审批未满足，不能合并。该失败是冻结样本的预期约束，不把整个 fork PR 记为 CI 全部通过，也没有为通过而注入凭据。

## 剩余边界

- `pull_request_target`、受保护引用上的定时／手动／重跑、分支变更和撤权的全部真实交错还未验收。
- `ref_protected` 当前仅由服务端计算，在已验 Runner 的表达式中不可使用；需要单列兼容说明，不能当成工作流安全门禁。
- 测试后数据集为原十个群组仓库加一个私有测试 fork。容量测试仍独立，不能用此数据量宣称千仓通过。
- 该本机 PR URL 无法被 Codex 的受支持 PR 附件工具识别；浏览器、运行记录与仓库中的 PR 历史保留，不伪造远端 PR 链接。
