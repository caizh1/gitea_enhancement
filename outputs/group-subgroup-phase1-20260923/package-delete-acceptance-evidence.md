# I-07 Generic 与 OCI 删除闭环客户端验收

日期：2026-09-24。仅操作本机隔离 `127.0.0.1:13043`，固定入口 `gitea-current` 的 SHA-256 为 `e80d141963ce43ff15e9d3e0695eede84062f8a8a803380b025fb4b7069a098f`，与运行中的 v12 记录一致；`/api/healthz` 返回 200。未改成员、共享、群组生命周期、服务配置或旧验收包，也未使用浏览器、PostgreSQL 和生产环境。

## 协议与授权路径

- [Gitea Generic 官方文档](https://docs.gitea.com/usage/packages/generic/)规定按包名与版本执行 `DELETE /api/packages/{owner}/generic/{package_name}/{package_version}`；本次使用原生 HTTP 客户端的 PUT、GET、DELETE，上传明示 `application/octet-stream`。仓库路由先要求包写权，再由 `RemovePackageVersion` 在 `WithAuthenticatedOwnerWrite` 内重新核对当前组织来源和归档／待删状态；下载经包读权检查。
- [OCI Distribution 规范](https://github.com/opencontainers/distribution-spec/blob/main/spec.md#content-management)区分按 tag 和按 digest 的 manifest DELETE；[ORAS 官方删除命令](https://oras.land/docs/commands/oras_manifest_delete/)允许两种引用。本次分别向 ORAS 1.3.4 传 `:v1` 与 `@sha256:…`，不把两个操作当同一语义。Gitea 容器路由先要求包写权，`DeleteManifest` 按当前 tag／digest 找版本，`RemovePackageVersion` 再在短事务内复核当前写权。
- 使用前再次核对官方 macOS arm64 ORAS 1.3.4 发行归档 SHA-256 `217761a9500242ff473de8656b5aca21136ff39e17e9e61fd8936bbfd902704c`，解出的本机可执行文件 SHA-256 `50ae33461ea6e2746b774587d2cb060850961ce01bfd6e70b48d4267919b491c`，版本输出为 `1.3.4 darwin/arm64`。归档与可执行文件摘要不同是正常的，均未替换。官方发行页：[ORAS v1.3.4](https://github.com/oras-project/oras/releases/tag/v1.3.4)。

## 实际命令与首错

执行命令：`python3 -B outputs/group-subgroup-phase1-20260923/package-delete-acceptance.py > /Users/archer/.cache/gitea-phase1-v5-validation/package-delete-v12.json`；脚本经终端无回显读取隔离密码，ORAS 使用标准输入传密，HTTP／ORAS 均绕过代理。首轮退出码 1，原始 JSON SHA-256 `14291e138970ed8a917e574a84240bd10afa2cb662cb1ac563d43f5f5b329c31`，保留未覆盖。

首轮 Generic 全部通过；OCI 发布、读、拒删和 Owner 按 tag 删除也成功，但脚本误要求 ORAS 删除后失败输出必须含数字 `404`。实际 ORAS `pull` 退出码 1，错误为 `not found`，没有数字状态码；一次独立只读拉取复核相同结果。首错是客户端结果分类过严，不是已证明的产品删除失败。首轮新建名称仅为 `phase1-delete-generic-1790184068819120000` 和 `phase1-delete-oci-1790184068819120000`；Generic 1.0.0 与 OCI v1 已由 Owner 删除，测试专用的 Generic 2.0.0、OCI v2/v3 保留，未继续处理 v3，也未触碰既有样本。

随后仅将脚本的 `not found` 归类为客户端 404，使用**新的唯一包名**再次执行该脚本，并把 stdout 保存为 `/Users/archer/.cache/gitea-phase1-v5-validation/package-delete-v12-corrected.json`，退出码 0。该原始结果 SHA-256 为 `34523ede2d2e36d553b751495b2989d3520aba7c27d83f16a233ee100e7e0c9d`；42 项记录中 41 项断言通过、1 项为 digest 可见性观察。脚本 SHA-256 为 `5d72620216332766091796ad1ec4c80f3f859a124fc1d8eb384c3f67ed9fef26`。脚本和两份 JSON 均未写入密码、Basic 凭据或 Bearer token。

## 最终新样本结果

| 协议 | 新对象及有权发布／下载 | 拒绝与删除 | 删除后及旁路对照 |
| --- | --- | --- | --- |
| Generic | `phase1-delete-generic-1790184137128077000`；祖先 Developer 发布 1.0.0、2.0.0 均 HTTP 201；Owner 与继承 Reporter 对 1.0.0 GET 200 且字节一致。 | Reporter 与无关用户删 1.0.0 均 HTTP 401；每次拒删后 Owner 同对象 GET 200。Owner 删指定 1.0.0 为 HTTP 204。 | Owner 同一客户端再次 GET 1.0.0 为 404；2.0.0 GET 200、字节一致，内容 SHA-256 `87e123f53ed40446d0bb666db8afe442b7b699362680b05145635534760725d3`。 |
| OCI tag | `phase1-delete-oci-1790184137128077000`；Developer 用 ORAS 发布 v1/v2/v3 均退出 0，三版本摘要互异；Owner 与 Reporter 对 v1/v3 拉取退出 0、字节一致。 | Reporter、无关用户对 `:v1` 执行 ORAS `manifest delete` 均退出 1／401；每次 Owner 对 v1 拉取仍成功。Owner 按 `:v1` 删除退出 0。 | Owner 再拉 `:v1` 退出 1／`not found`；v2 仍拉取成功。按 tag 删 v1 后再按其原摘要 `manifest fetch` 也返回 `not found`，只记录此唯一摘要样本的实际实现结果，不推定一般的 tag 删除等同 digest 删除。 |
| OCI digest | v3 原摘要 `sha256:28f70bc058d31e9272e1fc5fae3ca4aee85047bcd6f0fe9652f70edea4ef126f`，删除前 Owner／Reporter 均可下载同一内容。 | Reporter、无关用户按该摘要删除均退出 1／401；每次 Owner 对 v3 tag 拉取仍成功。Owner 按摘要删除退出 0。 | Owner 对原 `:v3` 和原摘要拉取均退出 1／`not found`；v2 仍拉取成功，内容 SHA-256 `89b2a5b3cea80469c3ca694c722d2269e3c69b4b91e8ea9748411ff383b36cfe`。 |

OCI 三个原始 manifest 摘要分别为 v1 `sha256:b1c6249f567f7e1e8959ce8e22f21ce8294e8934386777402252e1dd0bee526f`、v2 `sha256:bb5a6f653b47d62e9e0234cb1d070a1430c06d79c174acca460d40f298c13141`、v3 如表。验收前后，旧 `phase1-client-1790170311` 的 Generic GET 200 且 SHA-256 `449fcdcb2b22622ef811a6af1e5ece3e0d92b4d2ed897e3df0ea60e74a2ee6ed` 一致，旧 OCI v1 ORAS 拉取退出 0 且内容一致。拒绝并非对象缺失，成功删除也未误伤旧样本。

## 判定与局限

VERDICT: PASS（仅本机 v12 的这组删除协议样本）。未发现经反证仍成立的 P0／P1。Generic 验证按版本删除；OCI 分别验证 tag 引用与 digest 引用的删除结果，未证明共享同一 manifest 摘要的多 tag 语义或延迟 blob 物理回收。原生制品 UI 删除、并发撤权、归档／待删期间删除、其他格式和目标 Linux 环境未验证，不因本客户端正反例自动通过。
