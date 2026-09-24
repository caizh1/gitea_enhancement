# 原生组织 OAuth2 应用授权证据

## 范围与旧行为

组织 OAuth2 应用路由在进入请求时检查 Owner，但创建、编辑、轮换密钥和删除的模型写事务原先不复核当前请求人及群组状态。实例应用共用处理器也只在请求入口核对管理员；本轮把其最终当前管理员检查接入相同的写事务回调。真实旧版 UI 已由父任务在归档子组的应用编辑入口复现写入成功；本文件的本机证据仅来自 SQLite 定向测试，不代表已完成新版本浏览器或 PostgreSQL 验收。

## 先红后绿

先添加完整回归及模型回调签名，但暂不在写事务调用回调。运行：

```text
GITEA_TEST_DATABASE=sqlite3 /Users/archer/.cache/gitea-governance/go/bin/go test -tags 'sqlite sqlite_unlock_notify' ./services/org -run '^TestNativeOAuth' -count=1
```

运行态红例：`TestNativeOAuthRevokedOwnerCannotMutate` 在旧 Owner 被移出原生 Owners Team 后创建应用仍返回 `nil`；`TestNativeOAuthGroupOwnerAndLifecycle` 的“祖先归档”和“待删除”两例创建子组应用也返回 `nil`。测试均在预期的 `ErrNotFound` / `ErrConflict` 断言处失败。此前尚未添加模型回调签名时，同命令还曾在编译阶段失败；该编译失败不作为行为复现。

将当前 Owner、组织身份与祖先生命周期检查接入同一最终写事务后，运行：

```text
GITEA_TEST_DATABASE=sqlite3 /Users/archer/.cache/gitea-governance/go/bin/go test -tags 'sqlite sqlite_unlock_notify' ./models/auth ./services/org ./routers/web/org ./routers/web/user/setting -run '^(TestNativeOAuth|TestOrganizationOAuth|TestOAuth2Application_GenerateClientSecret|TestCreateOAuth2Application)' -count=1
```

结果：四包均 `ok`。覆盖旧 Owner 撤权后的四种写操作、旧 Web `ctx.Org.IsOwner` 快照下轮换密钥拒绝、正常父级继承与子级直接 Owner、祖先归档与待删除、归档时允许当前 Owner 删除应用、个人和实例应用原有模型调用，以及组织设置表单使用公开群组路径。

实例管理员入口另用旧请求上下文固定红例：先让另一人取得管理员资格，再在数据库撤销当前请求人的管理员资格，调用原生 `ApplicationsRegenerateSecret`；修前返回 200 并轮换，预期 404，测试失败。给实例共用处理器注册当前管理员回调后运行：

```text
GITEA_TEST_DATABASE=sqlite3 /Users/archer/.cache/gitea-governance/go/bin/go test -tags 'sqlite sqlite_unlock_notify' ./routers/web/admin -run '^TestInstanceOAuth' -count=1
```

结果 `ok`。旧上下文轮换现在返回 404、密钥哈希未变；当前有效管理员可创建、编辑、轮换和删除实例应用。管理员回调在最终事务重读当前账号的激活、禁止登录、管理员及主体类型状态。测试更新数据库是为了固定中途撤权，不依赖请求中缓存的 `IsAdmin`。

组织应用创建和密钥轮换均在治理写锁外生成随机密钥并完成 bcrypt；组织 Web 创建把密钥哈希与应用放在同一事务提交。审计函数只记录应用名称、客户端类型、跳过二次授权标志和重定向来源，不记录密钥或哈希。失败时不返回明文密钥。删除作为撤权操作，在归档与待删除时仍允许当前 Owner 执行。

## 静态检查与限制

仅对本轮 Go 文件执行 `gofmt`，`git diff --check` 无输出；双语 locale JSON 均通过 `python3 -m json.tool`。定向 `golangci-lint` 在模型、组织服务和共用 Web 的四个目标包报告 4 项，全部位于未由本轮修改的 `models/auth/source.go` 三处与 `source_test.go` 一处；本轮 OAuth 文件无报告项。新增的 `./routers/web/admin` 包定向 lint 为 `0 issues`。首次 lint 未把私有 Go 目录加入 `PATH`，因找不到 `go` 未执行分析；再次加上该目录后得到上述实际结果。

随后独立复核发现个人 API 的 `sudo` 可把请求 `Doer` 换成组织账号，因此原“个人入口无法指定组织 UID”的判断撤回。个人 API 与个人 Web 的最终授权修复及回归见下节；客户端密钥自身不能直接签发用户令牌。

本轮未操作活跃 UI、共享 PostgreSQL 或 Git 分支，也未验证最终资产生成后的浏览器表单行为。父任务负责新构建和真实 UI / PostgreSQL 复验。

## 个人 API 与个人 Web 的最终授权补充

API 的 `sudo` 中间件在请求开始时保留原管理员于 `AuthenticatedUser`，并把 `Doer` 换成目标账号。旧 API 应用创建直接使用目标 UID，没有排除组织身份，也没有在最终写事务复核目标账号或原管理员的当前资格。个人 Web 共用处理器原先没有注册授权回调，在途请求遇账号停用仍可轮换密钥。

先添加专用路由回归，再用 Go `-overlay` 将 API 与个人 Web 入口替换为修改前源码执行同一测试。运行态红例包括：`sudo` 到组织账号的 API 创建返回 201 而非 404；已停用个人的旧 API 请求创建返回 201；撤销原 `sudo` 管理员后旧 API 请求创建返回 201；已停用个人的旧 Web 请求轮换返回 200 而非 404。最初尝试直接运行红例时，曾先被其他代理并行编辑产生的两个短暂导入循环与未使用导入阻断；这些编译错误没有当作行为红例，待共享树可构建后才执行上述 overlay 红例。

现由个人 API 的创建、更新、删除及个人 Web 的创建、编辑、轮换、删除共用最终写事务中的当前身份校验：目标必须为活动、可登录的人类个人账号；`sudo` 请求还须原管理员仍活动、可登录且仍为管理员。组织、幽灵及 Actions 账号不能通过个人 API 写应用。API 创建与更新先在事务外生成随机密钥和 bcrypt 哈希，再将应用字段及哈希一起提交，避免两次事务间撤权后的部分写入。正常个人与有效 `sudo` 管理员创建、正常个人更新后哈希变化及删除均为正例；停用账号更新与删除拒绝且原应用保留。

定向绿例命令：

```text
GITEA_TEST_DATABASE=sqlite3 /Users/archer/.cache/gitea-governance/go/bin/go test -tags 'sqlite sqlite_unlock_notify' ./models/auth ./services/auth ./routers/api/v1/user ./routers/web/user/setting -run '^(TestAPIOAuthApplication|TestPersonalOAuthSecretRotation|TestOAuth2Application_GenerateClientSecret|TestCreateOAuth2Application)' -count=1
```

API 更新在同一事务轮换密钥时，专用回归先固定旧 `oauth.application_secret_rotated` 审计事件由一次降为零次的红例；模型更新事务现同时写入 `oauth.application_updated` 和 `oauth.application_secret_rotated`，仍只记录允许的非敏感字段。测试核对轮换事件计数由一次变两次（第一次来自样本初始密钥生成）、新密钥哈希未进入审计详情。修复后六包定向命令通过：

```text
GITEA_TEST_DATABASE=sqlite3 /Users/archer/.cache/gitea-governance/go/bin/go test -tags 'sqlite sqlite_unlock_notify' ./models/auth ./services/org ./routers/web/org ./routers/web/admin ./routers/web/user/setting ./routers/api/v1/user -run '^(TestNativeOAuth|TestOrganizationOAuth|TestInstanceOAuth|TestAPIOAuthApplication|TestPersonalOAuthSecretRotation|TestOAuth2Application_GenerateClientSecret|TestCreateOAuth2Application)' -count=1
```

对本轮文件运行定向 `gofmt`，`git diff --check` 无输出。五项 lint 诊断均位于未由本轮修改的 `models/auth/source.go` 三处、`source_test.go` 一处及 `services/auth/source.go` 一处，本轮 API、个人 Web、OAuth 模型新行与授权服务无报告项。本补充未占共享 PostgreSQL 或活跃 UI；父任务负责最终 HTTP 验收。
