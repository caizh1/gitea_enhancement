#!/usr/bin/env python3
"""用独立容器、数据库和数据卷验证旧版到当前开发镜像的数据迁移。"""

import argparse
import base64
from datetime import datetime, timedelta, timezone
import hashlib
from html.parser import HTMLParser
import http.cookiejar
import ipaddress
import json
import os
from pathlib import Path
import secrets
import shlex
import tempfile
import subprocess
import time
import urllib.error
import urllib.request
import urllib.parse
import uuid


ROOT = Path(__file__).resolve().parents[1]
LOCAL_HTTP = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def run(args, *, env=None, check=True):
    result = subprocess.run(args, cwd=ROOT, env=env, capture_output=True, text=True)
    if check and result.returncode:
        operation = args[1] if args[0] == "docker" and len(args) > 1 else "本地子进程"
        raise RuntimeError(f"执行失败：{args[0]} {operation}，退出码 {result.returncode}，UTC {datetime.now(timezone.utc).isoformat()}；{result.stderr[-2000:]}")
    return result.stdout.strip()


def published_port(container, port):
    ports = json.loads(run(["docker", "inspect", "--format", "{{json .NetworkSettings.Ports}}", container]))
    bindings = ports.get(str(port)+"/tcp", [])
    if len(bindings) != 1 or bindings[0]["HostIp"] != "127.0.0.1":
        raise RuntimeError("验收端口未独占绑定到本机回环地址")
    return int(bindings[0]["HostPort"])


def unused_subnet():
    identifiers = run(["docker", "network", "ls", "--quiet"]).splitlines()
    occupied = []
    if identifiers:
        for network in json.loads(run(["docker", "network", "inspect", *identifiers])):
            for config in network.get("IPAM", {}).get("Config", []) or []:
                if config.get("Subnet"):
                    occupied.append(ipaddress.ip_network(config["Subnet"]))
    for candidate in ipaddress.ip_network("10.240.0.0/16").subnets(new_prefix=24):
        if not any(candidate.overlaps(item) for item in occupied):
            return str(candidate)
    raise RuntimeError("专用验收子网池没有空闲地址；不会清理其他任务的网络")


def api(url, method="GET", body=None, credentials=None):
    headers = {"Accept": "application/json"}
    if credentials:
        encoded = base64.b64encode(":".join(credentials).encode()).decode()
        headers["Authorization"] = "Basic " + encoded
    data = None
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    with LOCAL_HTTP.open(request, timeout=15) as response:
        raw = response.read()
        return json.loads(raw) if raw else None


def wait_ready(url):
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        try:
            return api(url + "/api/v1/version")["version"]
        except (urllib.error.URLError, ConnectionError, TimeoutError):
            time.sleep(0.5)
    raise RuntimeError("隔离 Gitea 在九十秒内未就绪")


def git_refs(url, credentials, path="acme/firmware"):
    env = os.environ.copy()
    env.update({"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "http.extraHeader",
                "GIT_CONFIG_VALUE_0": "Authorization: Basic " + base64.b64encode(":".join(credentials).encode()).decode(),
                "GIT_TERMINAL_PROMPT": "0", "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1",
                "NO_PROXY": "127.0.0.1,localhost", "no_proxy": "127.0.0.1,localhost"})
    return run(["git", "ls-remote", url + "/" + path + ".git"], env=env)


def complete_evidence(evidence, checks):
    """全部阶段及清理结束后一次汇总；后续异常不能保留先前的通过结论。"""
    evidence["断言"] = [{"检查": label, "结果": "通过" if passed else "失败"} for label, passed in checks]
    successful = bool(checks) and all(passed for _, passed in checks) and "错误" not in evidence
    successful = successful and all(item["结果"] == "完成" for item in evidence.get("资源清理", []))
    evidence["结果"] = "通过" if successful else "失败"


def verify_upgrade(source, target, postgres, expected_version, ui_ready=None, ui_release=None, receiver_binary=None, check_expiry=False, check_member_sources=False, check_account_security=False, check_branch_protection=False, check_tag_protection=False):
    prefix = "governance-upgrade-" + uuid.uuid4().hex[:10]
    network, database, server, volume = [prefix + suffix for suffix in ("-net", "-db", "-web", "-data")]
    resources = []
    ssh_port = 0
    url = ""
    admin = ("governance_admin", secrets.token_urlsafe(24))
    reviewer = ("governance_reviewer", secrets.token_urlsafe(24))
    receiver = prefix + "-receiver"
    receiver_url = ""
    receiver_token = secrets.token_urlsafe(24)
    evidence = {"原版镜像": source, "目标镜像": target, "断言": [], "结果": "失败"}
    checks = []

    def launch(image, fixed_ports=None):
        nonlocal url, ssh_port
        config = json.loads(run(["docker", "image", "inspect", "--format", "{{json .Config}}", image]))
        command = (config.get("Entrypoint") or [])+(config.get("Cmd") or [])
        if not command:
            raise RuntimeError("待验证镜像没有原生启动入口")
        if ("container", server) not in resources:
            resources.append(("container", server))
        # 先由 Docker 保留随机端口，再写入已分配的公开地址并执行镜像原生入口。
        wrapper = 'until [ -f /tmp/governance-root-url ]; do sleep 0.1; done; export GITEA__server__ROOT_URL="$(cat /tmp/governance-root-url)"; exec "$@"'
        run(["docker", "run", "--detach", "--platform", "linux/amd64", "--pull=never", "--name", server,
             "--network", network, "--label", "codex.task=gitea-native-governance",
             "--publish", "127.0.0.1:"+(str(fixed_ports[0]) if fixed_ports else "")+":3000",
             "--publish", "127.0.0.1:"+(str(fixed_ports[1]) if fixed_ports else "")+":22",
             "--volume", volume + ":/data", "--env", "GITEA__database__DB_TYPE=postgres",
             "--env", f"GITEA__database__HOST={database}:5432", "--env", "GITEA__database__NAME=governance_test",
             "--env", "GITEA__database__USER=governance_test", "--env", "GITEA__database__PASSWD=",
             "--env", "GITEA__packages__PATH=/data/gitea/packages", "--env", "GITEA__server__LFS_START_SERVER=true", "--env", "GITEA__lfs__PATH=/data/git/lfs",
             "--env", "GITEA__security__INSTALL_LOCK=true", "--env", "GITEA__service__DISABLE_REGISTRATION=true",
             "--env", f"GITEA__security__ALLOWED_HOST_LIST=external,{receiver}",
             *(["--env", "GITEA__mailer__ENABLED=true", "--env", "GITEA__mailer__PROTOCOL=smtp", "--env", f"GITEA__mailer__SMTP_ADDR={receiver}", "--env", "GITEA__mailer__SMTP_PORT=1025", "--env", "GITEA__mailer__FROM=governance@example.test"] if expected_version >= 359 and receiver_binary else []),
             "--entrypoint", "/bin/sh", image, "-c", wrapper, "治理验收启动", *command])
        port, ssh_port = published_port(server, 3000), published_port(server, 22)
        url = f"http://127.0.0.1:{port}"
        run(["docker", "exec", server, "/bin/sh", "-c", 'printf "%s\n" "$1" > /tmp/governance-root-url', "治理验收地址", url+"/"])
        return wait_ready(url)

    try:
        evidence["原版镜像校验"] = json.loads(run(["docker", "image", "inspect", source]))[0]["Id"]
        evidence["目标镜像校验"] = json.loads(run(["docker", "image", "inspect", target]))[0]["Id"]
        subnet = unused_subnet()
        run(["docker", "network", "create", "--subnet", subnet, "--label", "codex.task=gitea-native-governance", network])
        evidence["临时专用子网"] = subnet
        resources.append(("network", network))
        run(["docker", "volume", "create", "--label", "codex.task=gitea-native-governance", volume])
        resources.append(("volume", volume))
        resources.append(("container", database))
        run(["docker", "run", "--detach", "--platform", "linux/amd64", "--pull=never", "--name", database,
             "--network", network, "--label", "codex.task=gitea-native-governance",
             "--env", "POSTGRES_USER=governance_test", "--env", "POSTGRES_DB=governance_test",
             "--env", "POSTGRES_HOST_AUTH_METHOD=trust", postgres])
        if receiver_binary:
            resources.append(("container", receiver))
            run(["docker", "run", "--detach", "--platform", "linux/amd64", "--pull=never", "--name", receiver,
                 "--network", network, "--label", "codex.task=gitea-native-governance", "--publish", "127.0.0.1::8080",
                 "--read-only", "--cap-drop=ALL", "--security-opt", "no-new-privileges", "--user", "1000:1000",
                 "--env", "AUDIT_TEST_TOKEN="+receiver_token, "--volume", str(receiver_binary.resolve())+":/test-receiver:ro",
                 "--entrypoint", "/test-receiver", target])
            receiver_url = f"http://127.0.0.1:{published_port(receiver,8080)}"
        resources.append(("container", server))
        evidence["原版程序版本"] = launch(source)
        run(["docker", "exec", "--user", "git", server, "gitea", "admin", "user", "create",
             "--config", "/data/gitea/conf/app.ini", "--username", admin[0], "--password", admin[1],
             "--email", "admin@example.test", "--admin", "--must-change-password=false"])
        org = api(url + "/api/v1/orgs", "POST", {"username": "acme", "full_name": "治理验收团队", "visibility": "private"}, admin)
        repo = api(url + "/api/v1/orgs/acme/repos", "POST", {"name": "firmware", "private": True, "auto_init": True, "default_branch": "main"}, admin)
        issue = api(url + "/api/v1/repos/acme/firmware/issues", "POST", {"title": "必须保留的问题历史", "body": "迁移固定样本"}, admin)
        api(url + "/api/v1/admin/users", "POST", {"username": reviewer[0], "password": reviewer[1], "email": "reviewer@example.test", "must_change_password": False}, admin)
        api(url + "/api/v1/repos/acme/firmware/collaborators/" + reviewer[0], "PUT", {"permission": "write"}, admin)
        api(url + "/api/v1/repos/acme/firmware/branches", "POST", {"new_branch_name": "feature", "old_branch_name": "main"}, admin)
        file = api(url + "/api/v1/repos/acme/firmware/contents/migration.txt", "POST", {"branch": "feature", "message": "验收：建立升级样本", "content": base64.b64encode("必须保留的代码内容\n".encode()).decode()}, admin)
        pull = api(url + "/api/v1/repos/acme/firmware/pulls", "POST", {"title": "必须保留的合并请求", "base": "main", "head": "feature"}, admin)
        review = api(url + f"/api/v1/repos/acme/firmware/pulls/{pull['number']}/reviews", "POST", {"event": "APPROVED", "commit_id": file["commit"]["sha"]}, reviewer)
        original_refs = git_refs(url, admin)
        evidence["历史样本ID"] = {"组织": org["id"], "仓库": repo["id"], "问题": issue["id"], "合并请求": pull["id"], "评审": review["id"]}
        run(["docker", "stop", "--time", "30", server])
        run(["docker", "rm", server])
        evidence["目标程序版本"] = launch(target)
        checks = [
            ("组织 ID 保留", api(url + "/api/v1/orgs/acme", credentials=admin)["id"] == org["id"]),
            ("仓库 ID 保留", api(url + "/api/v1/repos/acme/firmware", credentials=admin)["id"] == repo["id"]),
            ("问题 ID 保留", api(url + f"/api/v1/repos/acme/firmware/issues/{issue['number']}", credentials=admin)["id"] == issue["id"]),
            ("PR ID 保留", api(url + f"/api/v1/repos/acme/firmware/pulls/{pull['number']}", credentials=admin)["id"] == pull["id"]),
            ("批准评审保留", any(item["id"] == review["id"] and item["state"] == "APPROVED" for item in api(url + f"/api/v1/repos/acme/firmware/pulls/{pull['number']}/reviews", credentials=admin))),
            ("真实 HTTP Git 引用保持一致", git_refs(url, admin) == original_refs),
        ]
        sql = "SELECT count(*) FROM governance_namespace WHERE id = " + str(org["id"])
        checks.append(("旧组织导入原生命名空间", run(["docker", "exec", database, "psql", "-U", "governance_test", "-d", "governance_test", "-tAc", sql]) == "1"))
        checks.append((f"原生迁移版本为 {expected_version}", run(["docker", "exec", database, "psql", "-U", "governance_test", "-d", "governance_test", "-tAc", "SELECT version FROM version WHERE id = 1"]) == str(expected_version)))
        capabilities = api(url + "/api/v1/governance/capabilities", credentials=admin)
        checks.append(("原生审计查询与导出能力可发现", capabilities["audit_query"] and capabilities["audit_exports"]))
        now = datetime.now(timezone.utc)
        audit_filter = {"scope_type": "repository", "scope_id": repo["id"], "from": (now-timedelta(days=1)).isoformat(), "to": now.isoformat()}
        api(url + "/api/v1/governance/audit-exports", "POST", {"filter": audit_filter, "format": "json"}, admin)
        events = api(url + f"/api/v1/governance/audit-events?scope_type=repository&scope_id={repo['id']}", credentials=admin)["events"]
        checks.append(("导出申请生成持久化审计并保留真实身份", any(e["event_type"] == "audit.export_requested" and e["actor"]["name"] == admin[0] for e in events)))
        audit_filter["to"] = datetime.now(timezone.utc).isoformat()
        api(url + "/api/v1/repos/acme/firmware/collaborators/" + reviewer[0], "PUT", {"permission": "admin"}, admin)
        job = api(url + "/api/v1/governance/audit-exports", "POST", {"filter": audit_filter, "format": "json"}, reviewer)
        deadline = time.monotonic()+30
        while job["state"] != "ready" and time.monotonic() < deadline:
            time.sleep(0.5)
            job = api(url + "/api/v1/governance/audit-exports/" + job["id"], credentials=reviewer)
        checks.append(("原生后台调度完成导出", job["state"] == "ready"))
        exported = api(url + "/api/v1/governance/audit-exports/" + job["id"] + "/download", credentials=reviewer)
        checks.append(("导出内容与原生审计事件对应", any(e["id"] == events[0]["id"] for e in exported)))
        api(url + "/api/v1/repos/acme/firmware/collaborators/" + reviewer[0], "PUT", {"permission": "write"}, admin)
        try:
            api(url + "/api/v1/governance/audit-exports/" + job["id"] + "/download", credentials=reviewer)
            checks.append(("原生撤权后不能下载旧导出", False))
        except urllib.error.HTTPError as error:
            checks.append(("原生撤权后不能下载旧导出", error.code == 404))
        if expected_version >= 347:
            child = api(url+"/api/v1/governance/groups", "POST", {"name":"研发子群组", "path":"rd", "parent_id":org["id"], "visibility":2}, admin)
            nested = api(url+"/api/v1/orgs/acme/rd/repos", "POST", {"name":"nested", "private":True, "auto_init":True}, admin)
            checks.append(("完整层级项目路径由原生接口返回", nested["full_path"]=="acme/rd/nested"))
            api(url+"/api/v1/repos/acme/rd/nested/contents/automatic-hook.txt", "POST", {"message":"验收：自动接入引用事务", "content":base64.b64encode("自动接入后的原生文件变更\n".encode()).decode()}, admin)
            automatic_state = run(["docker", "exec", database, "psql", "-U", "governance_test", "-d", "governance_test", "-tAc", f"SELECT count(*) FROM governance_ref_transaction WHERE repo_id = {nested['id']} AND state = 'committed'"])
            checks.append(("新建层级仓库自动接入且原生文件写入留下引用事务", int(automatic_state)>0))
            member_id = api(url+"/api/v1/users/"+reviewer[0], credentials=admin)["id"]
            parent_path = url+"/api/v1/governance/groups/"+str(org["id"])
            parent = api(parent_path, credentials=admin)
            api(parent_path+"/members/"+str(member_id), "PUT", {"role":20,"revision":parent["revision"]}, admin)
            refs = git_refs(url,reviewer,"acme/rd/nested")
            checks.append(("原生 HTTP Git 读取继承授权的子组项目", bool(refs)))
            child_path = url+"/api/v1/governance/groups/"+str(child["id"])
            moved = api(child_path+"/move", "POST", {"path":"research","parent_id":org["id"],"revision":child["revision"]}, admin)
            checks.append(("层级移动后新旧 Git 地址指向同一引用", git_refs(url,reviewer,"acme/research/nested")==refs and git_refs(url,reviewer,"acme/rd/nested")==refs))
            parent = api(parent_path, credentials=admin)
            api(parent_path+"/members/"+str(member_id)+"?revision="+str(parent["revision"]),"DELETE",credentials=admin)
            try:
                api(url+"/api/v1/repos/acme/research/nested",credentials=reviewer)
                denied=False
            except urllib.error.HTTPError as error:
                denied=error.code==404
            checks.append(("撤销父组授权后原生项目接口拒绝",denied))
            try:
                git_refs(url,reviewer,"acme/rd/nested")
                denied=False
            except RuntimeError as error:
                denied=any(text in str(error).lower() for text in ("not found", "403", "404"))
            checks.append(("旧别名不能绕过 HTTP Git 撤权",denied))
            parent = api(parent_path,credentials=admin)
            role = api(parent_path+"/roles","POST",{"name":"局部审计","base_role":20,"abilities":["read_audit"],"revision":parent["revision"]},admin)
            api(child_path+"/members/"+str(member_id),"PUT",{"role":20,"custom_role_id":role["id"],"revision":moved["revision"]},admin)
            state=api(child_path,credentials=reviewer)
            checks.append(("自定义角色按能力增加审计而不授予推送",state["abilities"].get("read_audit",False) and not state["abilities"].get("push_code",False)))
            parent=api(parent_path,credentials=admin)
            api(parent_path+"/roles/"+str(role["id"]),"PUT",{"name":"只读成员","base_role":20,"abilities":[],"revision":parent["revision"]},admin)
            state=api(child_path,credentials=reviewer)
            checks.append(("角色收权完成后的新请求不再获得审计能力",not state["abilities"].get("read_audit",False)))
            evidence["层级样本ID"]={"群组":child["id"],"项目":nested["id"],"角色":role["id"]}
        if expected_version >= 349:
            rule_path = url+f"/api/v1/governance/repositories/{repo['id']}/approval-rules"
            reviewer_id = api(url+"/api/v1/users/"+reviewer[0], credentials=admin)["id"]
            rule_option = {"rule":{"name":"指定评审者必须批准", "required":1, "user_ids":[reviewer_id], "branch_mode":"all", "enabled":True}}
            rule = api(rule_path, "POST", rule_option, admin)
            configuration = api(rule_path, credentials=admin)
            checks.append(("升级镜像原生保存指定人员审批规则", any(item["id"]==rule["id"] and item["user_ids"]==[reviewer_id] for item in configuration["rules"])))
            checks.append(("未完成最终门禁时不宣称审批强制就绪", not configuration["enforcement_ready"]))
            try:
                api(rule_path, "POST", {"rule":dict(rule_option["rule"], name="无权创建")}, reviewer)
                denied = False
            except urllib.error.HTTPError as error:
                denied = error.code==404
            checks.append(("普通开发者不能修改项目审批规则", denied))
            rule_option.update({"revision":rule["revision"]})
            rule_option["rule"]["required"] = 2
            rule = api(rule_path+"/"+str(rule["id"]), "PUT", rule_option, admin)
            try:
                api(rule_path+"/"+str(rule["id"]), "PUT", rule_option, admin)
                denied = False
            except urllib.error.HTTPError as error:
                denied = error.code==409
            checks.append(("过期审批规则修订号被拒绝", denied))
            settings_path = url+f"/api/v1/governance/repositories/{repo['id']}/approval-settings"
            settings_option = {"revision":configuration["settings_revision"], "prevent_author":True, "reset_on_change":True, "prevent_overrides":True}
            api(settings_path, "PUT", settings_option, admin)
            configuration = api(rule_path, credentials=admin)
            checks.append(("原生审批设置保存并返回真实来源", configuration["settings"]["settings"]["prevent_overrides"] and configuration["settings"]["sources"]["prevent_overrides"]["scope_id"]==repo["id"]))
            sql = f"SELECT rules FROM governance_pull_rule_version WHERE pull_id = {pull['id']} ORDER BY revision DESC LIMIT 1"
            rules_snapshot = json.loads(run(["docker", "exec", database, "psql", "-U", "governance_test", "-d", "governance_test", "-tAc", sql]))
            checks.append(("禁止覆盖后已有 PR 使用最新项目规则快照", any(item["id"]==rule["id"] and item["required"]==2 for item in rules_snapshot)))
            events = api(url+f"/api/v1/governance/audit-events?scope_type=repository&scope_id={repo['id']}", credentials=admin)["events"]
            checks.append(("审批规则与设置变更生成真实身份审计", all(any(event["event_type"]==kind and event["actor"]["name"]==admin[0] for event in events) for kind in ("approval.rule_changed", "approval.settings_changed"))))
        if expected_version >= 351:
            output = run(["docker", "exec", "--user", "git", server, "gitea", "admin", "governance", "pending", "--config", "/data/gitea/conf/app.ini"])
            pending = json.loads(next(line for line in reversed(output.splitlines()) if line.startswith("{")))
            checks.append(("镜像内恢复命令可查询且没有遗留操作", not pending["合并操作"] and not pending["引用事务"]))
            table = run(["docker", "exec", database, "psql", "-U", "governance_test", "-d", "governance_test", "-tAc", "SELECT to_regclass('public.governance_reference_revision')"])
            checks.append(("升级创建仓库引用修订表", table == "governance_reference_revision"))
        if expected_version >= 352:
            scope_settings_path = url+f"/api/v1/governance/approval-settings/group/{org['id']}"
            inherited = api(scope_settings_path, credentials=admin)
            scope_option = {"revision":inherited["local"]["revision"], "prevent_author":True, "prevent_committer":True, "reset_on_change":True}
            scoped = api(scope_settings_path, "PUT", scope_option, admin)
            effective = api(rule_path, credentials=admin)["settings"]
            checks.append(("顶级组限制下发到已有项目", effective["settings"]["prevent_committer"] and effective["sources"]["prevent_committer"]["scope_id"]==org["id"]))
            api(scope_settings_path+"?revision="+str(scoped["revision"]), "DELETE", credentials=admin)
            inherited = api(scope_settings_path, credentials=admin)
            checks.append(("恢复继承保留审批设置修订历史", inherited["local"]["inherit"] and inherited["local"]["revision"]>scoped["revision"]))
            try:
                api(scope_settings_path, "PUT", scope_option, admin)
                denied = False
            except urllib.error.HTTPError as error:
                denied = error.code==409
            checks.append(("恢复继承后拒绝旧设置请求", denied))
            policy_path = url+f"/api/v1/governance/approval-policies/group/{org['id']}"
            policy = api(policy_path, "POST", {"rule":{"name":"顶级组指定人员必批", "required":1, "user_ids":[reviewer_id], "branch_mode":"all", "enabled":True}}, admin)
            rules = api(rule_path, credentials=admin)["rules"]
            checks.append(("顶级组强制策略作用于已有项目", any(item["id"]==policy["id"] and item["scope_type"]=="group" and item["locked"] for item in rules)))
        if expected_version >= 353:
            protection_path = url+"/api/v1/repos/acme/firmware/branch_protections"
            gate = {"rule_name":"main", "enable_push":True, "enable_force_push":True, "require_governance_approval":True}
            # 此独立固定样本明确模拟入口丢失，不能依赖旧版本恰好未安装。
            run(["docker", "exec", server, "rm", "-f", "/data/git/repositories/acme/firmware.git/hooks/reference-transaction"])
            try:
                api(protection_path, "POST", gate, admin)
                denied = False
            except urllib.error.HTTPError as error:
                denied = error.code==409
            checks.append(("引用事务入口缺失时拒绝开启强制合并门禁", denied))
            output = run(["docker", "exec", "--user", "git", server, "gitea", "admin", "governance", "install-reference-hook", "--repository-id", str(repo["id"]), "--config", "/data/gitea/conf/app.ini"])
            checks.append(("镜像内接入命令安装并核对真实入口", "引用事务入口已核对" in output))
            saved_gate = api(protection_path, "POST", gate, admin)
            checks.append(("迁移后的原生保护分支持久化最终审批门禁", saved_gate["require_governance_approval"] and api(protection_path+"/main", credentials=admin)["require_governance_approval"]))
            configuration = api(rule_path, credentials=admin)
            settings_option.update({"revision":configuration["settings_revision"], "require_reauthentication":True})
            api(settings_path, "PUT", settings_option, admin)
            review_path = url+f"/api/v1/repos/acme/firmware/pulls/{pull['number']}/reviews"
            approve = {"event":"APPROVED", "commit_id":file["commit"]["sha"]}
            try:
                api(review_path, "POST", approve, reviewer)
                denied = False
            except urllib.error.HTTPError as error:
                denied = error.code==403
            checks.append(("升级镜像要求批准时再次认证", denied))
            approve["approval_password"] = reviewer[1]
            reviews_before = api(review_path, credentials=admin)
            try:
                api(review_path+"?sudo="+urllib.parse.quote(reviewer[0]), "POST", approve, admin)
                denied = False
            except urllib.error.HTTPError as error:
                denied = error.code==403
            reviews_after = api(review_path, credentials=admin)
            denied_events = api(url+f"/api/v1/governance/audit-events?scope_type=repository&scope_id={repo['id']}&event_type=approval.denied", credentials=admin)["events"]
            checks.append(("管理员代办不能产生本人批准且保留双方身份", denied and reviews_before==reviews_after and any(event["actor"]["name"]==admin[0] and event["actor"].get("acting_as_id")==reviewer_id and event["result"]=="denied" for event in denied_events)))
            approved = api(review_path, "POST", approve, reviewer)
            proof = run(["docker", "exec", database, "psql", "-U", "governance_test", "-d", "governance_test", "-tAc", f"SELECT reauthenticated FROM governance_approval_evidence WHERE review_id = {approved['id']}"])
            checks.append(("真实批准保存再次认证与当前差异证据", proof=="t"))
            events = api(url+f"/api/v1/governance/audit-events?scope_type=repository&scope_id={repo['id']}", credentials=admin)["events"]
            checks.append(("批准审计保存真实身份和评审关联", any(event["event_type"]=="approval.approved" and event["entity_id"]==approved["id"] and event["actor"]["name"]==reviewer[0] and event["actor"]["transport"]=="api" for event in events)))
            # 保护规则变更会重新排队原生冲突检查；只读等待，合并写请求只发送一次。
            native_ready_deadline = time.monotonic()+90
            while not api(url+f"/api/v1/repos/acme/firmware/pulls/{pull['number']}", credentials=admin)["mergeable"]:
                if time.monotonic() >= native_ready_deadline:
                    raise RuntimeError("原生 PR 检查九十秒内未就绪，未发送合并请求")
                time.sleep(0.5)
            before_denial = next(line for line in git_refs(url, admin).splitlines() if line.endswith("refs/heads/main"))
            denial_status = 200
            denial_body = ""
            try:
                api(url+f"/api/v1/repos/acme/firmware/pulls/{pull['number']}/merge", "POST", {"Do":"merge", "head_commit_id":file["commit"]["sha"]}, admin)
                denied = False
            except urllib.error.HTTPError as error:
                denied = error.code==403
                denial_status = error.code
                denial_body = error.read().decode("utf-8", errors="replace")[:1000]
            after_denial = next(line for line in git_refs(url, admin).splitlines() if line.endswith("refs/heads/main"))
            events = api(url+f"/api/v1/governance/audit-events?scope_type=repository&scope_id={repo['id']}", credentials=admin)["events"]
            denial_audited = any(event["event_type"]=="merge.denied" and event["actor"]["name"]==admin[0] and event["result"]=="denied" for event in events)
            evidence["拒绝合并诊断"] = {"HTTP状态":denial_status, "响应":denial_body, "引用前":before_denial, "引用后":after_denial, "存在对应拒绝审计":denial_audited}
            checks.append(("批准不足时真实目标不变且保存拒绝合并审计", denied and before_denial==after_denial and denial_audited))
            evidence["拒绝合并诊断"]["原生检查状态"] = run(["docker", "exec", database, "psql", "-U", "governance_test", "-d", "governance_test", "-tAc", f"SELECT status FROM pull_request WHERE issue_id = {pull['id']}"])
            if denial_status != 403:
                diagnostic = subprocess.run(["docker", "logs", "--tail", "500", server], capture_output=True, text=True)
                selected = [line for line in (diagnostic.stdout+diagnostic.stderr).splitlines() if any(word in line.lower() for word in ("[e]", "conflict", "reference", "pull request"))]
                text = "\n".join(selected)[-12000:]
                for secret in (admin[1], reviewer[1], receiver_token):
                    text = text.replace(secret, "[已移除]")
                evidence["拒绝合并诊断"]["后台检查日志"] = text

            api(review_path+"/"+str(approved["id"]), "DELETE", credentials=reviewer)
            events = api(url+f"/api/v1/governance/audit-events?scope_type=repository&scope_id={repo['id']}", credentials=admin)["events"]
            checks.append(("删除新批准后保留真实撤回审计", any(event["event_type"]=="approval.withdrawn" and event["entity_id"]==approved["id"] and event["actor"]["name"]==reviewer[0] for event in events)))
            archive_path = url+"/api/v1/repos/acme/firmware"
            archived_repo = api(archive_path, "PATCH", {"archived":True}, admin)
            events = api(url+f"/api/v1/governance/audit-events?scope_type=repository&scope_id={repo['id']}", credentials=admin)["events"]
            checks.append(("升级镜像原生归档与真实身份审计一致", archived_repo["archived"] and any(event["event_type"]=="repository.archived" and event["actor"]["name"]==admin[0] for event in events)))
            archived_repo = api(archive_path, "PATCH", {"archived":False}, admin)
            events = api(url+f"/api/v1/governance/audit-events?scope_type=repository&scope_id={repo['id']}", credentials=admin)["events"]
            checks.append(("升级镜像恢复归档与真实身份审计一致", not archived_repo["archived"] and any(event["event_type"]=="repository.unarchived" and event["actor"]["name"]==admin[0] for event in events)))


            group_archive_path = url+f"/api/v1/governance/groups/{org['id']}/archive"
            impact = api(group_archive_path, credentials=admin)
            archived_group = api(group_archive_path, "PUT", {"archived":True, "revision":impact["revision"]}, admin)
            checks.append(("升级镜像群组归档同步项目状态", archived_group["archived"] and api(archive_path, credentials=admin)["archived"]))
            try:
                api(archive_path, "PATCH", {"archived":False}, admin)
                denied = False
            except urllib.error.HTTPError as error:
                denied = error.code==409
            checks.append(("父组归档时不能单独恢复项目", denied))
            restored_group = api(group_archive_path, "PUT", {"archived":False, "revision":archived_group["revision"]}, admin)
            checks.append(("恢复群组同步恢复项目且引用不变", not restored_group["archived"] and not api(archive_path, credentials=admin)["archived"] and next(line for line in git_refs(url, admin).splitlines() if line.endswith("refs/heads/main"))==after_denial))
            events = api(url+f"/api/v1/governance/audit-events?scope_type=group&scope_id={org['id']}", credentials=admin)["events"]
            checks.append(("群组归档恢复保留实际操作者审计", all(any(event["event_type"]==kind and event["actor"]["name"]==admin[0] for event in events) for kind in ("group.archived", "group.unarchived"))))


            personal_key = api(url+f"/api/v1/users/{admin[0]}/tokens", "POST", {"name":"升级凭据审计", "scopes":["read:user"]}, admin)
            token_auth = (admin[0], personal_key["sha1"])
            token_owner = api(url+"/api/v1/user", credentials=token_auth)
            api(url+f"/api/v1/users/{admin[0]}/tokens/{personal_key['id']}", "DELETE", credentials=admin)
            try:
                api(url+"/api/v1/user", credentials=token_auth)
                denied = False
            except urllib.error.HTTPError as error:
                denied = error.code==401
            checks.append(("新镜像个人令牌撤销后立即拒绝认证", token_owner["login"]==admin[0] and denied))
            credential_events = api(url+f"/api/v1/governance/audit-events?scope_type=user&scope_id={token_owner['id']}&entity_type=access_token&entity_id={personal_key['id']}", credentials=admin)["events"]
            checks.append(("令牌创建撤销保存真实用户和入口审计", all(any(event["event_type"]==kind and event["actor"]["name"]==admin[0] and event["actor"]["transport"]=="api" for event in credential_events) for kind in ("credential.access_token_created", "credential.access_token_revoked"))))
            checks.append(("令牌审计不含正文或尾部凭据", personal_key["sha1"] not in json.dumps(credential_events) and personal_key["token_last_eight"] not in json.dumps(credential_events)))


            class LoginFields(HTMLParser):
                def __init__(self):
                    super().__init__()
                    self.fields = {}
                def handle_starttag(self, tag, attrs):
                    values = dict(attrs)
                    if tag == "input" and values.get("type")=="hidden" and values.get("name"):
                        self.fields[values["name"]] = values.get("value", "")
            login_browser = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
            with login_browser.open(url+"/user/login", timeout=15) as response:
                fields = LoginFields()
                fields.feed(response.read().decode())
            fields.fields.update({"user_name":admin[0], "password":admin[1]})
            request = urllib.request.Request(url+"/user/login", data=urllib.parse.urlencode(fields.fields).encode(), method="POST")
            with login_browser.open(request, timeout=15) as response:
                response.read()
            with login_browser.open(url+"/user/settings", timeout=15) as response:
                logged_in = urllib.parse.urlsplit(response.url).path=="/user/settings" and response.status==200
                response.read()
            login_events = api(url+f"/api/v1/governance/audit-events?scope_type=user&scope_id={token_owner['id']}&event_type=authentication.login_succeeded", credentials=admin)["events"]
            checks.append(("镜像真实网页登录建立会话并保存成功审计", logged_in and any(event["actor"]["name"]==admin[0] and event["actor"]["transport"]=="web" for event in login_events)))

        if receiver_binary:
            stream_option = {"scope_type":"instance", "scope_id":0, "name":"隔离外送接收器", "kind":"http",
                             "endpoint":f"http://{receiver}:8080/events", "enabled":True, "event_types":["audit.stream_test"],
                             "credentials":{"verification_token":receiver_token}}
            stream = api(url+"/api/v1/governance/audit-streams", "POST", stream_option, admin)
            stream_option.pop("credentials")
            stream_path = url+"/api/v1/governance/audit-streams/"+str(stream["id"])

            def state_until(predicate, seconds=45):
                deadline = time.monotonic()+seconds
                while time.monotonic()<deadline:
                    state = next(s for s in api(url+"/api/v1/governance/audit-streams?scope_type=instance&scope_id=0", credentials=admin) if s["id"]==stream["id"])
                    if predicate(state):
                        return state
                    time.sleep(0.5)
                raise RuntimeError("持久化外送未在时限内达到预期状态")

            api(receiver_url+"/control", "POST", {"失败":True})
            api(stream_path+"/test", "POST", {"revision":stream["revision"]}, admin)
            state = state_until(lambda s:s["pending"]==1 and s["last_error"] and s["in_flight"]==0)
            checks.append(("外送目标失败时保留正文并显示诊断", state["pending"]==1))
            stream_option.update({"revision":state["revision"],"enabled":False})
            state = api(stream_path,"PUT",stream_option,admin)
            checks.append(("停用外送保留积压", not state["enabled"] and state["pending"]==1))
            moved = dict(stream_option, revision=state["revision"], endpoint=f"http://{receiver}:8080/other")
            try:
                api(stream_path,"PUT",moved,admin)
                checks.append(("积压期间阻止改送其他地址",False))
            except urllib.error.HTTPError as error:
                checks.append(("积压期间阻止改送其他地址",error.code==409))
            api(receiver_url+"/control", "POST", {"丢失回执":True})
            stream_option.update({"revision":state["revision"],"enabled":True})
            api(stream_path,"PUT",stream_option,admin)
            state = state_until(lambda s:s["pending"]==0 and s["delivered_count"]==1,60)
            receipts = api(receiver_url+"/state")
            checks.append(("响应丢失后使用相同事件 ID 重投并确认", len(receipts["事件"])==1 and min(receipts["事件"].values())>=3))
            checks.append(("真实接收器校验身份标识和事件正文一致", receipts["校验失败"]==0))
            checks.append(("确认后清理队列正文并保留投递统计", state["pending"]==0 and state["delivered_count"]==1))
            stream_option.update({"revision":state["revision"], "enabled":False, "event_types":["audit.stream_test", "access.code", "access.git_http", "access.git_ssh"]})
            state = api(stream_path, "PUT", stream_option, admin)
            request = urllib.request.Request(url+"/api/v1/repos/acme/firmware/raw/main/README.md", headers={"Authorization":"Basic "+base64.b64encode(":".join(admin).encode()).decode()})
            with LOCAL_HTTP.open(request, timeout=15) as response:
                file_read = response.status==200 and bool(response.read())
            read_refs = git_refs(url, admin)
            for key_kind in ("user", "deploy_key"):
                with tempfile.TemporaryDirectory(prefix="治理SSH验收-") as key_directory:
                    key_path = Path(key_directory)/"访问密钥"
                    run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(key_path)])
                    key_api = "/api/v1/user/keys" if key_kind=="user" else "/api/v1/repos/acme/firmware/keys"
                    key_option = {"title":"访问审计-"+key_kind, "key":key_path.with_suffix(".pub").read_text().strip(), "read_only":True}
                    if expected_version >= 360 and key_kind == "user":
                        # 只在本次隔离容器中制造文件写入故障，检查提交后的持久化恢复。
                        run(["docker", "exec", "--user", "git", server, "mkdir", "/data/git/.ssh/authorized_keys.tmp"])
                        try:
                            api(url+key_api, "POST", key_option, admin)
                            file_error = False
                        except urllib.error.HTTPError as error:
                            file_error = error.code == 500
                        key_info = next(key for key in api(url+key_api, credentials=admin) if key["title"] == key_option["title"])
                        pending = run(["docker", "exec", database, "psql", "-U", "governance_test", "-d", "governance_test", "-tAc", "SELECT count(*) FROM governance_ssh_key_file_sync"])
                        checks.append(("授权文件故障明确报错并持久化待恢复记录",file_error and int(pending)>0))
                        original_ports = (published_port(server, 3000), published_port(server, 22))
                        evidence["SSH崩溃前服务状态"] = run(["docker", "exec", server, "s6-svstat", "/etc/s6/gitea"])
                        # 原生 finish 会停止容器；保留文件故障直至进程退出，避免后台先清空待同步记录。
                        run(["docker", "exec", server, "s6-svc", "-k", "/etc/s6/gitea"], check=False)
                        stop_deadline = time.monotonic()+15
                        while run(["docker", "inspect", "--format", "{{.State.Running}}", server]) == "true":
                            if time.monotonic() >= stop_deadline:
                                raise RuntimeError("SSH 故障验收未能确认 Gitea 进程退出")
                            time.sleep(0.25)
                        pending_after_crash = run(["docker", "exec", database, "psql", "-U", "governance_test", "-d", "governance_test", "-tAc", "SELECT count(*) FROM governance_ssh_key_file_sync"])
                        checks.append(("真实进程崩溃后待同步记录仍然存在",int(pending_after_crash)>0))
                        run(["docker", "rm", server])
                        run(["docker", "run", "--rm", "--platform", "linux/amd64", "--label", "codex.task=gitea-native-governance", "--user", "git", "--volume", volume+":/data", "--entrypoint", "/bin/rmdir", target, "/data/git/.ssh/authorized_keys.tmp"])
                        launch(target, fixed_ports=original_ports)
                        deadline = time.monotonic()+60
                        while time.monotonic() < deadline:
                            pending = run(["docker", "exec", database, "psql", "-U", "governance_test", "-d", "governance_test", "-tAc", "SELECT count(*) FROM governance_ssh_key_file_sync"])
                            if pending == "0":
                                try:
                                    api(url+"/api/v1/version")
                                    break
                                except (urllib.error.URLError, TimeoutError):
                                    pass
                            time.sleep(0.5)
                        checks.append(("进程崩溃重启后恢复待同步密钥而不重新创建",pending == "0"))
                    else:
                        key_info = api(url+key_api, "POST", key_option, admin)
                    ssh_env = os.environ.copy()
                    ssh_env.update({"GIT_SSH_COMMAND":"ssh -F /dev/null -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i "+shlex.quote(str(key_path)), "GIT_TERMINAL_PROMPT":"0"})
                    ssh_refs = run(["git", "ls-remote", f"ssh://git@127.0.0.1:{ssh_port}/acme/firmware.git"], env=ssh_env)
                    audit_payloads = run(["docker", "exec", database, "psql", "-U", "governance_test", "-d", "governance_test", "-tAc", "SELECT payload FROM governance_audit_delivery WHERE payload::jsonb->>'event_type' = 'access.git_ssh'"])
                    events = [json.loads(line) for line in audit_payloads.splitlines() if line.strip()]
                    matched = [event for event in events if event["actor"]["kind"]==key_kind]
                    checks.append(("真实SSH读取与身份快照："+key_kind, bool(ssh_refs) and len(matched)==2 and {e["result"] for e in matched}=={"pending","success"} and all(e["actor"].get("credential_id",0)>0 and bool(e["actor"].get("ip")) and (e["actor"]["id"]==0 if key_kind=="deploy_key" else e["actor"]["id"]>0) for e in matched)))
                    api(url+key_api+"/"+str(key_info["id"]), "DELETE", credentials=admin)
                    if expected_version >= 360:
                        rejected = subprocess.run(["git", "ls-remote", f"ssh://git@127.0.0.1:{ssh_port}/acme/firmware.git"],env=ssh_env,capture_output=True,text=True,timeout=20)
                        checks.append(("撤销后真实SSH拒绝读取："+key_kind,rejected.returncode != 0 and not rejected.stdout.strip()))
                        scope = f"user&scope_id={token_owner['id']}" if key_kind == "user" else f"repository&scope_id={repo['id']}"
                        event_prefix = "credential.ssh_key_" if key_kind == "user" else "credential.deploy_key_"
                        key_events = api(url+"/api/v1/governance/audit-events?scope_type="+scope,credentials=admin)["events"]
                        relevant = [event for event in key_events if event["event_type"] in {event_prefix+"created",event_prefix+"revoked"} and event["entity_id"] == key_info["id"]]
                        checks.append(("创建撤销审计完整且使用实际操作者："+key_kind,len(relevant)==2 and all(event["actor"]["name"]==admin[0] for event in relevant) and key_option["key"] not in json.dumps(relevant)))
            state = state_until(lambda s:s["pending"]>=4 and s["in_flight"]==0)
            access_pending = state["pending"]
            checks.append(("暂停外送仍保存代码与真实Git读取的访问事件", file_read and bool(read_refs) and not state["enabled"] and access_pending>=4))
            stream_option.update({"revision":state["revision"], "enabled":True})
            api(stream_path, "PUT", stream_option, admin)
            state = state_until(lambda s:s["pending"]==0 and s["delivered_count"]==1+access_pending, 60)
            receipts = api(receiver_url+"/state")
            checks.append(("恢复外送后真实接收器收到代码及Git访问事件", receipts["类型"].get("access.code",0)>=2 and receipts["类型"].get("access.git_http",0)>=2 and receipts["类型"].get("access.git_ssh",0)>=4 and receipts["校验失败"]==0))
            permanent_access = run(["docker", "exec", database, "psql", "-U", "governance_test", "-d", "governance_test", "-tAc", "SELECT count(*) FROM governance_audit_event WHERE type IN ('access.code', 'access.git_http', 'access.git_ssh')"])
            checks.append(("访问事件确认后清理正文且未混入永久治理表", state["pending"]==0 and permanent_access=="0"))
        if expected_version >= 354:
            temporary_repo = api(url+"/api/v1/orgs/acme/repos", "POST", {"name":"cleanup-check", "private":True, "auto_init":True}, admin)
            temporary_url = url+"/api/v1/repos/acme/cleanup-check"
            temporary_refs = git_refs(url, admin, "acme/cleanup-check")
            shared_repo = api(url+"/api/v1/orgs/acme/repos", "POST", {"name":"cleanup-shared", "private":True, "auto_init":True}, admin)
            lfs_body = "镜像中的共享 LFS 正文".encode()
            lfs_oid = hashlib.sha256(lfs_body).hexdigest()
            lfs_headers = {"Authorization":"Basic "+base64.b64encode(":".join(admin).encode()).decode(), "Content-Type":"application/octet-stream"}
            for repository_path in ("cleanup-check", "cleanup-shared"):
                request = urllib.request.Request(url+f"/acme/{repository_path}.git/info/lfs/objects/{lfs_oid}/{len(lfs_body)}", data=lfs_body, headers=lfs_headers, method="PUT")
                with LOCAL_HTTP.open(request, timeout=15) as response:
                    if response.status!=200:
                        raise RuntimeError("真实 LFS 上传未成功")

            sql_prefix = ["docker", "exec", database, "psql", "-v", "ON_ERROR_STOP=1", "-U", "governance_test", "-d", "governance_test", "-tAc"]
            deletion_endpoint = temporary_url
            deletion_method, deletion_option = "DELETE", None
            if expected_version >= 357:
                api(temporary_url, "DELETE", credentials=admin)
                deletion_base = url+f"/api/v1/governance/repositories/{temporary_repo['id']}"
                pending = api(deletion_base+"/deletion", credentials=admin)
                checks.append(("原生项目删除进入恢复期且旧地址仍可读取", pending["pending"] is not None and pending["archived"] and git_refs(url,admin,"acme/cleanup-check")==temporary_refs))
                deletion_endpoint = deletion_base+"/delete-permanently"
                deletion_method = "POST"
                deletion_option = {"confirmation_path":pending["full_path"], "due_unix":pending["pending"]["due_unix"]}

            def wait_repository_cleanup(repository_id):
                deadline = time.monotonic()+30
                while time.monotonic()<deadline:
                    remaining = run([*sql_prefix, f"SELECT count(*) FROM governance_resource_cleanup WHERE kind='repository' AND resource_id={repository_id}"])
                    if remaining=="0":
                        return
                    time.sleep(0.2)
                raise RuntimeError("项目提交后清理未在三十秒内完成，保留失败结果")
            run([*sql_prefix, "CREATE FUNCTION governance_delete_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type = 'resource.deletion_committed' THEN RAISE EXCEPTION '删除审计故障样本'; END IF; RETURN NEW; END $$; CREATE TRIGGER governance_delete_fault BEFORE INSERT ON governance_audit_event FOR EACH ROW EXECUTE FUNCTION governance_delete_fault();"])
            try:
                api(deletion_endpoint, deletion_method, deletion_option, admin)
                rejected = False
            except urllib.error.HTTPError as error:
                rejected = error.code==500
            finally:
                run([*sql_prefix, "DROP TRIGGER governance_delete_fault ON governance_audit_event; DROP FUNCTION governance_delete_fault();"])
            checks.append(("删除审计故障保留数据库与真实Git文件", rejected and api(temporary_url, credentials=admin)["id"]==temporary_repo["id"] and git_refs(url, admin, "acme/cleanup-check")==temporary_refs))
            api(deletion_endpoint, deletion_method, deletion_option, admin)
            wait_repository_cleanup(temporary_repo["id"])
            run(["docker", "exec", server, "sh", "-c", "test ! -e /data/git/repositories/acme/cleanup-check.git"])
            cleanup_count = run([*sql_prefix, f"SELECT count(*) FROM governance_resource_cleanup WHERE kind='repository' AND resource_id={temporary_repo['id']}"])
            audit_count = run([*sql_prefix, f"SELECT count(*) FROM governance_audit_event WHERE object_id={temporary_repo['id']} AND type IN ('resource.deletion_committed','resource.cleanup_completed')"])
            checks.append(("正常删除完成真实文件清理且永久审计成对保存", cleanup_count=="0" and audit_count=="2"))
            request = urllib.request.Request(url+f"/acme/cleanup-shared.git/info/lfs/objects/{lfs_oid}/shared", headers=lfs_headers)
            with LOCAL_HTTP.open(request, timeout=15) as response:
                shared_read = response.read()==lfs_body
            checks.append(("删除首个仓库后真实LFS共享正文仍可读取", shared_read))
            api(url+"/api/v1/repos/acme/cleanup-shared", "DELETE", credentials=admin)
            if expected_version >= 357:
                shared_deletion = url+f"/api/v1/governance/repositories/{shared_repo['id']}"
                pending = api(shared_deletion+"/deletion", credentials=admin)
                api(shared_deletion+"/delete-permanently", "POST", {"confirmation_path":pending["full_path"], "due_unix":pending["pending"]["due_unix"]}, admin)
            wait_repository_cleanup(shared_repo["id"])
            lfs_path = f"/data/git/lfs/{lfs_oid[:2]}/{lfs_oid[2:4]}/{lfs_oid[4:]}"
            run(["docker", "exec", server, "test", "!", "-e", lfs_path])
            checks.append(("最后引用删除后真实LFS正文完成清理", True))

        evidence["引用证据校验"] = hashlib.sha256(original_refs.encode()).hexdigest()
        if expected_version >= 355:
            deletion_group = api(url+"/api/v1/governance/groups", "POST", {"name":"删除恢复镜像样本", "path":"deletion-check", "visibility":2}, admin)
            deletion_child = api(url+"/api/v1/governance/groups", "POST", {"name":"子组", "path":"child", "parent_id":deletion_group["id"], "visibility":2}, admin)
            deletion_repo = api(url+"/api/v1/orgs/deletion-check/child/repos", "POST", {"name":"firmware", "private":True, "auto_init":True}, admin)
            deletion_refs = git_refs(url, admin, "deletion-check/child/firmware")
            group_endpoint = url+f"/api/v1/governance/groups/{deletion_group['id']}"
            api(url+"/api/v1/orgs/deletion-check", "DELETE", credentials=admin)
            pending_group = api(group_endpoint, credentials=admin)
            checks.append(("原生组织删除进入三十天保留期", pending_group["delete_after"]>time.time()+29*86400 and pending_group["archived"] and pending_group["full_path"]!="deletion-check"))
            checks.append(("待删除群组旧Git地址保留真实引用读取", git_refs(url, admin, "deletion-check/child/firmware")==deletion_refs))
            restored_group = api(group_endpoint+"/restore", "POST", {"revision":pending_group["revision"], "confirmation_path":pending_group["full_path"]}, admin)
            checks.append(("群组恢复还原完整路径与项目归档状态", restored_group["full_path"]=="deletion-check" and restored_group["delete_after"]==0 and not api(url+"/api/v1/repos/deletion-check/child/firmware", credentials=admin)["archived"]))
            pending_group = api(group_endpoint+"/deletion", "POST", {"revision":restored_group["revision"], "confirmation_path":restored_group["full_path"]}, admin)
            api(group_endpoint+"/delete-permanently", "POST", {"revision":pending_group["revision"], "confirmation_path":pending_group["full_path"]}, admin)
            deadline = time.monotonic()+30
            while time.monotonic()<deadline:
                remaining = run([*sql_prefix, "SELECT count(*) FROM governance_resource_cleanup"])
                if remaining=="0":
                    break
                time.sleep(0.2)
            group_rows = run([*sql_prefix, f"SELECT count(*) FROM governance_namespace WHERE id IN ({deletion_group['id']},{deletion_child['id']})"])
            repository_rows = run([*sql_prefix, f"SELECT count(*) FROM repository WHERE id={deletion_repo['id']}"])
            storage_path = "/data/git/repositories/"+deletion_child["compatibility_name"].lower()+"/firmware.git"
            run(["docker", "exec", server, "test", "!", "-e", storage_path])
            checks.append(("整树永久删除完成数据库与真实Git存储清理", group_rows=="0" and repository_rows=="0" and remaining=="0"))
        if expected_version >= 356:
            package_body = "镜像验收中的软件包共享正文".encode()
            package_hash = hashlib.sha256(package_body).hexdigest()
            package_base = url+"/api/packages/"+urllib.parse.quote(admin[0], safe="")+"/generic/content-recovery"
            package_headers = {"Authorization":"Basic "+base64.b64encode(":".join(admin).encode()).decode(), "Content-Type":"application/octet-stream"}
            for version in ("1", "2"):
                request = urllib.request.Request(package_base+"/"+version+"/sample", data=package_body, headers=package_headers, method="PUT")
                with LOCAL_HTTP.open(request, timeout=15) as response:
                    if response.status!=201:
                        raise RuntimeError("软件包实际上传未成功")
            run([*sql_prefix, f"UPDATE package_blob SET created_unix={int(time.time())-365*86400} WHERE hash_sha256='{package_hash}'"])
            api(package_base+"/1/sample", "DELETE", credentials=admin)
            api(url+"/api/v1/admin/cron/cleanup_packages", "POST", credentials=admin)
            request = urllib.request.Request(package_base+"/2/sample", headers=package_headers)
            with LOCAL_HTTP.open(request, timeout=15) as response:
                checks.append(("原生软件包清理保留其他版本共享正文", response.read()==package_body))
            api(package_base+"/2/sample", "DELETE", credentials=admin)
            api(url+"/api/v1/admin/cron/cleanup_packages", "POST", credentials=admin)
            remaining = run([*sql_prefix, f"SELECT count(*) FROM package_blob WHERE hash_sha256='{package_hash}'"])
            tasks_left = run([*sql_prefix, f"SELECT count(*) FROM governance_resource_cleanup WHERE kind='package_blob' AND object_path='{package_hash}'"])
            audit_count = run([*sql_prefix, f"SELECT count(*) FROM governance_audit_event WHERE object_type='package_blob' AND object_path='{package_hash}' AND type IN ('resource.deletion_committed','resource.cleanup_completed')"])
            package_storage = "/data/gitea/packages/"+package_hash[:2]+"/"+package_hash[2:4]+"/"+package_hash
            run(["docker", "exec", server, "test", "!", "-e", package_storage])
            checks.append(("最后一个软件包引用删除后完成持久化清理与审计", remaining=="0" and tasks_left=="0" and audit_count=="2"))
        if expected_version >= 357:
            restore_repo = api(url+"/api/v1/user/repos", "POST", {"name":"restore-project", "private":True, "auto_init":True}, admin)
            restore_path = admin[0]+"/restore-project"
            restore_refs = git_refs(url,admin,restore_path)
            restore_api = url+"/api/v1/repos/"+restore_path
            api(restore_api,"DELETE",credentials=admin)
            restore_base = url+f"/api/v1/governance/repositories/{restore_repo['id']}"
            pending = api(restore_base+"/deletion",credentials=admin)
            restored = api(restore_base+"/restore","POST",{"confirmation_path":pending["full_path"],"due_unix":pending["pending"]["due_unix"]},admin)
            checks.append(("独立项目恢复原路径、权限状态和真实Git引用", restored["pending"] is None and not restored["archived"] and restored["full_path"]==restore_path and git_refs(url,admin,restore_path)==restore_refs))
            bulk_group = api(url+"/api/v1/governance/groups", "POST", {"name":"批量保留期镜像样本", "path":"bulk-check", "visibility":2}, admin)
            bulk_repositories = [api(url+"/api/v1/orgs/bulk-check/repos", "POST", {"name":name,"private":True},admin) for name in ("one","two")]
            api(url+"/api/v1/orgs/bulk-check/repos","DELETE",credentials=admin)
            bulk_states = [api(url+f"/api/v1/governance/repositories/{item['id']}/deletion",credentials=admin) for item in bulk_repositories]
            checks.append(("组织批量删除保留项目并持久化各自删除计划", all(item["pending"] is not None and item["archived"] for item in bulk_states) and api(url+f"/api/v1/governance/groups/{bulk_group['id']}",credentials=admin)["delete_after"]==0))
        if check_expiry:
            expiry_group = api(url+"/api/v1/governance/groups", "POST", {"name":"到期授权镜像样本", "path":"expiry-check", "visibility":2}, admin)
            api(url+"/api/v1/orgs/expiry-check/repos", "POST", {"name":"private", "private":True, "auto_init":True}, admin)
            expiry_endpoint = url+f"/api/v1/governance/groups/{expiry_group['id']}"
            expiry_state = api(expiry_endpoint, credentials=admin)
            api(expiry_endpoint+f"/members/{member_id}", "PUT", {"role":20, "revision":expiry_state["revision"], "expires_unix":int(time.time())+3600}, admin)
            checks.append(("到期前真实HTTPGit按直接群组授权可读", bool(git_refs(url, reviewer, "expiry-check/private"))))
            run([*sql_prefix, f"UPDATE governance_membership SET expires_unix={int(time.time())-60} WHERE scope_type='group' AND scope_id={expiry_group['id']} AND user_id={member_id}"])
            try:
                api(url+"/api/v1/repos/expiry-check/private", credentials=reviewer)
                denied = False
            except urllib.error.HTTPError as error:
                denied = error.code == 404
            checks.append(("到期后原生项目API立即拒绝访问", denied))
            try:
                git_refs(url, reviewer, "expiry-check/private")
                denied = False
            except RuntimeError as error:
                denied = any(text in str(error).lower() for text in ("not found", "403", "404"))
            checks.append(("到期后真实HTTPGit立即拒绝访问", denied))
            for _ in range(2):
                api(url+"/api/v1/admin/cron/governance_expired_grants", "POST", credentials=admin)
            rows = run([*sql_prefix, f"SELECT count(*) FROM governance_membership WHERE scope_type='group' AND scope_id={expiry_group['id']} AND user_id={member_id}"])
            events = run([*sql_prefix, f"SELECT count(*) FROM governance_audit_event WHERE type='member.expired' AND scope_type='group' AND scope_id={expiry_group['id']}"])
            checks.append(("镜像后台清理到期来源且重复执行只有一次审计", rows == "0" and events == "1"))
        if expected_version >= 358:
            request_group = api(url+"/api/v1/governance/groups", "POST", {"name":"访问申请镜像样本", "path":"request-check", "visibility":0}, admin)
            api(url+"/api/v1/orgs/request-check/repos", "POST", {"name":"private", "private":True, "auto_init":True}, admin)
            endpoint = url+f"/api/v1/governance/groups/{request_group['id']}/access-requests"
            request = api(endpoint, "POST", credentials=reviewer)
            own = api(endpoint, credentials=reviewer)
            checks.append(("镜像原生申请不暴露管理员队列", own["own_request"]["id"]==request["id"] and not own["requests"] and not own["can_manage"]))
            try:
                git_refs(url, reviewer, "request-check/private")
                denied = False
            except RuntimeError as error:
                denied = any(text in str(error).lower() for text in ("not found", "403", "404"))
            checks.append(("待审申请不授予私有项目真实Git读取权限", denied))
            pending = api(endpoint, credentials=admin)
            api(endpoint+f"/{request['id']}/approve", "POST", {"revision":pending["revision"], "role":20}, admin)
            checks.append(("批准后继承Reporter并实际读取私有项目Git引用", bool(git_refs(url, reviewer, "request-check/private"))))
            decision_events = api(url+f"/api/v1/governance/audit-events?scope_type=group&scope_id={request_group['id']}", credentials=admin)["events"]
            checks.append(("申请与批准均留下真实身份审计", all(any(event["event_type"]==kind and event["actor"]["name"]==name for event in decision_events) for kind,name in (("access_request.created",reviewer[0]),("access_request.approved",admin[0])))))
            project = api(url+"/api/v1/user/repos", "POST", {"name":"request-project", "private":False, "auto_init":True}, admin)
            endpoint = url+f"/api/v1/governance/repositories/{project['id']}/access-requests"
            request = api(endpoint, "POST", credentials=reviewer)
            pending = api(endpoint, credentials=admin)
            api(endpoint+f"/{request['id']}/approve", "POST", {"revision":pending["revision"], "role":20}, admin)
            project_path = admin[0]+"/request-project"
            api(url+"/api/v1/repos/"+project_path,"PATCH",{"private":True},admin)
            checks.append(("项目申请批准创建直接授权且真实Git可读", bool(git_refs(url,reviewer,project_path))))
            members_url = url+f"/api/v1/governance/repositories/{project['id']}/members"
            members = api(members_url,credentials=admin)
            checks.append(("直接成员API显示申请产生的稳定用户来源", any(item["user_id"]==member_id and item["direct"]["scope_id"]==project["id"] for item in members["members"])))
            api(members_url+f"/{member_id}?revision={members['revision']}","DELETE",credentials=admin)
            try:
                git_refs(url,reviewer,project_path)
                denied = False
            except RuntimeError as error:
                denied = any(text in str(error).lower() for text in ("not found", "403", "404"))
            checks.append(("撤销项目直接来源后实际Git立即拒绝",denied))
        if check_member_sources:
            inherited_repo = api(url+"/api/v1/repos/request-check/private",credentials=admin)
            sources = api(url+f"/api/v1/governance/repositories/{inherited_repo['id']}/members?include_inherited=true",credentials=admin)
            inherited = next((member for member in sources["members"] if member["user_id"]==member_id),None)
            checks.append(("成员总览纳入无直接授权的继承成员", sources["all_sources"] and inherited is not None and not inherited.get("direct") and any(grant["source"]=="inherited" for grant in inherited["grants"])))
            direct = api(url+f"/api/v1/governance/repositories/{inherited_repo['id']}/members",credentials=admin)
            checks.append(("原有直接成员API保持原语义",not direct["all_sources"] and not direct["members"]))
            remaining = api(url+f"/api/v1/governance/repositories/{project['id']}/members?include_inherited=true",credentials=admin)
            checks.append(("总览不复活已撤销的项目直接来源", all(member["user_id"]!=member_id for member in remaining["members"])))
            checks.append(("个人项目所有者显示明确来源", any(any(source["kind"]=="owner" for source in member.get("native_sources",[])) for member in remaining["members"])))
        if expected_version >= 359:
            if not receiver_binary:
                raise RuntimeError("邀请升级验收需要本次专用邮件接收器")
            invited_group = api(url+"/api/v1/governance/groups", "POST", {"name":"邮件邀请镜像样本", "path":"invitation-check", "visibility":2}, admin)
            api(url+"/api/v1/orgs/invitation-check/repos", "POST", {"name":"private", "private":True, "auto_init":True}, admin)
            endpoint = url+f"/api/v1/governance/groups/{invited_group['id']}/invitations"
            invited_group = api(url+f"/api/v1/governance/groups/{invited_group['id']}", credentials=admin)
            invitation = api(endpoint, "POST", {"email":"reviewer@example.test", "role":20, "revision":invited_group["revision"]}, admin)
            checks.append(("镜像邀请响应不返回凭据", not any(key in invitation for key in ("token_hash","token_encrypted","token"))))
            try:
                git_refs(url, reviewer, "invitation-check/private")
                denied = False
            except RuntimeError as error:
                denied = any(text in str(error).lower() for text in ("not found", "403", "404"))
            checks.append(("邀请未接受前真实Git拒绝",denied))
            api(url+"/api/v1/admin/cron/governance_invitations", "POST", credentials=admin)
            delivered = None
            deadline = time.monotonic()+20
            while time.monotonic() < deadline:
                request = urllib.request.Request(receiver_url+"/invitations", headers={"X-Test-Token":receiver_token})
                with LOCAL_HTTP.open(request,timeout=5) as response:
                    delivered = json.load(response).get("reviewer@example.test")
                if delivered and int(delivered["邀请ID"]) == invitation["id"]:
                    break
                time.sleep(0.2)
            if not delivered:
                raise RuntimeError("未收到镜像实际发出的邀请邮件")
            action = url+f"/api/v1/governance/invitations/{invitation['id']}"
            api(action+"/preview", "POST", {"token":delivered["令牌"]}, reviewer)
            api(action+"/accept", "POST", {"token":delivered["令牌"]}, reviewer)
            checks.append(("实际邮件凭据接受后继承权限且真实Git可读",bool(git_refs(url,reviewer,"invitation-check/private"))))
            try:
                api(action+"/accept", "POST", {"token":delivered["令牌"]}, reviewer)
                replay_denied = False
            except urllib.error.HTTPError as error:
                replay_denied = error.code == 404
            checks.append(("已接受邀请凭据不能重复授权",replay_denied))
            decision_events = api(url+f"/api/v1/governance/audit-events?scope_type=group&scope_id={invited_group['id']}",credentials=admin)["events"]
            checks.append(("镜像邀请接受审计使用收件人身份且不含令牌",any(event["event_type"]=="invitation.accepted" and event["actor"]["name"]==reviewer[0] for event in decision_events) and delivered["令牌"] not in json.dumps(decision_events)))
        if expected_version >= 361:
            auditor = ("governance_auditor", secrets.token_urlsafe(24))
            audit_user = api(url+"/api/v1/admin/users", "POST", {"username":auditor[0], "email":"auditor@example.test", "password":auditor[1], "must_change_password":False, "auditor":True}, admin)
            audit_repo = api(url+"/api/v1/orgs/acme/repos", "POST", {"name":"auditor-check", "private":True, "auto_init":True}, admin)
            audit_path = "acme/auditor-check"
            checks.append(("镜像创建全站审计员且保持非管理员",audit_user["is_auditor"] and not audit_user["is_admin"]))
            private_group = api(url+"/api/v1/orgs", "POST", {"username":"auditor-private-group", "visibility":"private"}, admin)
            private_teams = api(url+"/api/v1/orgs/auditor-private-group/teams",credentials=auditor)
            checks.append(("审计员读取未加入的私有群组与原生团队",api(url+"/api/v1/orgs/auditor-private-group",credentials=auditor)["id"] == private_group["id"] and bool(private_teams) and all(api(url+f"/api/v1/teams/{team['id']}",credentials=auditor)["id"] == team["id"] for team in private_teams)))
            checks.append(("审计员读取未加入的私有项目与实例审计",api(url+"/api/v1/repos/"+audit_path,credentials=auditor)["id"] == audit_repo["id"] and bool(api(url+"/api/v1/governance/audit-events?scope_type=instance",credentials=auditor)["events"])))
            for label, endpoint, method, payload in (("管理员入口", "/api/v1/admin/users", "GET", None), ("创建Issue", "/api/v1/repos/"+audit_path+"/issues", "POST", {"title":"只读身份不得创建"})):
                try:
                    api(url+endpoint,method,payload,auditor)
                    rejected = False
                except urllib.error.HTTPError as error:
                    rejected = error.code == 403
                checks.append(("镜像审计员拒绝"+label,rejected))
            with tempfile.TemporaryDirectory(prefix="治理审计员Git验收-") as directory:
                key = Path(directory)/"审计员密钥"
                checkout = Path(directory)/"检出项目"
                run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(key)])
                key_info = api(url+"/api/v1/user/keys", "POST", {"title":"审计员权限验收", "key":key.with_suffix(".pub").read_text().strip()}, auditor)
                git_env = os.environ.copy()
                git_env.update({"GIT_CONFIG_COUNT":"1", "GIT_CONFIG_KEY_0":"http.extraHeader", "GIT_CONFIG_VALUE_0":"Authorization: Basic "+base64.b64encode(":".join(auditor).encode()).decode(), "GIT_CONFIG_GLOBAL":os.devnull, "GIT_CONFIG_NOSYSTEM":"1", "GIT_TERMINAL_PROMPT":"0", "NO_PROXY":"127.0.0.1,localhost", "no_proxy":"127.0.0.1,localhost", "GIT_SSH_COMMAND":"ssh -F /dev/null -i "+shlex.quote(str(key))+" -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes"})
                http_remote = url+"/"+audit_path+".git"
                ssh_remote = f"ssh://git@127.0.0.1:{ssh_port}/{audit_path}.git"
                before_refs = git_refs(url,admin,audit_path)
                run(["git", "clone", http_remote, str(checkout)],env=git_env)
                checks.append(("审计员真实HTTP克隆和SSH读取",bool(run(["git","ls-remote",ssh_remote],env=git_env))))
                for protocol, remote in (("HTTP",http_remote),("SSH",ssh_remote)):
                    result = subprocess.run(["git","-C",str(checkout),"push",remote,"HEAD:refs/heads/auditor-denied"],env=git_env,capture_output=True,text=True,timeout=30)
                    checks.append(("审计员真实"+protocol+"推送拒绝且目标引用未变",result.returncode != 0 and git_refs(url,admin,audit_path)==before_refs))
                members_endpoint = url+f"/api/v1/governance/repositories/{audit_repo['id']}/members"
                members = api(members_endpoint,credentials=admin)
                api(members_endpoint+f"/{audit_user['id']}","PUT",{"role":30,"revision":members["revision"]},admin)
                run(["git","-C",str(checkout),"push",http_remote,"HEAD:refs/heads/auditor-authorized"],env=git_env)
                checks.append(("另获Developer后真实推送成功", "refs/heads/auditor-authorized" in git_refs(url,admin,audit_path)))
                members = api(members_endpoint,credentials=admin)
                api(members_endpoint+f"/{audit_user['id']}?revision={members['revision']}","DELETE",credentials=admin)
                api(url+"/api/v1/admin/users/"+auditor[0],"PATCH",{"auditor":False},admin)
                for protocol, remote in (("HTTP",http_remote),("SSH",ssh_remote)):
                    result = subprocess.run(["git","ls-remote",remote],env=git_env,capture_output=True,text=True,timeout=30)
                    checks.append(("撤销角色后原凭据真实"+protocol+"读取拒绝",result.returncode != 0 and not result.stdout.strip()))
                api(url+"/api/v1/user/keys/"+str(key_info["id"]),"DELETE",credentials=auditor)
            role_events = api(url+f"/api/v1/governance/audit-events?scope_type=user&scope_id={audit_user['id']}&event_type=user.auditor_changed",credentials=admin)["events"]
            checks.append(("镜像授予撤销审计员均保存真实管理员身份",len(role_events)==2 and all(event["actor"]["name"]==admin[0] for event in role_events)))
        if check_account_security:
            # 崩溃恢复会重建应用进程；必须重新登录，不能复用之前的内存会话。
            login_browser = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
            with login_browser.open(url+"/user/login",timeout=15) as response:
                fields = LoginFields()
                fields.feed(response.read().decode())
            fields.fields.update({"user_name":admin[0],"password":admin[1]})
            with login_browser.open(urllib.request.Request(url+"/user/login",data=urllib.parse.urlencode(fields.fields).encode(),method="POST"),timeout=15) as response:
                response.read()
            with login_browser.open(url+"/user/settings",timeout=15) as response:
                if urllib.parse.urlsplit(response.url).path!="/user/settings":
                    raise RuntimeError("账号安全验收的管理会话未建立")
                response.read()
            account = ("security_account", secrets.token_urlsafe(24))
            original_email = "security-account@example.test"
            created_account = api(url+"/api/v1/admin/users", "POST", {"username":account[0], "email":original_email, "password":account[1], "must_change_password":False}, admin)
            account_id = created_account["id"]
            account_path = url+"/api/v1/admin/users/"+account[0]
            replacement = secrets.token_urlsafe(24)
            api(account_path, "PATCH", {"password":replacement, "restricted":True}, admin)
            try:
                api(url+"/api/v1/user", credentials=account)
                old_rejected = False
            except urllib.error.HTTPError as error:
                old_rejected = error.code==401
            account = (account[0], replacement)
            checks.append(("账号安全保存后旧密码拒绝且新密码真实认证成功", old_rejected and api(url+"/api/v1/user", credentials=account)["id"]==account_id))
            run([*sql_prefix, "CREATE FUNCTION governance_account_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type = 'user.auditor_changed' THEN RAISE EXCEPTION '组合保存审计故障'; END IF; RETURN NEW; END $$; CREATE TRIGGER governance_account_fault BEFORE INSERT ON governance_audit_event FOR EACH ROW EXECUTE FUNCTION governance_account_fault();"])
            try:
                api(account_path,"PATCH",{"password":secrets.token_urlsafe(24),"auditor":True,"email":"failed-account@example.test"},admin)
                denied = False
            except urllib.error.HTTPError as error:
                denied = error.code==500
            finally:
                run([*sql_prefix,"DROP TRIGGER governance_account_fault ON governance_audit_event; DROP FUNCTION governance_account_fault();"])
            retained = api(url+"/api/v1/user",credentials=account)
            checks.append(("镜像组合保存审计故障完整回滚密码邮箱与角色",denied and retained["email"]==original_email and not retained["is_auditor"]))
            web_password = secrets.token_urlsafe(24)
            edit_form = {"user_name":account[0],"login_type":"0-0","email":original_email,"active":"on","restricted":"on","password":web_password}
            request = urllib.request.Request(url+f"/-/admin/users/{account_id}/edit",data=urllib.parse.urlencode(edit_form).encode(),method="POST")
            with login_browser.open(request,timeout=15) as response:
                web_saved = response.status==200 and urllib.parse.urlsplit(response.url).path==f"/-/admin/users/{account_id}"
                response.read()
            account = (account[0],web_password)
            checks.append(("镜像原生管理员表单保存安全设置后真实密码认证成功",web_saved and api(url+"/api/v1/user",credentials=account)["id"]==account_id))
            edit_form.update({"user_name":"security-renamed","password":secrets.token_urlsafe(24)})
            try:
                with login_browser.open(urllib.request.Request(url+f"/-/admin/users/{account_id}/edit",data=urllib.parse.urlencode(edit_form).encode(),method="POST"),timeout=15) as response:
                    response.read()
                denied = False
            except urllib.error.HTTPError as error:
                denied = error.code==400
            checks.append(("镜像拒绝混合改名与安全设置且旧账号凭据保持有效",denied and api(url+"/api/v1/user",credentials=account)["id"]==account_id))
            with login_browser.open(urllib.request.Request(url+f"/-/admin/users/{account_id}/rename",data=urllib.parse.urlencode({"user_name":"security-renamed"}).encode(),method="POST"),timeout=15) as response:
                response.read()
            account = ("security-renamed",account[1])
            checks.append(("镜像独立账号改名保留稳定ID和密码",api(url+"/api/v1/user",credentials=account)["id"]==account_id))
            account_events = api(url+f"/api/v1/governance/audit-events?scope_type=user&scope_id={account_id}",credentials=admin)["events"]
            password_events = [event for event in account_events if event["event_type"]=="credential.password_changed"]
            checks.append(("镜像密码审计保存网页与API真实管理员且无秘密正文",len(password_events)==2 and {event["actor"]["transport"] for event in password_events}=={"web","api"} and all(event["actor"]["name"]==admin[0] for event in password_events) and all(value not in json.dumps(account_events) for value in (replacement,web_password))))
            command = ["docker","exec","--user","git",server,"gitea","admin","user","must-change-password","--config","/data/gitea/conf/app.ini"]
            run([*command,account[0]])
            must_change = run([*sql_prefix,f'SELECT must_change_password FROM "user" WHERE id={account_id}'])
            run([*command,"--unset",account[0]])
            after_unset = api(url+"/api/v1/user",credentials=account)
            command_events = api(url+f"/api/v1/governance/audit-events?scope_type=user&scope_id={account_id}&event_type=user.security_changed",credentials=admin)["events"]
            checks.append(("镜像管理命令强制改密与取消均保存命令行系统审计",must_change=="t" and after_unset["id"]==account_id and len([event for event in command_events if event["actor"]["transport"]=="cli" and event["actor"]["kind"]=="system"])==2))
        if check_branch_protection:
            protection_endpoint = url+"/api/v1/repos/acme/firmware/branch_protections"
            audit_endpoint = url+f"/api/v1/governance/audit-events?scope_type=repository&scope_id={repo['id']}"
            api(protection_endpoint, "POST", {"rule_name":"audit-fixture", "enable_push":True, "enable_force_push":False}, admin)
            protection_id = int(run([*sql_prefix, f"SELECT id FROM protected_branch WHERE repo_id={repo['id']} AND branch_name='audit-fixture'"]))
            created = api(audit_endpoint+"&event_type=repository.branch_protection_created",credentials=admin)["events"]
            checks.append(("镜像原生保护规则创建记录实际操作者与路径", any(event["entity_id"]==protection_id and event["actor"]["name"]==admin[0] and event["entity_path"]=="acme/firmware:audit-fixture" for event in created)))
            api(protection_endpoint+"/audit-fixture", "PATCH", {"enable_force_push":True, "required_approvals":2}, admin)
            changed = api(audit_endpoint+"&event_type=repository.branch_protection_updated",credentials=admin)["events"]
            changes = [event for event in changed if event["entity_id"]==protection_id]
            checks.append(("镜像保护规则审计保存强推与审批人数前后值", len(changes)==1 and changes[0]["details"]["before"]["can_force_push"] is False and changes[0]["details"]["after"]["can_force_push"] is True and changes[0]["details"]["after"]["required_approvals"]==2))
            fault = "CREATE FUNCTION protection_audit_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type LIKE 'repository.branch_protection_%' THEN RAISE EXCEPTION '保护规则审计注入故障'; END IF; RETURN NEW; END $$; CREATE TRIGGER protection_audit_fault BEFORE INSERT ON governance_audit_event FOR EACH ROW EXECUTE FUNCTION protection_audit_fault();"
            run([*sql_prefix,fault])
            try:
                for method, path, body in (("PATCH",protection_endpoint+"/audit-fixture",{"required_approvals":0}), ("DELETE",protection_endpoint+"/audit-fixture",None), ("POST",protection_endpoint,{"rule_name":"failed-audit-fixture"})):
                    try:
                        api(path,method,body,admin)
                        rejected = False
                    except urllib.error.HTTPError as error:
                        rejected = error.code==500
                    checks.append(("镜像保护规则审计故障拒绝"+method,rejected))
            finally:
                run([*sql_prefix,"DROP TRIGGER protection_audit_fault ON governance_audit_event; DROP FUNCTION protection_audit_fault();"])
            retained = api(protection_endpoint+"/audit-fixture",credentials=admin)
            all_rules = api(protection_endpoint,credentials=admin)
            checks.append(("镜像保护规则审计故障后原规则完整保留且失败创建无残留",retained["required_approvals"]==2 and retained["enable_force_push"] is True and not any(rule["rule_name"]=="failed-audit-fixture" for rule in all_rules)))
            api(protection_endpoint+"/audit-fixture", "DELETE",credentials=admin)
            deleted = api(audit_endpoint+"&event_type=repository.branch_protection_deleted",credentials=admin)["events"]
            checks.append(("镜像原生保护规则删除保留历史配置",any(event["entity_id"]==protection_id and event["details"]["after"] is None and event["details"]["before"]["required_approvals"]==2 for event in deleted)))
        if check_tag_protection:
            tag_rules = url+"/api/v1/repos/acme/firmware/tag_protections"
            tag_api = url+"/api/v1/repos/acme/firmware/tags"
            tag_audit = url+f"/api/v1/governance/audit-events?scope_type=repository&scope_id={repo['id']}"
            protected = api(tag_rules,"POST",{"name_pattern":"audit-tag-*", "whitelist_usernames":[reviewer[0]]},admin)
            tag_rule = tag_rules+"/"+str(protected["id"])
            created = api(tag_audit+"&event_type=repository.tag_protection_created",credentials=admin)["events"]
            checks.append(("镜像保护标签创建保存范围和操作者",any(event["entity_id"]==protected["id"] and event["actor"]["name"]==admin[0] and event["entity_path"]=="acme/firmware:audit-tag-*" for event in created)))
            before_refs = git_refs(url,admin)
            try:
                api(tag_api,"POST",{"tag_name":"audit-tag-proof","target":"main"},admin)
                denied = False
            except urllib.error.HTTPError as error:
                denied = error.code==403
            checks.append(("镜像标签保护拒绝非名单管理员且实际引用不变",denied and git_refs(url,admin)==before_refs))
            api(tag_rule,"PATCH",{"whitelist_usernames":[admin[0]]},admin)
            api(tag_api,"POST",{"tag_name":"audit-tag-proof","target":"main"},admin)
            checks.append(("镜像修改标签名单后原生创建成功且实际引用存在","refs/tags/audit-tag-proof" in git_refs(url,admin)))
            changed = api(tag_audit+"&event_type=repository.tag_protection_updated",credentials=admin)["events"]
            changes = [event for event in changed if event["entity_id"]==protected["id"]]
            checks.append(("镜像标签保护修改保存名单前后值",len(changes)==1 and changes[0]["details"]["before"]["allowlist_user_ids"]==[reviewer_id] and len(changes[0]["details"]["after"]["allowlist_user_ids"])==1 and changes[0]["actor"]["name"]==admin[0]))
            run([*sql_prefix,"CREATE FUNCTION tag_protection_audit_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type LIKE 'repository.tag_protection_%' THEN RAISE EXCEPTION '标签保护审计注入故障'; END IF; RETURN NEW; END $$; CREATE TRIGGER tag_protection_audit_fault BEFORE INSERT ON governance_audit_event FOR EACH ROW EXECUTE FUNCTION tag_protection_audit_fault();"])
            try:
                for method, path, body in (("PATCH",tag_rule,{"whitelist_usernames":[reviewer[0]]}), ("DELETE",tag_rule,None), ("POST",tag_rules,{"name_pattern":"failed-tag-*", "whitelist_usernames":[admin[0]]})):
                    try:
                        api(path,method,body,admin)
                        rejected = False
                    except urllib.error.HTTPError as error:
                        rejected = error.code==500
                    checks.append(("镜像标签保护审计故障拒绝"+method,rejected))
            finally:
                run([*sql_prefix,"DROP TRIGGER tag_protection_audit_fault ON governance_audit_event; DROP FUNCTION tag_protection_audit_fault();"])
            retained = api(tag_rule,credentials=admin)
            checks.append(("镜像标签规则故障回滚保留名单且失败创建无残留",retained["whitelist_usernames"]==[admin[0]] and not any(rule["name_pattern"]=="failed-tag-*" for rule in api(tag_rules,credentials=admin))))
            api(tag_rule,"DELETE",credentials=admin)
            deleted = api(tag_audit+"&event_type=repository.tag_protection_deleted",credentials=admin)["events"]
            checks.append(("镜像删除标签保护保留历史配置",any(event["entity_id"]==protected["id"] and event["details"]["after"] is None and event["details"]["before"]["name_pattern"]=="audit-tag-*" for event in deleted)))
        if ui_ready:
            ui_ready.parent.mkdir(parents=True, exist_ok=True)
            descriptor = os.open(ui_ready, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            with os.fdopen(descriptor, "w", encoding="utf-8") as output:
                json.dump({"地址": url, "用户名": admin[0], "密码": admin[1], "项目ID": repo["id"], "群组ID":org["id"], "容器": server, "受邀用户名": reviewer[0], "受邀密码": reviewer[1], "邮件接收地址": receiver_url, "邮件接收凭据": receiver_token,
                           **({"审计员用户名":auditor[0], "审计员密码":auditor[1], "审计员ID":audit_user["id"], "审计项目ID":audit_repo["id"]} if expected_version >= 361 else {})}, output, ensure_ascii=False)
            print("隔离页面已就绪，等待页面验收释放标记；最长保留十五分钟。", flush=True)
            deadline = time.monotonic()+900
            while not ui_release.exists() and time.monotonic() < deadline:
                time.sleep(0.5)
            evidence["页面验收等待"] = "已释放" if ui_release.exists() else "等待超时，自动清理"
    except Exception as error:
        evidence["错误"] = str(error)
    finally:
        if ui_ready:
            ui_ready.unlink(missing_ok=True)
            ui_release.unlink(missing_ok=True)
        evidence["资源清理"] = []
        for kind, name in reversed(resources):
            args = ["docker", "rm", "--force", "--volumes", name] if kind == "container" else ["docker", kind, "rm", name]
            result = subprocess.run(args, capture_output=True, text=True)
            removed = result.returncode == 0 or "No such" in result.stderr
            evidence["资源清理"].append({"类型": kind, "名称": name, "结果": "完成" if removed else "失败"})
            if not removed:
                evidence["结果"] = "失败"
    complete_evidence(evidence, checks)
    return evidence


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-image", action="append", required=True, help="已在本机准备好的原版镜像，可重复指定")
    parser.add_argument("--target-image", default="gitea-governance-dev:streams")
    parser.add_argument("--postgres-image", default="postgres:17.11-bookworm")
    parser.add_argument("--expected-db-version", type=int, default=346)
    parser.add_argument("--receiver-binary", type=Path, help="已构建的 Linux amd64 隔离审计接收器，用于外送故障验收")
    parser.add_argument("--ui-ready-file", type=Path, help="最后一条升级路径就绪后写入受限临时凭据文件，供页面验收使用")
    parser.add_argument("--ui-release-file", type=Path, help="页面验收完成时创建此标记，触发资源清理")
    parser.add_argument("--check-expiry", action="store_true", help="验证到期即时撤权、实际 Git 拒绝和后台审计清理")
    parser.add_argument("--check-member-sources", action="store_true", help="验证全部成员来源与旧直接成员接口兼容")
    parser.add_argument("--check-tag-protection", action="store_true", help="验证标签保护审计、故障回滚与真实引用")
    parser.add_argument("--check-branch-protection", action="store_true", help="验证原生保护分支规则审计及故障回滚")
    parser.add_argument("--check-account-security", action="store_true", help="验证账号组合保存、审计故障回滚与独立改名")
    args = parser.parse_args()
    if bool(args.ui_ready_file) != bool(args.ui_release_file):
        parser.error("页面验收必须同时提供就绪文件和释放标记")
    if args.ui_ready_file and (args.ui_ready_file.exists() or args.ui_release_file.exists()):
        parser.error("页面验收文件已存在，不能覆盖其他运行")
    report = {"任务": "治理基础镜像升级样本验证", "UTC时间": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
              "边界": "验证固定历史样本、真实 HTTP Git 与 SSH 密钥读取，不代表全部数据迁移、三项原生功能、全部 SSH/LFS、容量或生产升级验收。", "升级路径": []}
    for index, source in enumerate(args.source_image):
        print(f"正在隔离验证升级来源：{source}", flush=True)
        last = index == len(args.source_image)-1
        evidence = verify_upgrade(source, args.target_image, args.postgres_image, args.expected_db_version,
                                  args.ui_ready_file if last else None, args.ui_release_file if last else None, args.receiver_binary, args.check_expiry, args.check_member_sources, args.check_account_security, args.check_branch_protection, args.check_tag_protection)
        report["升级路径"].append(evidence)
        print(f"本条升级样本结果：{evidence['结果']}", flush=True)
    report["本次结论"] = "通过" if all(item["结果"] == "通过" for item in report["升级路径"]) else "失败"
    report["整项实施结论"] = "未完成"
    target = ROOT / "治理验收记录" / (time.strftime("%Y%m%d-%H%M%S") + "-升级样本.json")
    target.parent.mkdir(parents=True, exist_ok=True)
    with target.open("x", encoding="utf-8") as output:
        json.dump(report, output, ensure_ascii=False, indent=2)
        output.write("\n")
    print(f"报告：{target}", flush=True)
    return 0 if report["本次结论"] == "通过" else 1


if __name__ == "__main__":
    raise SystemExit(main())
