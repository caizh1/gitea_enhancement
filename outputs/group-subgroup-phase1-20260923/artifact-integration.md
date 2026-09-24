# 作业凭据及产物集成验证

日期：2026-09-23。仅使用本次独立 PostgreSQL／MinIO 测试实例，没有访问生产数据。

## 首因与修正

- 新鉴权要求合法 Runner、任务、Job、Run 和当前仓库归属一致。原产物 fixture 中任务 47／48 的 OwnerID 与仓库不一致，并引用不存在的 Runner。新增测试准备函数补齐正例关系；没有放宽生产鉴权，也没有修改公共 fixture 文件。
- 新增归属改变、Runner 停用、仓库归档、触发人停用四组测试。每组先证明原生任务 token 与 JWT 可访问，再要求状态改变后返回 401。
- 原生 token 无效分支最初返回 500，JWT 分支返回 401。修复仅将明确的无效凭据错误映射为 401；其他数据库错误保留 500。
- 原工作区集成测试中跨仓库正例失败：测试框架调用根目录现有 `gitea`，该文件是 Linux x86_64 ELF，在 macOS 的 Git hook 中报 `Exec format error`。未替换用户二进制。

## 隔离副本复验

将当前跟踪源码、新增 Go 文件及已有前端资源复制至工作区外 `/Users/archer/.cache/gitea-phase1-integration-lco96594`。副本根目录放置本批 `go build` 的 macOS arm64 二进制。没有创建分支、提交或修改原根目录二进制。

实际命令如下；测试口令仅由运行环境提供，记录中省略。

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH \
GITEA_TEST_DATABASE=pgsql TEST_PGSQL_HOST=127.0.0.1:15433 \
TEST_PGSQL_DBNAME=gitea_phase1_test TEST_PGSQL_USERNAME=phase1_test \
TEST_PGSQL_SCHEMA=public TEST_MINIO_ENDPOINT=127.0.0.1:19000 \
go test -count=1 -run '^TestActions(Artifact|JobSummary|JobToken|CrossRepoAccess)' ./tests/integration
```

结果：退出 0，`ok gitea.dev/tests/integration 18.598s`。

覆盖匹配的现有产物上传下载、摘要、任务 token 权限和跨仓库访问集成测试，以及新增失效凭据正反例。测试由 HTTP 测试客户端执行，不冒充真实软件包客户端、浏览器产物下载、完整祖先继承或全部签名链接撤权验收。此结果先于随后独立审查发现的定时身份修复；最终相关回归另行记录。
