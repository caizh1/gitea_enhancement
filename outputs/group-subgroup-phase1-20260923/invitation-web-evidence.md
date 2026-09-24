# 邀请邮件落地页与表单反证

## 结论

**VERDICT: PASS。** 当前未发现 Web 邀请落地页或表单的 P0/P1 缺陷，无生产代码修改。真实 UI 中，改用与邀请邮箱匹配的账号重新打开原邮件链接后，页面自动预览并显示邀请资源、邀请人、角色及接受／拒绝按钮。先前使用不匹配账号时，服务端应拒绝令牌；不能从浏览器只读 DOM 中脱敏后的密码输入框空值推断自动读取失败。

## 调用链

- 邮件链接使用 `#token=...`；片段不发送给服务器。落地页 GET 只呈现登录链接或预览表单，设置 `Cache-Control: no-store` 和 `Referrer-Policy: no-referrer`。
- 前端脚本把合规的 64 位十六进制令牌暂存于本标签会话，清除地址栏片段；已登录页面把令牌置入表单正文后自动提交一次预览。登录跳转只包含无秘密的邀请路径。接受或拒绝完成后清除对应暂存令牌。
- 预览、接受、拒绝均从 POST 正文取 `token`。预览与最终接受都重新验证令牌、账号已验证邮箱和资源授权；错邮箱返回 404，不暴露邀请信息。错误响应不是再次渲染预览表单，因此未发现自动提交循环。最终接受成功后邀请不可重放。

## 定向 HTTP 验证

新增 `TestGovernanceInvitationWebForm`，覆盖已登录错邮箱账号 GET 落地页、POST 预览 404、POST 接受 404、成员不变；随后匹配账号 GET、POST 预览 200、POST 接受 200、重复接受 404。测试仅断言令牌未出现在初始 GET 正文，不在失败输出中打印令牌。既有 `TestGovernanceInvitationHTTP` 同时保留 API 与 Web 的分工检查。

执行：`GITEA_TEST_DATABASE=sqlite /Users/archer/.cache/gitea-governance/go/bin/go test -count=1 -run '^TestGovernanceInvitation(HTTP|WebForm)$' ./tests/integration`，结果通过。对 `./tests/integration` 的 golangci-lint 检查显示新增测试零诊断；另有 3 条位于其他文件的告警，见 `governance_release_archive_test.go`、`org_team_invite_test.go`、`unified_branch_approval_test.go`。

## 边界

没有操作运行中的实例、浏览器或配置；没有读取、记录或输出真实邮件令牌。真实浏览器匹配账号预览的观察由主任务完成，HTTP 测试使用独立测试数据库。
