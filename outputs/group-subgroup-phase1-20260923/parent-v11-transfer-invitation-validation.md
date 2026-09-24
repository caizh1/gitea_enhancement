# 第十一版：仓库邀请入口及同名子群组转移

日期：2026-09-24。仅本机 UI、合成邮箱、隔离 SMTP、测试 Git 仓库。

## 修复与反证

`ChangeNativeRepositoryPath` 的返回值是所有者路径。旧 `services/repository/transfer.go` 把它作为完整仓库路径传给邀请同步，导致转移后 `Invitation.ScopePath` 缺少仓库末段。现在直接保存 OwnerNamespace，并给同步服务传入 `repo.FullPath()`，底层函数契约不变。

父任务独立核对 `models/repo/update.go`：原代码的临时 TrimSuffix 赋值会在模型更新 Owner 时重新同步，因此**没有证据说明最终仓库 OwnerNamespace 曾被错误持久化**。稳定旧代码红例是邀请路径缺少末段。测试覆盖目标子群组末级与仓库同名、申请仍保留旧路径、申请人无读取权，以及当前 Owner 按资源 ID 撤销邀请和拒绝申请。[红例](repository-transfer-ownerpath-red.log)、[绿例](repository-transfer-ownerpath-green.log)保留；四项仓库服务定向回归通过，包耗时 1.233 秒。

另一处 UI 缺口由父任务实际发现：治理仓库成员页已经重定向到当前协作者页，邮箱邀请入口却只留在旧模板。当前页面现在在既有 `MemberView.CanManage` 条件下，用正常流 flex 加入“通过邮箱邀请成员”，指向现有邀请页。SQLite HTTP 回归先红后绿；无管理权用户看不到入口，直接请求为 404。第十一版真实 PG 同用例通过。

## 真实浏览器与邮件流程

1. 普通 Owner 经独立对照群组 7 的“创建子群组”建立群组 16，路径 `phase1-other/request-private`。末级名称与待转移仓库 12 相同；没有 Runner、部署密钥、Hook 或工作流来源。
2. v11 从仓库 12 协作者页点击新邮箱邀请入口，邀请 `phase1-outsider@example.invalid` 为 Reporter。页面先显示待投递；本机 SMTP 接收器实际收到邀请 3。
3. Owner 从仓库原生设置、Transfer Ownership、目标输入及预览依次操作。预览明确显示来源 `phase1-request-private/request-private`、目的 `phase1-other/request-private/request-private`，列出祖先及成员变化、零待取消任务、旧地址兼容规则。确认后页面提示已转移，设置地址为完整目的路径。
4. 收件人登录后打开本机接收器中的同一邀请链接。预览正确显示 `phase1-other/request-private/request-private`，仓库末段完整，没有把目标群组当作仓库。随后用户点击拒绝邀请，页面显示“邀请已拒绝”。没有新增成员权限。

邀请预览当前仍使用“邀请时的资源”文案，但其路径按已定义语义随资源更新，文案需后续明确为当前邀请目标；这是非阻塞显示问题，不据此宣称路径永远是创建时快照。访问申请采用独立的创建时路径语义，不混用。

## Git 与数据库佐证

对新路径以及旧别名 `phase1-request-private/request-private` 分别执行实际 `git ls-remote … HEAD`，明确禁用系统凭据 helper：

| 身份 | 新路径 | 旧别名 |
| --- | --- | --- |
| 普通 Owner | 退出 0，提交 `35c9015118ae2c7dfe668467e7034147ba089fba` | 退出 0，同一提交 |
| 尚未接受邀请的收件人 | 退出 128，仓库不可见 | 退出 128，仓库不可见 |

只读数据库确认仓库 12 所有者为 16，OwnerNamespace 为 `phase1-other/request-private`、仓库名 `request-private`，仍为私有；邀请 3 的 ScopeID 仍为仓库 12，ScopePath 为完整目的路径。原始客户端记录 `/Users/archer/.cache/gitea-phase1-git-7n1x_l_6/v11-transfer-invitation-git.json`。未手工修改数据库。

这只证明本样本同名子群组转移、邀请预览及 HTTP Git 正反例。邀请接受后的全部角色、过期、SSH／LFS／包旧别名、项目看板关联及完整转移生命周期仍需各自验收。构建沿用 v11，SHA 及部署边界见[Owner 与生命周期记录](parent-v11-owner-lifecycle-validation.md)。
