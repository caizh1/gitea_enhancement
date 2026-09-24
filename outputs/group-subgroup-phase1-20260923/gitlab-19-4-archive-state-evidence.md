# GitLab 19.4 群组归档状态核对

核对日期：2026-09-24。固定对象为 GitLab 官方仓库标签 [`v19.4.0-ee`](https://gitlab.com/gitlab-org/gitlab/-/tags/v19.4.0-ee)，提交 `fa703f93afaf1133cd263f6aa0e91d284d7f1ebf`。只读核查官方文档、该标签源码与测试；没有运行 GitLab 实例或测试。

## 版本、档位与文档语义

[固定版本的群组管理文档](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.4.0-ee/doc/user/group/manage.md#L100-149)记载：群组归档 UI 在 18.3 引入，18.9 正式可用并移除 `archive_group` 特性开关；归档使群组及后代只读，解档群组及其后代。官方[18.9 发布说明](https://docs.gitlab.com/releases/18/gitlab-18-9-released/#archive-a-group-and-its-content)把这项功能列为 Free、Premium、Ultimate，包含 GitLab Self-Managed。19.4 文档中另述的“19.4 正式可用”属于**异步群组转移**，不是群组归档。

文档只说解档后代，没有解释归档前已经独立归档的后代会否保持原状态；下面以固定源码和测试判定。

## 独立归档的后代不会保持原标记

- [归档服务第 24–34 行](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.4.0-ee/app/services/namespaces/groups/archive_service.rb#L24-34)：父组归档事务内先归档父组，再调用 `unarchive_descendants!` 与 `unarchive_all_projects!`。这一步已经清除后代自身的独立归档标记；父组归档期间仍由父组状态使其有效只读。
- [Group 模型第 1302–1314 行](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.4.0-ee/app/models/group.rb#L1302-1314)：批量把后代群组的 `namespace_settings.archived` 设为 `false`、独立 `archived` 状态改为 `ancestor_inherited`，并把范围内项目的 `archived` 设为 `false`、项目命名空间独立归档状态改为 `ancestor_inherited`。
- [归档服务测试第 52–57、136–147 行](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.4.0-ee/spec/services/namespaces/groups/archive_service_spec.rb#L52-57)预置已独立归档的子组与项目，[断言](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.4.0-ee/spec/services/namespaces/groups/archive_service_spec.rb#L136-147)父组归档后其独立标记均为 `false`。[解档服务测试第 43–57、91–104 行](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.4.0-ee/spec/services/namespaces/groups/unarchive_service_spec.rb#L43-57)再次预置旧独立标记，[断言](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.4.0-ee/spec/services/namespaces/groups/unarchive_service_spec.rb#L91-104)父组解档后后代群组、项目均非独立归档。[解档服务第 21–29 行](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.4.0-ee/app/services/namespaces/groups/unarchive_service.rb#L21-29)与此一致。

因此在该固定版本中，原独立归档的子群组和项目**不会在父组归档→解档后保持独立归档**。这与当前一期“统一恢复后代并在 UI 预告”方向一致；不据此声称当前 Gitea 实际 UI 已验。

## 待删除项目边界

[归档服务第 28 行](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.4.0-ee/app/services/namespaces/groups/archive_service.rb#L24-34)阻止目标群组或祖先已经安排删除，但没有对后代项目逐一做待删拒绝。[项目待删服务第 19–49 行](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.4.0-ee/app/services/projects/mark_for_deletion_service.rb#L19-49)设置 `marked_for_deletion_at` 并进入删除状态；[项目恢复服务第 27–45 行](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.4.0-ee/app/services/projects/restore_service.rb#L27-45)才取消删除并清空该字段。父组归档／解档的批量清理只处理 `archived` 与命名空间的独立归档状态，不调用项目恢复服务，也不清空 `marked_for_deletion_at`；[Project 模型第 3188–3195 行](https://gitlab.com/gitlab-org/gitlab/-/blob/v19.4.0-ee/app/models/project.rb#L3188-3195)还把自身安排删除的项目排除出有效 `archived` 判断。

据此可确定父组解档**不等于恢复待删除项目**，其删除标记仍在。已读归档／解档服务测试未包含“子项目待删除同时父组归档”的专门断言；该具体混合状态的端到端表现未作实测，不扩写为已验证。
