# I-07 OCI 与 Generic 客户端验收证据

时间：2026-09-23。实际目标是 `127.0.0.1:13043` 的 v4，本代理对 `outputs/group-subgroup-phase1-20260923/gitea-phase1-ancestors-v4` 独立执行 `shasum -a 256`，得到 `0cf7042d326e4dd146e8a18e6052c4d80b8411569e1ad4d8e648bd7bcffaf013`，与父任务提供的运行版本摘要一致。HTTP 客户端及 ORAS 子进程均明确绕过代理。

## 准备与来源

- 本机未安装 `oras`、`crane` 或 `skopeo`；已有 Docker CLI 与 curl。按 [ORAS 官方安装说明](https://oras.land/docs/installation/) 与[官方 1.3.4 发布资产](https://github.com/oras-project/oras/releases/tag/v1.3.4)，只在独立缓存下载 `darwin/arm64` 的 ORAS 1.3.4。实际 SHA-256 为 `217761a9500242ff473de8656b5aca21136ff39e17e9e61fd8936bbfd902704c`，与官方发布页一致；`oras version` 显示 1.3.4、darwin/arm64。未修改仓库依赖或全局客户端。
- Generic 路径及 PUT/GET 行为依据 [Gitea 官方 Generic 文档](https://docs.gitea.com/usage/packages/generic/)；OCI 命名遵循 [Gitea 官方 Container 文档](https://docs.gitea.com/usage/packages/container/)，本地明文 HTTP 选项与标准输入密码用法依据 [ORAS 官方命令文档](https://oras.land/docs/commands/oras_push/)。
- 准备脚本 `package-client-acceptance.py` 已通过 `python3 -m py_compile`，无需在命令行、脚本文件或本记录保存隔离测试密码。脚本运行时无回显输入；ORAS 认证配置、上传样本和下载文件均写入自动清理的临时目录。结果只包含状态码、退出码、样本摘要和已脱敏错误摘要。

## 真实执行与首错纠正

- `curl --noproxy '*' -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:13043/api/healthz` 返回 `200`。
- 实际命令：`python3 -B outputs/group-subgroup-phase1-20260923/package-client-acceptance.py baseline`。脚本在终端无回显读取隔离凭据；此处不记录凭据。
- 首次 `baseline` 使用包名 `phase1-client-1790170174`：OCI 路径通过，但 Generic Developer PUT 为 `500`。父任务从 v4 控制台取到首错 `request Content-Type isn't multipart/form-data`。静态核对发现 Python `urllib.request.Request(data=..., method='PUT')` 未指定类型时把原始字节误标成 `application/x-www-form-urlencoded`；`Context.UploadStream` 因此按表单解析。此请求不符合官方 Generic 原始文件上传示例，属于验收客户端脚本错误，撤回“产品 Generic 500 缺陷”判断。没有修改业务代码，也没有对旧包名盲目重试。
- 修正脚本 Generic PUT 为 `Content-Type: application/octet-stream`，并用独立包名 `phase1-client-1790170311` 仅执行一次规范 `baseline`：脚本退出码 `0`。样本 SHA-256 为 `449fcdcb2b22622ef811a6af1e5ece3e0d92b4d2ed897e3df0ea60e74a2ee6ed`。
- Generic：祖先 Developer 发布 `201`；原生组织 Owner、Developer、从 child5 继承 Reporter 的 `phase1-reader` 下载均 `200` 且内容 SHA 一致；Reporter 拒写 `401`；仅凭 project2 共享的 `phase1-shared` 与无关用户对 deep6 包命名空间拒读拒写均 `401`。
- ORAS OCI：祖先 Developer 发布退出码 `0`；Owner、Developer、继承 Reporter 下载退出码 `0` 且内容一致；Reporter 拒写及仅 project2 共享成员、无关用户拒读拒写均返回 OCI `401`。这些拒绝是在同一已由 Owner 成功下载的对象上验证，不是缺失对象导致的假阴性。

## 群组共享受限读权

- `baseline` 已完成；规范请求结果如上。
- 父任务已用普通 Owner 浏览器在 group6 共享页保存 deep6→group7、上限 Reporter，刷新回显目标稳定 ID 7 与 Reporter；原 project2 共享保持。随后执行 `python3 -B outputs/group-subgroup-phase1-20260923/package-client-acceptance.py shared --package phase1-client-1790170311`，退出码 `0`。
- `phase1-shared` Generic GET `200` 且字节一致、PUT `401`；ORAS OCI pull 退出码 `0` 且字节一致、push 返回 OCI `401`。Owner 对照 Generic GET `200`、OCI pull 退出码 `0` 且字节一致。此正例来自群组共享 Reporter 上限，不把单仓 project2 共享误判为整个包命名空间的来源。

## 撤权复核

- 父任务在 group6 共享页撤销临时 group7 Reporter 共享，刷新回显暂无共享关系；project2 原仓库共享保持。随后执行 `python3 -B outputs/group-subgroup-phase1-20260923/package-client-acceptance.py revoked --package phase1-client-1790170311`，退出码 `0`。
- `phase1-shared` Generic GET/PUT 均 `401`；ORAS OCI pull/push 均返回 `401`。同一时刻原生组织 Owner 对照 Generic GET `200`、OCI pull 退出码 `0` 且内容一致，证明拒绝不是对象消失或服务不可用。

## 未验证

CLI 三阶段均已完成；本代理未自行改变成员或共享关系。Generic 和 OCI 正例已真实比较下载字节。父任务另用浏览器从 group6 制品入口进入原生包列表与 Generic 详情，实际看到 Developer 发布、Generic 与 OCI 共三条包、真实路径及下载入口正确；这是父任务的页面操作证据，与本代理的 API/CLI 验收分别记录。原生 UI 写入或删除按钮、其他包格式、任务令牌、跨实例和生产环境未验证。

## 对抗式审查

VERDICT: PASS

P0/P1 BLOCKERS：无。已反证“单仓共享泄露整个包命名空间”“Reporter 可写包”“撤权后旧凭据仍可取包”“OCI 拒绝只是对象缺失”等候选缺陷；每个拒绝阶段均有同一对象 Owner 成功下载作对照。首次 Generic `500` 的首因是本验收脚本错误标记 Content-Type，规范原始上传 `201` 已复核，不是已证实的产品缺陷。

UNVERIFIED RISKS：原生制品 UI 写入或删除操作、其他包格式、真实 Actions 任务令牌、生产数据规模与外部 TLS 终端未验证，不影响本客户端矩阵结论。

NON-BLOCKING FINDINGS：客户端脚本使用系统标准库 Generic 请求和官方 ORAS 1.3.4；临时群组共享在验收后已由父任务撤销。
