# 统一成员接口说明

## 查看

`GET /api/v1/governance/repositories/{id}/members/view`

参数：`q` 用户名搜索；`role` 标准角色编号；`source` 来源类型；`after_id` 下一页游标。过滤后每页最多 100 人，响应 `next_id` 非零时继续。合法来源类型为 `direct`、`inherited`、`shared`、`inherited_shared`、`native_team`、`native_collaborator`、`owner`。

返回成员实际角色、账号状态、站点管理员标记、已裁剪来源、可执行操作及禁止原因。`can_manage`、`can_own` 和各来源 `can_edit` 均由服务端计算。`public_projection=true` 表示只按公开可见来源展示，不能用作鉴权依据。组合权限的 `role=0`，须读取 `role_name` 和来源能力明细。

公开访问遵守站点强制登录设置；私有和内部项目遵守现有身份、可见性和受限账号规则。携带 API 令牌时还需通过治理范围校验。旧管理成员接口继续需要管理权限或既有审计权限，不能替代安全只读接口。

## 预览

`POST /api/v1/governance/repositories/{id}/members/preview`

请求使用 `kind` 区分 `member`、`share`、`native`；`remove=true` 表示撤销。成员使用 `member` 对象，包含 `user_id`、`role`、`custom_role_id`、`expires_unix`、`revision`。共享使用 `share` 对象，包含 `group_id`、`max_role`、`expires_unix`、`revision`。也可使用用户名 `username` 或群组完整路径 `group_path` 定位对象。

返回 `token`、逐人 `impacts`、`visibility_notice`、`self_loses_management` 和 `requires_confirmation`。预览不会提交授权、修订号或审计。令牌代表参与权限计算的当前状态；表单变更应重新预览，权限状态变化后旧令牌无法提交。

## 提交

既有成员 PUT/DELETE 和共享接口保留路径；PUT 在对应请求对象中增加 `preview_token`，DELETE 使用查询参数 `preview_token`。旧客户端不带令牌仍须满足原修订号及新的 Owner/来源权限校验。

页面使用 `/{owner}/{repo}/collaborators/preview` 与 `/change`，请求体与预览相同；提交必须携带对应 `member.preview_token` 或 `share.preview_token`。页面写请求保留登录、跨站请求校验。`native` 只允许调整或撤销已经存在的原生协作者，新增授权统一使用 `member`。

有效期是服务端 Unix 秒；0 为永久。页面日期按 UTC 当天结束计算。到期判断不依赖后台清理。

常见响应：参数错误 400；无权限或资源不可见 404；来源变化、修订号变化或永久 Owner 保护 409；原生 API 中已可访问资源但能力不足可能由原有入口返回 403。错误信息说明下一步，不返回完整用户或私有群组记录。
