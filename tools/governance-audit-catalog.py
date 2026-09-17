#!/usr/bin/env python3
"""从冻结的 GitLab 19.3.2 审计定义生成中文对照清单，不把目录存在视为实现完成。"""

import argparse
import hashlib
import json
from pathlib import Path
import re
import tarfile
from collections import Counter

ROOT = Path(__file__).resolve().parents[1]
MAPPINGS = {}


def mapped(names, event, evidence):
    for name in names.split():
        MAPPINGS[name] = (event, evidence)


mapped("protected_branch_created", "repository.branch_protection_created", "分支保护审计实施记录.md；原生共用模型事务事件，最终镜像复验中")
mapped("protected_branch_removed", "repository.branch_protection_deleted", "分支保护审计实施记录.md；删除前保存历史规则，审计失败整体回滚")
mapped("protected_branch_updated protected_branch_allow_force_push_updated", "repository.branch_protection_updated", "分支保护审计实施记录.md；规则字段、顺序及名单清理的前后值；全部入口矩阵待完成")
mapped("repository_download_operation", "access.git_http / access.git_ssh / access.archive", "访问审计接入实施记录.md；已认证 HTTP、SSH Git 和归档入口已有真实样本，完整拒绝与协议覆盖待完成")
mapped("repository_file_accessed_web", "access.code", "访问审计接入实施记录.md；代码页面和原始下载已接入，其他查看入口待核对")
mapped("repository_file_accessed_api", "access.code", "访问审计接入实施记录.md；原始文件、媒体、单文件、目录、批量及 Git 对象 API 已接入，完整协议覆盖待完成")
mapped("add_ssh_key", "credential.ssh_key_created", "SSH密钥审计实施记录.md；原生网页、API、代办及来源同步已接入，完整部署环境复验中")
mapped("remove_ssh_key", "credential.ssh_key_revoked", "SSH密钥审计实施记录.md；原生撤销、来源同步与用户删除已接入，完整批量入口待验收")
mapped("deploy_key_added", "credential.deploy_key_created", "SSH密钥审计实施记录.md；共享公钥及项目绑定分开审计，镜像 SSH 复验中")
mapped("deploy_key_removed", "credential.deploy_key_revoked", "SSH密钥审计实施记录.md；原生撤销与项目删除批量入口已接入，最终镜像复验中")
mapped("personal_access_token_created", "credential.access_token_created", "凭据审计实施记录.md")
mapped("user_auditor_status_updated", "user.auditor_changed", "全站审计员实施记录.md；原生管理员页面与接口、同事务审计及撤权校验已接入，最终镜像复验中")
mapped("user_admin_status_updated user_blocked user_deactivate", "user.security_changed", "账户安全审计实施记录.md；账号安全字段、状态及权限变更同事务审计，外部同步与全部身份入口待补齐")
mapped("user_password_updated", "credential.password_changed", "账户安全审计实施记录.md；密码保存、持久登录凭据撤销及审计同事务；网页、API、命令和恢复入口已接入")
mapped("user_enable_two_factor", "credential.totp_enabled / credential.webauthn_created", "账户安全审计实施记录.md；TOTP 与通行密钥登记接入共享模型，真实硬件通行密钥验收待完成")
mapped("user_disable_two_factor", "credential.totp_disabled / credential.webauthn_revoked", "账户安全审计实施记录.md；个人停用、管理员重置及账号删除共用事务审计")
mapped("personal_access_token_revoked", "credential.access_token_revoked", "凭据审计实施记录.md")
mapped("authenticated_with_password authenticated_with_ldap authenticated_with_oauth authenticated_with_two_factor authenticated_with_webauthn", "authentication.login_authorized / authentication.login_succeeded", "凭据审计实施记录.md；仅已接入共用交互会话入口，各认证来源仍需独立验收")
mapped("login_failed_with_otp_authentication login_failed_with_webauthn_authentication", "authentication.login_failed", "凭据审计实施记录.md；已验证 TOTP、恢复码及 WebAuthn 非法响应，其他故障路径待补齐")
mapped("login_failed_with_standard_authentication", "authentication.login_failed", "凭据审计实施记录.md；当前覆盖密码表单，其他入口待补齐")
mapped("group_archived", "group.archived", "群组生命周期实施记录.md")
mapped("group_unarchived", "group.unarchived", "群组生命周期实施记录.md")
mapped("project_archived", "repository.archived", "治理验收记录/20260915-原生归档并发与审计.json")
mapped("project_unarchived", "repository.unarchived", "治理验收记录/20260915-原生归档并发与审计.json")
mapped("group_created", "group.created", "层级权限与原生评审实施记录.md；治理群组创建入口")
mapped("group_name_updated group_path_updated group_description_updated group_visibility_level_updated", "group.updated / group.transferred", "层级权限与原生评审实施记录.md；具体字段及原生组织入口待逐项核对")
mapped("project_deletion_marked", "repository.deletion_scheduled", "项目删除实施记录.md；双数据库真实 Git 与恢复通过，最终镜像与通知待完成")
mapped("project_restored", "repository.restored", "项目删除实施记录.md；原路径、归档状态、到期撤权恢复已有固定用例")
mapped("group_deletion_marked", "group.deletion_scheduled", "删除恢复实施记录.md；群组保留期服务与真实 Git 验收通过，完整原生入口镜像复验中")
mapped("group_restored", "group.restored", "删除恢复实施记录.md；恢复路径及原有归档、到期撤权恢复已有服务用例，完整事件目录仍在补齐")
mapped("group_destroyed project_destroyed", "resource.deletion_committed / resource.cleanup_completed", "删除恢复实施记录.md；原生删除与恢复任务已有验收，延迟删除和完整生命周期待完成")
mapped("member_created", "member.added", "群组生命周期实施记录.md；治理直接成员入口")
mapped("member_updated", "member.updated", "群组生命周期实施记录.md；治理直接成员入口")
mapped("group_request_access_enabled_updated", "access_request.setting_changed", "访问申请与邀请实施记录.md；群组申请设置事务事件已接入，完整通知和生命周期待完成")
mapped("member_destroyed", "member.removed；member.expired", "群组生命周期实施记录.md；授权到期实施记录.md；治理直接成员与到期清理入口")
mapped("member_role_created", "role.created", "层级权限与原生评审实施记录.md")
mapped("member_role_updated", "role.updated", "层级权限与原生评审实施记录.md")
mapped("member_role_deleted", "role.removed", "层级权限与原生评审实施记录.md")
mapped("group_share_with_group_link_created project_group_link_created", "share.created", "层级权限与原生评审实施记录.md")
mapped("group_share_with_group_link_updated project_group_link_updated", "share.updated", "层级权限与原生评审实施记录.md")
mapped("group_share_with_group_link_removed project_group_link_deleted", "share.removed", "层级权限与原生评审实施记录.md")
mapped("approval_rule_created approval_rule_deleted update_approval_rules", "approval.rule_changed", "层级权限与原生评审实施记录.md")
mapped("allow_author_approval_updated allow_committer_approval_updated allow_overrides_to_approver_list_per_merge_request_updated group_merge_request_approval_setting_created project_disable_overriding_approvers_per_merge_request_updated project_merge_requests_author_approval_updated project_merge_requests_disable_committers_approval_updated project_require_password_to_approve_updated project_reset_approvals_on_push_updated require_password_to_approve_updated require_reauthentication_to_approve_updated retain_approvals_on_push_updated", "approval.settings_changed", "层级权限与原生评审实施记录.md；设置范围与锁定更新路径待完整矩阵核对")
mapped("merge_request_approval_operation", "approval.approved / approval.withdrawn", "治理验收记录/20260915-原生评审与代码失效审计.json")
mapped("merge_request_merged", "merge.authorized / merge.reconciled", "治理验收记录/20260915-最终授权与真实引用恢复.json")
mapped("security_policy_create security_policy_delete security_policy_update", "approval.policy_changed", "仅映射本次强制审批策略；扫描及其他安全策略不属于本次范围")
mapped("amazon_s3_configuration_created amazon_s3_configuration_deleted amazon_s3_configuration_updated instance_amazon_s3_configuration_created instance_amazon_s3_configuration_deleted instance_amazon_s3_configuration_updated google_cloud_logging_configuration_created google_cloud_logging_configuration_deleted google_cloud_logging_configuration_updated instance_google_cloud_logging_configuration_created instance_google_cloud_logging_configuration_deleted instance_google_cloud_logging_configuration_updated create_event_streaming_destination create_instance_event_streaming_destination created_group_audit_event_streaming_destination created_instance_audit_event_streaming_destination destroy_event_streaming_destination destroy_instance_event_streaming_destination deleted_group_audit_event_streaming_destination deleted_instance_audit_event_streaming_destination update_event_streaming_destination update_instance_event_streaming_destination updated_group_audit_event_streaming_destination updated_instance_audit_event_streaming_destination audit_events_streaming_headers_create audit_events_streaming_headers_destroy audit_events_streaming_headers_update audit_events_streaming_instance_headers_create audit_events_streaming_instance_headers_destroy audit_events_streaming_instance_headers_update event_type_filters_created event_type_filters_deleted", "audit.stream_changed", "本地外送服务已有事务事件；目标种类、字段及所有管理入口仍需逐项核对")

EXCLUDED_CATEGORIES = {
    "duo_agent_platform", "workflow_catalog", "ai_framework", "ai_abstraction_layer", "ai_agents", "agent_foundations", "code_suggestions", "mcp_server", "mlops",
    "dynamic_application_security_testing", "secret_detection", "vulnerability_management", "security_risk_management", "security_testing_configuration", "dependency_firewall", "fuzz_testing",
}
EXCLUDED_NAMES = set("protected_branch_code_owner_approval_required_updated selective_code_owner_removals_updated authenticated_with_group_saml group_saml_member_added group_saml_provider_create group_saml_provider_update saml_group_links_created saml_group_links_removed update_mismatched_group_saml_extern_uid user_provisioned_by_scim inactive_scim_user_cannot_be_added".split())


def scalar(text, key):
    match = re.search(r"^" + re.escape(key) + r":\s*([^\n]*)$", text, re.MULTILINE)
    return match.group(1).strip().strip("\"'") if match else ""


def entry(path, data):
    text = data.decode("utf-8")
    name = scalar(text, "name")
    if not name or name != Path(path).stem:
        raise ValueError("事件标识与文件名不一致：" + path)
    category = scalar(text, "feature_category")
    status, reason, target = "待逐项核对", "尚未核对全部 Gitea 对应入口与验收用例", ""
    if name in EXCLUDED_NAMES:
        status, reason = "不适用", "用户明确排除 CODEOWNERS 或 SAML/SCIM 身份供应"
    elif category in EXCLUDED_CATEGORIES:
        status, reason = "不适用", "GitLab 专有 AI/模型业务无原生 Gitea 对应，或属于用户排除的扫描平台"
    if name in MAPPINGS:
        target, reason = MAPPINGS[name]
        status = "部分接入，待完整验收"
    scopes = scalar(text, "scope")
    for original, chinese in (("Instance", "实例"), ("Project", "项目"), ("Group", "群组"), ("User", "用户")):
        scopes = scopes.replace(original, chinese)
    return {"GitLab事件":name, "定义路径":path, "定义SHA256":hashlib.sha256(data).hexdigest(),
            "官方分类标识":category, "官方范围":scopes or "定义未声明，待核对", "官方本地保存":scalar(text,"saved_to_database"),
            "官方外送":scalar(text,"streamed"), "本次状态":status, "Gitea事件":target, "依据与缺口":reason,
            "固定来源":"https://gitlab.com/gitlab-org/gitlab/-/blob/v19.3.2-ee/"+path}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--enterprise-archive", type=Path, required=True)
    parser.add_argument("--core-directory", type=Path, required=True)
    args = parser.parse_args()
    entries = []
    with tarfile.open(args.enterprise_archive) as archive:
        for item in archive.getmembers():
            if item.isfile() and item.name.endswith(".yml"):
                path = item.name.split("/", 1)[1]
                if not path.startswith("ee/config/audit_events/types/"):
                    raise ValueError("归档包含范围外文件")
                entries.append(entry(path, archive.extractfile(item).read()))
    if len(entries) != 561:
        raise ValueError("冻结企业目录数量应为 561，不能静默改变基线")
    core = list(args.core_directory.glob("*.yml"))
    if len(core) != 70:
        raise ValueError("冻结基础目录数量应为 70，不能接受未完整下载目录")
    entries.extend(entry("config/audit_events/types/"+p.name, p.read_bytes()) for p in core)
    entries.sort(key=lambda item: item["GitLab事件"])
    names = [item["GitLab事件"] for item in entries]
    if len(set(names)) != len(names) or set(MAPPINGS)-set(names) or EXCLUDED_NAMES-set(names):
        raise ValueError("事件重复或映射存在未登记的标识")
    report = {"基线":"GitLab Self-Managed Ultimate 19.3.2", "总数":len(entries), "状态统计":dict(Counter(item["本次状态"] for item in entries)),
              "说明":"目录覆盖不等于事件入口覆盖；映射均保留部分接入状态，禁止将其计为全部验收通过。其余事件必须逐项读源码、确定本次对应业务或不适用依据。",
              "本地策略":"适用治理事件永久保存，访问事件可靠外送；不直接照搬上游逐项保存标志。", "事件":entries}
    (ROOT/"审计事件对照矩阵.json").write_text(json.dumps(report,ensure_ascii=False,indent=2)+"\n")
    print("冻结目录生成完成："+str(len(entries))+" 项；"+json.dumps(report["状态统计"],ensure_ascii=False))


if __name__ == "__main__":
    main()
