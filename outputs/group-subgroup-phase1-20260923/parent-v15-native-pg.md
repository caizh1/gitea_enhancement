# 第十五批原生设置 PostgreSQL 验收

本记录仅覆盖原生应用／屏蔽的本批子项，不是一期交付完成结论。运行环境是本机 arm64 应用与隔离 PostgreSQL 17（端口 15433、数据库 gitea_phase1_test）、MinIO，不是约定的 Linux amd64 容量环境。

## 实际执行

使用仓库独立构建副本、Go 1.26.4，设置 `GITEA_TEST_DATABASE=pgsql` 和隔离连接参数，关闭本机代理。命令：

```text
go test -tags='bindata sqlite sqlite_unlock_notify' -count=1 -json -run '^(TestNativeOrgSettingsAncestorLifecycle|TestBlockUser|TestUserSettingsApplications|TestOAuth2Application|TestGovernanceOwnerIdentityRealDatabase)$' ./tests/integration
```

五个顶层测试通过；引擎用例实际断言并输出 `postgres / gitea_phase1_test`。原始日志保存在本机私有缓存 `native-pg-final.jsonl`，不把其中测试认证响应复制到交付文档。

新增 HTTP 集成用例创建父子群组，验证普通父级 Owner 创建与编辑子组 OAuth 应用；归档父组后创建、编辑、轮换被拒且名称／哈希未变，原生组织 API 解除屏蔽返回 409。当前 Owner 仍可删除应用以撤权；恢复后编辑与解除屏蔽恢复可用。无关用户 UI 读取／写入为 404。管理员经真实 API 的 `Sudo` 组织身份创建、编辑和删除应用均为 404，不能绕入个人 API。

## 审计红绿

组织应用旧审计范围为 `user`，新增用例要求 `group` 时失败（实际 user、预期 group）。修复后同用例验证组织范围及父级审计投影存在，详情不包含密钥哈希；个人应用原有 HTTP 回归同时通过。表前缀疑虑经反证撤回：当前项目未支持该配置，PostgreSQL 自定义 schema 由连接 search_path 处理，不凭假设新增兼容改造。

## 限制

这是 HTTP 路由与数据库集成，不代替真实浏览器同对象验收；后者另见 parent-v15-native-ui.md。旧请求身份撤销的固定回归来自子代理 SQLite 测试，本记录不把它冒称成 PostgreSQL 并发屏障。CI 分支同步和计划版本的本批修复尚在并行验证，另记证据。容量和 80% 人工效率仍未验证。
