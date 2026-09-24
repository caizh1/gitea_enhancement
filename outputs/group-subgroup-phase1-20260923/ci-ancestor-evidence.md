# I-03 祖先 CI 配置定向证据

记录时间：2026-09-23 19:10（北京时间）。工作树含多方并行修改；本记录只覆盖本工作包的定向检查，不代表一期产品验收。

## 消费者与边界

- Runner 候选读取、最终领取及运行中凭据复核均按当前命名空间父链判定；共享关系不参与。保留仓库及实例 Runner。
- 任务版本在新增或重新入队时递增当前归属及其祖先、仓库、实例版本。现有入队和重跑调用同一入口。
- 变量按实例、根到当前群组、仓库依次覆盖；Secret 按根到当前群组、仓库依次覆盖，仍禁止实例 Secret。fork 和复用任务原有 Secret 限制保留。
- 原生页面列出继承来源、覆盖状态和只读状态；来源路径按当前用户的群组读取权投影，无权时仅显示“受限来源”并隐藏非必要描述。被覆盖且无来源读取权的上游变量值不显示。Secret 列表仅显示名称、获准查看的描述与掩码，子级页面没有父级编辑入口。Runner 编辑入口仍按资源自身归属判定。
- 较近来源的 Secret 密文若损坏，凭据构造直接失败；不回退到上游同名 Secret。

## 已执行检查

- `/Users/archer/.cache/gitea-governance/go/bin/go test -run '^(TestAncestorRunnerScopeAndNotification|TestVariablesPreferNearestNamespaceAndRepo|TestAncestorRunnerClaimAndMoveRevocation|TestTaskSecretsPreferNearestNamespaceAndKeepForkBoundary)$' ./models/actions ./models/secret`：通过。合成数据覆盖父级 Runner 领取、兄弟组和仓库专属拒绝、移动后凭据失效、版本通知、同名配置优先级、无关组隔离和 fork 不泄露 Secret。
- `/Users/archer/.cache/gitea-governance/go/bin/go test -run '^(TestInheritedVariablesHideRestrictedSourceAndCannotEditParent|TestInheritedSecretsHideRestrictedSourceAndCannotDeleteParent)$' ./routers/web/shared/actions ./routers/web/shared/secrets`：通过。私有父组与子组 Owner 的合成场景中，父级路径及描述不回传；已覆盖的上游变量值隐藏；子级入口不能通过父级变量 ID 编辑，也不能删除父级 Secret。
- `/Users/archer/.cache/gitea-governance/go/bin/go test ./models/actions ./models/secret ./routers/web/shared/actions ./routers/web/shared/secrets ./routers/web/repo/actions ./routers/web/repo/setting`：六包通过。执行时其他代理修复了其页面编译错误；本结果只代表该时点工作树。
- `PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH /Users/archer/go/bin/golangci-lint run --new-from-rev=HEAD ./models/actions ./models/secret ./routers/web/shared/actions ./routers/web/shared/secrets ./routers/web/repo/actions ./routers/web/repo/setting`：零项增量问题。非增量检查仍报告其他已存在或并行文件问题，不作为本工作包新增缺陷。
- `python3 -m json.tool options/locale/locale_en-US.json` 和中文同名文件：通过。
- `git diff --check`：通过。

## 尚未验证

- 未在本工作包启动真实 Runner、用鼠标完成原生 UI 保存和回显，也未测 PostgreSQL 并发交错；交由父代理统一集成验收。
- 祖先 Runner 候选查询使用当前路径前缀缩小范围，最终领取与凭据授权仍重新验证完整父链。千仓、深层级及两次轮询周期内发现的目标环境性能尚未实测。
- 本工作包没有新增 API 有效配置列表；当前原生页面展示来源，运行时按父链取值。若一期要求外部 API 读取有效来源，需要单独明确接口及访问权边界。
