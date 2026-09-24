# I-07 包协议与相邻资源授权证据

## 结论与范围

VERDICT: PASS（本轮源码审查未发现成立的 P0/P1；真实 UI、OCI/Generic 客户端及 Runner 尚未验收）。

P0/P1 BLOCKERS: 无。

本轮保留 Gitea 的单段包协议命名空间及原存储所有者。群组页面显示的层级路径用于导航；包详情中可复制的 Generic URL 使用 `Owner.Name`，OCI 命令使用 `Owner.LowerName`，二者是实际协议命名空间，不把 `FullPath` 填进客户端命令。未建设跨协议层级路由。

## 已闭合的源码链

- Web 包页、包协议路由和 API v1 都进入 `PackageAssignment` / `PackageAssignmentAPI`；读取使用当前群组有效 `ReadPackages`、原生 Team Packages 单元与可见性，写入使用 `WritePackages` 和 Team Packages 单元，归档或待删祖先把写权限降为只读。共享 Reporter 只能读，父组 Owner 可管理子组，子组 Owner 不能越树写父组或兄弟组。
- Generic 与 OCI 等协议在入口校验当前主体；上传正文先以内容哈希暂存到对象存储，再在短治理事务中重新检查主体、群组生命周期、配额、重复版本和权限，原子建立包、版本、文件引用。撤权发生在正文写入时，最终关系提交被拒绝。失败留下的孤立正文交给既有定时清理；新暂存行和同哈希行刷新创建时间，未引用 blob 至少保留 24 小时，避免清理并发误删正在上传的正文。这是即时清理行为的兼容差异。
- 显式用户删除及 Web/API/OCI/Generic 文件删除在短事务复核真实用户当前能力与归档状态；内部清理经显式 cleanup 路径调用。无主体的 HTTP 写入不能按系统清理处理。
- OCI blob 跨镜像挂载按 blob 当前关联的包所有者重新判定读权，包含父组与共享只读来源；共享撤销后不能继续挂载私有 blob。
- CI 任务令牌保留任务 ID，而非把 synthetic 用户当成普通 Owner。每次协议授权及最终写事务复核任务/Runner 状态、触发者的源仓可读权、YAML 与祖先 token 权限上限、同包所有者范围，以及当前触发者对目标包命名空间的 Packages 能力。仅有仓库协作写权不允许写整个群组包空间；取消中的任务不能发布，停用 Runner 后失效。定时任务没有真实触发者的包空间写权，不凭系统身份扩权。

## 定向自动化结果

- `go test ./models/packages ./services/context ./services/packages ./routers/api/packages/... -count=1`：通过。覆盖父/子/兄弟与共享授权及撤权、仅仓库协作者负例、YAML 降权、取消与 Runner 停用、包删除撤权、正文写入期间治理写锁可继续取得、上传中撤权不留包引用、blob 宽限期。
- `golangci-lint run ./models/packages ./services/context ./services/packages ./routers/api/packages/container`：0 issues。
- `git diff --check`（上述目标文件）：通过。仅目标文件 gofmt；未运行共享 PostgreSQL、浏览器或全仓格式化。

## 相邻资源静态审查与待验收

- LFS 通过当前仓库读取/写入权限及任务令牌仓库权限判定；Release 的 Web/API 操作经过当前仓库 Releases 单元权限；Actions artifact 的任务令牌路径检查当前任务凭据及运行关联。本轮未修改这些并行公共文件，也未运行真实客户端。
- 真正的 Generic 最小入口：在子群组包页复制 `Owner.Name` 的 `/api/packages/<owner>/generic/...` URL，用父 Owner、子 Owner、共享只读、撤权用户分别执行 PUT/GET/DELETE。OCI 最小入口：复制包页 `Owner.LowerName` 的 `docker login/push/pull` 命令，重复上述身份，并检查跨镜像 mount 与撤权。Runner 还需用实际任务 token 验证 YAML `packages: write`、只读 YAML、仅仓库协作者、取消与停用。
- 浏览器包页、真实 Generic/OCI 客户端、真实 Runner、对象存储 S3/MinIO 和预签名下载 URL 在撤权后的剩余有效期均未实测；不得将源码与 SQLite 定向测试视为交付验收或人工精力减少 80% 的证明。

NON-BLOCKING FINDINGS: 失败上传的内容寻址孤立正文最多等待既有清理周期及至少 24 小时宽限；部署需保留清理任务。协议单段命名空间和群组显示层级路径不同，客户端须使用页面给出的实际命令。

UNVERIFIED RISKS: 真实对象存储的一致性与预签名 URL 在撤权后的时效、真实 Runner 的完整鉴权链、旧路径与匿名下载的线上行为，需父任务的协议与 UI 验收确认。
