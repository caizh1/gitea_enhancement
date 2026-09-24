# 工作流产物下载 404 反证

## 结论

**VERDICT: PASS。** 本次未发现产物下载的 P0/P1 代码缺陷，不修改生产处理器或数据模型。最初浏览器下载事件超时被误判为 HTTP 下载失败；随后服务重启令浏览器会话失效，使用 Basic Auth 请求普通 Web 页面得到的 404 是仓库分配阶段的匿名拒绝，不是产物查询返回空。

## 真实路径证据

- 只读 SQLite 核对：仓库 2 的运行 30（index 11）最新尝试 ID 为 33；尝试 33 是 attempt 1；产物 2 的名称为 `phase1-ancestor-result`，状态 2（上传已确认），文件路径为 `result.txt`。名称字节无隐藏字符。
- 失败日志：`GET /phase1-parent/child/deep/ancestor-ci/actions/runs/30/artifacts/phase1-ancestor-result?attempt=1` 返回 404，处理位置为 `context.RepoAssignment`，尚未进入 `ArtifactsDownloadView`。记录位于隔离实例的 `log/router-artifact.log`。
- 重新登录后的真实浏览器点击命中 `actions.ArtifactsDownloadView` 并返回 200。两次真实 ZIP 均含 46 字节 `result.txt`，其 SHA-256 为 `8d5f32877961a67c5e142de8887505f68b94a242f9400028a7222b8a4bb03bc9`，与工作流结果相符；其中一次原始下载的文件时间早于错误报告。

## 定向回归

新增 `TestWebArtifactDownloadUsesRunIDAndAttemptNumber`：在私有个人仓库和原生组织仓库中，允许读者下载 attempt 1、attempt 2 和默认最新产物，逐一核对 ZIP 内正文；无效 attempt、无权用户和跨仓库运行 ID 均返回 404。命令：

`GITEA_TEST_DATABASE=sqlite /Users/archer/.cache/gitea-governance/go/bin/go test -count=1 -run '^TestWebArtifactDownloadUsesRunIDAndAttemptNumber$' ./tests/integration`

结果：通过。临时模型查询镜像测试已删除，因为实际查询和消费者均通过，未成立的怀疑不应导致生产改动。

对 `./models/actions ./tests/integration` 执行定向 golangci-lint 后有 5 条其他文件问题（`governance_audit.go`、`runner.go`、`governance_release_archive_test.go`、`org_team_invite_test.go`、`unified_branch_approval_test.go`）；新增测试文件没有 lint 诊断。首次执行未设置专用 Go 路径，工具报 `go` 不在 PATH；补上 `/Users/archer/.cache/gitea-governance/go/bin` 后取得上述实际 lint 结果。

## 边界

本次不改运行实例、SQLite 测试数据、Web 路由、产物模型或 Basic Auth 策略。以真实浏览器下载 200 和正文散列作为完成证据；其他协议或用户角色未在此次定向回归中扩展。
