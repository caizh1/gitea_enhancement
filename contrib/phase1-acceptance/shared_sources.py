#!/usr/bin/env python3
"""用独立夹具验收群组与项目共享的上限、来源撤销和真实到期。"""

import argparse
import base64
import hashlib
import http.cookiejar
import json
import os
import pathlib
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

from job_token_boundary import Client


DENIED = {401, 403, 404}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("stage", choices=("prepare", "prepare_resume", "config_boundary", "config_effective", "group_reduce", "group_expire", "group_config_effective", "group_config_revoke", "repo_overlap", "repo_source_current", "repo_remove_direct", "repo_remove_team", "repo_expire", "artifact_prepare", "artifact_remove_direct", "artifact_remove_team", "artifact_remove_share"))
    parser.add_argument("--base", required=True)
    parser.add_argument("--owner", required=True)
    parser.add_argument("--owner-credentials", type=pathlib.Path, required=True)
    parser.add_argument("--binary", type=pathlib.Path, required=True)
    parser.add_argument("--work-path", type=pathlib.Path, required=True)
    parser.add_argument("--config", type=pathlib.Path, required=True)
    parser.add_argument("--credentials", type=pathlib.Path, required=True)
    parser.add_argument("--state", type=pathlib.Path, required=True)
    args = parser.parse_args()
    if args.base.rstrip("/") not in ("http://127.0.0.1:13043", "http://localhost:13043"):
        raise RuntimeError("只允许本机隔离实例13043")
    if args.stage == "prepare":
        if args.state.exists() or args.credentials.exists():
            raise RuntimeError("夹具已存在，不覆盖")
        args.state.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        suffix = format(time.time_ns(), "x")[-9:]
        names = {kind: "p1share-" + kind + "-" + suffix for kind in ("admin", "user", "out", "g", "p", "b", "c")}
        state = {"说明": "M12/M13/M10独立共享来源验收", "阶段": [], "步骤": [], "名称": names,
                 "运行实例": args.base.rstrip("/"), "二进制SHA256": hashlib.sha256(args.binary.read_bytes()).hexdigest()}
        original = json.loads(args.owner_credentials.read_text())
        owner_password = original.get(args.owner, original.get("password"))
        if not isinstance(owner_password, str):
            raise RuntimeError("Owner私有凭据缺失")
        passwords = {names["admin"]: secrets.token_urlsafe(27), names["user"]: secrets.token_urlsafe(27),
                     names["out"]: secrets.token_urlsafe(27), args.owner: owner_password}
    else:
        if not args.state.exists() or not args.credentials.exists():
            raise RuntimeError("先运行prepare")
        state = json.loads(args.state.read_text())
        names = state["名称"]
        passwords = json.loads(args.credentials.read_text())
        if args.stage in state["阶段"]:
            raise RuntimeError("阶段已完成，不重复执行")
    if args.stage != "prepare" and args.state.stat().st_mode & 0o077:
        raise RuntimeError("私有结果文件权限过宽")

    def save():
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")
        args.state.chmod(0o600)

    def check(label, actual, expected):
        ok = actual in expected if isinstance(expected, set) else actual == expected
        state["步骤"].append({"阶段": args.stage, "检查": label, "实际": actual,
                          "期望": sorted(expected) if isinstance(expected, set) else expected, "通过": ok})
        save()
        if not ok:
            raise AssertionError(f"{label}：实际 {actual}，期望 {expected}")

    def api(label, path, method="GET", body=None, user=args.owner, expected=200):
        code, result = client.call("/api/v1" + path, method, body, user)
        check(label, code, expected)
        return result

    def group(kind):
        return state["群组"][kind]

    def fresh_group(kind):
        return api("读取群组最新修订 " + kind, f"/governance/groups/{group(kind)['id']}")

    def group_share(invited, role, expires=0, remove=False):
        source = fresh_group("g")
        path = f"/governance/groups/{source['id']}/shares/{group(invited)['id']}"
        if remove:
            api("撤销群组共享 " + invited, path + "?revision=" + str(source["revision"]), "DELETE", expected=204)
        else:
            api("设置群组共享 " + invited, path, "PUT",
                {"max_role": role, "expires_unix": expires, "revision": source["revision"]}, expected=204)

    def repo_share(role, expires=0, remove=False):
        repo = state["仓库"]["project"]
        current = api("读取项目共享修订", f"/governance/repositories/{repo['id']}/shares")
        path = f"/governance/repositories/{repo['id']}/shares/{group('b')['id']}"
        if remove:
            api("撤销项目共享", path + "?revision=" + str(current["revision"]), "DELETE", expected=204)
        else:
            api("设置项目共享", path, "PUT",
                {"max_role": role, "expires_unix": expires, "revision": current["revision"]}, expected=204)

    def basic(user):
        return "Basic " + base64.b64encode((user + ":" + passwords[user]).encode()).decode()

    web_opener = None

    def web(label, path):
        nonlocal web_opener
        if web_opener is None:
            web_opener = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
            login = urllib.parse.urlencode({"user_name": args.owner, "password": passwords[args.owner]}).encode()
            with web_opener.open(args.base.rstrip("/") + "/user/login", data=login, timeout=25) as response:
                check("Owner网页登录", response.status, 200)
        with web_opener.open(args.base.rstrip("/") + path, timeout=25) as response:
            check(label + " 页面HTTP", response.status, 200)
            return response.read().decode(errors="replace")

    def generic(label, method, user, version, expected, payload=None, digest=None):
        name = urllib.parse.quote(state["包名"], safe="")
        owner = urllib.parse.quote(group("g")["compatibility_name"], safe="")
        path = f"/api/packages/{owner}/generic/{name}/{version}/payload.txt"
        request = urllib.request.Request(args.base.rstrip("/") + path, method=method, data=payload,
            headers={"Authorization": basic(user), "Content-Type": "application/octet-stream"})
        try:
            with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request, timeout=25) as response:
                code, raw = response.status, response.read()
        except urllib.error.HTTPError as error:
            code, raw = error.code, error.read(1024)
            error.close()
        check(label + " HTTP", code, expected)
        if digest is not None and code == 200:
            check(label + " 内容SHA256", hashlib.sha256(raw).hexdigest(), digest)

    def asset(label, user, expected):
        path = urllib.parse.urlsplit(state["项目附件"]["下载URL"]).path
        request = urllib.request.Request(args.base.rstrip("/") + path,
            headers={"Authorization": basic(user)})
        try:
            with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request, timeout=25) as response:
                code, raw = response.status, response.read()
        except urllib.error.HTTPError as error:
            code, raw = error.code, error.read(1024)
            error.close()
        check(label + " 附件HTTP", code, expected)
        if code == 200:
            check(label + " 附件SHA256", hashlib.sha256(raw).hexdigest(), state["项目附件"]["内容SHA256"])

    def git(label, repo_kind, user, writable=False, allowed=True):
        repo = state["仓库"][repo_kind]
        url = args.base.rstrip("/") + "/" + repo["full_name"] + ".git"
        env = os.environ.copy()
        env.update(GIT_TERMINAL_PROMPT="0", GIT_CONFIG_COUNT="2", GIT_CONFIG_KEY_0="http.extraHeader",
                   GIT_CONFIG_VALUE_0="Authorization: " + basic(user), GIT_CONFIG_KEY_1="credential.helper",
                   GIT_CONFIG_VALUE_1="")
        if writable:
            index = state.get("推送序号", 0) + 1
            state["推送序号"] = index
            save()
            branch = "probe-" + names["g"].split("-")[-1] + "-" + str(index)
            command = ["git", "-C", state["本地Git"], "push", "--porcelain", url, "HEAD:refs/heads/" + branch]
        else:
            command = ["git", "ls-remote", url]
        result = subprocess.run(command, env=env, capture_output=True, text=True, timeout=35)
        check(label + " Git退出判断", result.returncode == 0, allowed)
        if allowed and not writable:
            check(label + " 引用非空", bool(result.stdout.strip()), True)

    def abilities(label, group_kind, readable, push, packages_write):
        code, view = client.call(f"/api/v1/governance/groups/{group(group_kind)['id']}", user=names["user"])
        check(label + " 群组读取", code, 200 if readable else 404)
        if readable:
            check(label + " 推送能力", bool(view["abilities"].get("push_code")), push)
            check(label + " 包写能力", bool(view["abilities"].get("write_packages")), packages_write)
            state.setdefault("来源快照", {})[label] = [
                {key: grant.get(key) for key in ("source", "scope_type", "scope_id", "role", "ceiling_role", "share_id")}
                for grant in view["grants"]]
            save()

    def source_view(label, expected=None):
        repo = state["仓库"]["project"]
        query = urllib.parse.quote(names["user"], safe="")
        view = api(label + " 项目展示来源", f"/governance/repositories/{repo['id']}/members/view?q={query}")
        members = [item for item in view["members"] if item["username"] == names["user"]]
        check(label + " 目标账号唯一", len(members), 1 if expected else 0)
        sources = members[0]["sources"] if members else []
        kinds = sorted(item["kind"] for item in sources)
        if expected is not None:
            check(label + " 来源种类", kinds, sorted(expected))
        state.setdefault("项目展示来源快照", {})[label] = {
            "角色": members[0]["role_name"] if members else "无有效授权",
            "来源": [{key: item.get(key) for key in ("kind", "name", "role", "ceiling", "expired")}
                   for item in sources],
            "共享关系": view["shares"],
        }
        save()
        return view

    def ui_urls():
        base = args.base.rstrip("/")
        state["UI入口"] = {
            "群组共享": f"{base}/governance/groups/{group('g')['id']}?tab=shares",
            "共享成员": f"{base}/governance/groups/{group('g')['id']}?tab=members",
            "群组源配置": f"{base}/governance/groups/{group('g')['id']}?tab=settings",
            "被邀请组配置": f"{base}/governance/groups/{group('b')['id']}?tab=settings",
            "项目共享与来源": f"{base}/{state['仓库']['project']['full_name']}/collaborators",
            "共享群组项目列表": f"{base}/{group('b')['full_path']}?tab=shared_projects",
            "目标项目": f"{base}/{state['仓库']['project']['full_name']}",
            "兄弟私有项目": f"{base}/{state['仓库']['sibling']['full_name']}",
        }
        save()

    def finish_prepare():
        config = api("群组源保护规则基线", f"/governance/groups/{group('g')['id']}/branch-protections")
        check("群组源规则初始空", len(config["rules"] or []), 0)
        invited = api("被邀请组规则基线", f"/governance/groups/{group('b')['id']}/branch-protections")
        check("被邀请组规则初始空", len(invited["rules"] or []), 0)
        api("配置仅源群组保护规则", f"/governance/groups/{group('g')['id']}/branch-protections", "POST",
            {"revision": config["revision"], "rule_name": "main", "push_role": 40, "merge_role": 40}, expected=201)
        state["包名"] = "shared-source-" + names["g"].split("-")[-1]
        payload = ("一期群组共享包验收 " + state["包名"] + "\n").encode()
        state["包内容SHA256"] = hashlib.sha256(payload).hexdigest()
        save()
        generic("Owner发布独立Generic", "PUT", args.owner, "1.0.0", 201, payload=payload)
        generic("未授权账号读Generic", "GET", names["user"], "1.0.0", DENIED)
        for kind in ("group", "project", "sibling"):
            git("初始无授权 " + kind, kind, names["user"], allowed=False)
        local = args.state.parent / "git-probe"
        local.mkdir(mode=0o700)
        state["本地Git"] = str(local)
        subprocess.run(["git", "init", "-q", str(local)], check=True)
        subprocess.run(["git", "-C", str(local), "config", "user.name", "Phase1 Share Probe"], check=True)
        subprocess.run(["git", "-C", str(local), "config", "user.email", "share-probe@example.invalid"], check=True)
        (local / "README.md").write_text("一期共享权限真实推送探针。\n")
        subprocess.run(["git", "-C", str(local), "add", "README.md"], check=True)
        subprocess.run(["git", "-C", str(local), "commit", "-qm", "test: add share permission probe"], check=True)
        group_share("b", 20)
        group_share("c", 30)
        abilities("双共享上限", "g", True, True, True)
        git("双共享Developer读", "group", names["user"])
        git("双共享Developer推送", "group", names["user"], writable=True)
        generic("双共享Developer包读", "GET", names["user"], "1.0.0", 200,
                digest=state["包内容SHA256"])
        generic("双共享Developer包写", "PUT", names["user"], "2.0.0", 201,
                payload=("一期Developer写包 " + state["包名"] + "\n").encode())
        invited = api("共享后被邀请组规则", f"/governance/groups/{group('b')['id']}/branch-protections")
        check("共享后被邀请组未继承源保护", len(invited["rules"] or []), 0)
        source = api("共享后源组规则", f"/governance/groups/{group('g')['id']}/branch-protections")
        check("源组规则仍存在", len(source["rules"] or []), 1)
        repo_share(20)
        git("项目共享Reporter读", "project", names["user"])
        git("项目共享Reporter拒推", "project", names["user"], writable=True, allowed=False)
        git("兄弟私有仓始终拒读", "sibling", names["user"], allowed=False)
        source_view("仅项目共享", ["shared"])
        ui_urls()

    try:
        if args.stage == "prepare":
            admin = names["admin"]
            command = [str(args.binary), "--work-path", str(args.work_path), "--config", str(args.config),
                       "admin", "user", "create", "--username", admin,
                       "--email", admin + "@example.invalid", "--password", passwords[admin],
                       "--admin", "--must-change-password=false"]
            result = subprocess.run(command, capture_output=True, text=True, timeout=90)
            check("正式CLI建临时管理员", result.returncode, 0)
            args.credentials.write_text(json.dumps(passwords, ensure_ascii=False, indent=2) + "\n")
            args.credentials.chmod(0o600)
        client = Client(args.base, args.credentials, args.owner)
        if args.stage == "prepare":
            owner = api("Owner当前身份", "/user")
            check("Owner非实例管理员", owner["is_admin"], False)
            admin = api("临时管理员当前身份", "/user", user=names["admin"])
            check("临时管理员身份", admin["is_admin"], True)
            state["用户ID"] = {}
            for kind in ("user", "out"):
                name = names[kind]
                created = api("创建独立普通账号 " + kind, "/admin/users", "POST",
                    {"username": name, "email": name + "@example.invalid", "password": passwords[name],
                     "must_change_password": False, "restricted": False}, names["admin"], 201)
                check("独立账号非管理员 " + kind, created["is_admin"], False)
                state["用户ID"][kind] = created["id"]
                save()
            api("停用临时管理员", "/admin/users/" + names["admin"], "PATCH",
                {"active": False, "admin": False, "prohibit_login": True}, names["admin"])
            code, _ = client.call("/api/v1/user", user=names["admin"])
            check("临时管理员旧凭据拒绝", code in DENIED, True)
            state["群组"] = {}
            for kind in ("g", "p", "b", "c"):
                created = api("创建独立私有组 " + kind, "/governance/groups", "POST",
                    {"name": "一期共享来源验收 " + kind, "path": names[kind], "parent_id": 0, "visibility": 2},
                    expected=201)
                state["群组"][kind] = {key: created[key] for key in ("id", "full_path", "compatibility_name", "revision")}
                save()
            for kind in ("b", "c"):
                current = fresh_group(kind)
                api("邀请组直接Maintainer " + kind,
                    f"/governance/groups/{current['id']}/members/{state['用户ID']['user']}", "PUT",
                    {"role": 40, "revision": current["revision"]}, expected=204)
            state["仓库"] = {}
            for kind, host, name in (("group", "g", "group-target"), ("project", "p", "project-target"),
                                     ("sibling", "p", "sibling-private")):
                created = api("创建独立私有仓 " + kind, "/orgs/" + group(host)["compatibility_name"] + "/repos",
                    "POST", {"name": name, "private": True, "auto_init": True}, expected=201)
                state["仓库"][kind] = {"id": created["id"], "full_name": created["full_name"]}
                save()
            finish_prepare()
        elif args.stage == "prepare_resume":
            state["历史首错"] = state.pop("首错", None)
            finish_prepare()
        elif args.stage == "config_boundary":
            marker = "SHARED_SOURCE_MARKER"
            source_name = group("b")["compatibility_name"]
            api("在被邀请组B配置独立变量", f"/orgs/{source_name}/actions/variables/{marker}", "POST",
                {"value": "仅B配置-" + names["b"], "description": "共享配置隔离验收"}, expected=201)
            own = api("读取B变量列表", f"/orgs/{source_name}/actions/variables")
            check("B变量存在", any(item["name"] == marker for item in own), True)
            for kind in ("g", "p"):
                target = api("读取目标组变量 " + kind,
                    f"/orgs/{group(kind)['compatibility_name']}/actions/variables")
                check("目标组不继承B变量 " + kind, any(item["name"] == marker for item in target), False)
            for kind in ("group", "project"):
                target = api("读取目标仓变量 " + kind,
                    "/repos/" + state["仓库"][kind]["full_name"] + "/actions/variables")
                check("目标仓未复制B变量 " + kind, any(item["name"] == marker for item in target), False)
            token = api("获取仅B组Runner注册令牌", f"/orgs/{source_name}/actions/runners/registration-token", "POST")["token"]
            directory = args.state.parent / "b-runner"
            directory.mkdir(mode=0o700, exist_ok=False)
            config = directory / "runner.yaml"
            config.write_text(f"runner:\n  file: {directory}/.runner\n  labels:\n    - p1share-b:host\ncache:\n  enabled: false\n")
            config.chmod(0o600)
            runner_name = "p1share-b-" + names["b"].split("-")[-1]
            log = directory / "registration.log"
            with log.open("w") as output:
                result = subprocess.run(["outputs/group-subgroup-phase1-20260923/gitea-runner", "register",
                    "--no-interactive", "--config", str(config), "--instance", args.base,
                    "--token", token, "--name", runner_name, "--labels", "p1share-b:host"],
                    stdout=output, stderr=subprocess.STDOUT, timeout=35)
            log.chmod(0o600)
            check("仅B组Runner注册退出码", result.returncode, 0)
            listing = api("读取B组Runner列表", f"/orgs/{source_name}/actions/runners")
            matches = [runner for runner in listing["runners"] if runner["name"] == runner_name]
            check("B组专属Runner唯一", len(matches), 1)
            runner_id = matches[0]["id"]
            state["B专属Runner"] = {"id": runner_id, "name": runner_name, "运行状态": "未启动daemon"}
            save()
            for kind in ("g", "p"):
                target = api("读取目标组Runner " + kind,
                    f"/orgs/{group(kind)['compatibility_name']}/actions/runners")
                check("目标组不继承B Runner " + kind,
                    any(runner["id"] == runner_id for runner in target["runners"]), False)
            for kind in ("group", "project"):
                target = api("读取目标仓Runner " + kind,
                    "/repos/" + state["仓库"][kind]["full_name"] + "/actions/runners")
                check("目标仓不继承B Runner " + kind,
                    any(runner["id"] == runner_id for runner in target["runners"]), False)
        elif args.stage == "config_effective":
            marker = "PHYSICAL_SOURCE_MARKER"
            api("在项目物理祖先P配置对照变量",
                f"/orgs/{group('p')['compatibility_name']}/actions/variables/{marker}", "POST",
                {"value": "仅P物理祖先-" + names["p"], "description": "物理继承对照"}, expected=201)
            invited = web("被邀请组B变量页", f"/org/{group('b')['compatibility_name']}/settings/actions/variables")
            check("被邀请组B有效页含自身变量", "SHARED_SOURCE_MARKER" in invited, True)
            target = web("消费者仓68有效变量页", "/" + state["仓库"]["project"]["full_name"] + "/settings/actions/variables")
            check("消费者仓68显示物理祖先P变量", marker in target, True)
            check("消费者仓68排除共享来源B变量", "SHARED_SOURCE_MARKER" in target, False)
            state["配置有效页"] = {
                "被邀请组B": f"{args.base.rstrip('/')}/org/{group('b')['compatibility_name']}/settings/actions/variables",
                "消费者仓68": f"{args.base.rstrip('/')}/{state['仓库']['project']['full_name']}/settings/actions/variables",
            }
            save()
        elif args.stage == "group_reduce":
            group_share("c", 30, remove=True)
            abilities("撤Developer保Reporter", "g", True, False, False)
            git("仅Reporter群组读", "group", names["user"])
            git("仅Reporter群组拒推", "group", names["user"], writable=True, allowed=False)
            generic("仅Reporter包读", "GET", names["user"], "1.0.0", 200,
                    digest=state["包内容SHA256"])
            generic("仅Reporter包拒写", "PUT", names["user"], "3.0.0", DENIED,
                    payload=b"denied\n")
            git("兄弟私有仓仍拒读", "sibling", names["user"], allowed=False)
        elif args.stage == "group_expire":
            expires = int(time.time()) + 12
            group_share("b", 20, expires)
            state["群组共享到期Unix"] = expires
            save()
            git("到期前Reporter仍读", "group", names["user"])
            time.sleep(max(0, expires + 2 - time.time()))
            abilities("真实到期后无群组访问", "g", False, False, False)
            git("真实到期后群组Git拒读", "group", names["user"], allowed=False)
            generic("真实到期后包拒读", "GET", names["user"], "1.0.0", DENIED)
        elif args.stage == "group_config_effective":
            group_share("b", 20)
            abilities("配置有效页期间Reporter共享", "g", True, False, False)
            marker = "GROUP_PHYSICAL_MARKER"
            api("在群组共享源G配置物理祖先变量",
                f"/orgs/{group('g')['compatibility_name']}/actions/variables/{marker}", "POST",
                {"value": "仅G物理祖先-" + names["g"], "description": "群组共享配置边界"}, expected=201)
            invited = web("群组共享被邀请B变量页", f"/org/{group('b')['compatibility_name']}/settings/actions/variables")
            check("B自身变量仍存在", "SHARED_SOURCE_MARKER" in invited, True)
            target = web("群组共享消费者仓67变量页", "/" + state["仓库"]["group"]["full_name"] + "/settings/actions/variables")
            check("消费者仓67显示物理祖先G变量", marker in target, True)
            check("消费者仓67排除共享方B变量", "SHARED_SOURCE_MARKER" in target, False)
            git("配置有效页期间共享Git读", "group", names["user"])
            state["群组配置有效页"] = {
                "被邀请B": f"{args.base.rstrip('/')}/org/{group('b')['compatibility_name']}/settings/actions/variables",
                "消费者仓67": f"{args.base.rstrip('/')}/{state['仓库']['group']['full_name']}/settings/actions/variables",
            }
            save()
        elif args.stage == "group_config_revoke":
            group_share("b", 20, remove=True)
            abilities("配置正负对照后撤共享", "g", False, False, False)
            git("配置对照后群组Git拒读", "group", names["user"], allowed=False)
            generic("配置对照后包拒读", "GET", names["user"], "1.0.0", DENIED)
        elif args.stage == "repo_overlap":
            repo = state["仓库"]["project"]
            members = api("读取项目成员修订", f"/governance/repositories/{repo['id']}/members")
            api("增加独立直接Developer", f"/governance/repositories/{repo['id']}/members/{state['用户ID']['user']}",
                "PUT", {"role": 30, "revision": members["revision"]}, expected=204)
            team = api("建立专属Reporter Team", "/orgs/" + group("p")["compatibility_name"] + "/teams", "POST",
                {"name": "source-read", "permission": "read", "units_map": {"repo.code": "read"}}, expected=201)
            state["Team"] = {"id": team["id"], "name": team["name"]}
            save()
            api("Team仅绑定目标项目", f"/teams/{team['id']}/repos/{repo['full_name']}", "PUT", expected=204)
            api("Team加入专属普通账号", f"/teams/{team['id']}/members/{names['user']}", "PUT", expected=204)
            source_view("共享+直接+Team", ["direct", "shared", "native_team"])
            git("三来源项目读", "project", names["user"])
            git("三来源项目推送", "project", names["user"], writable=True)
            git("三来源兄弟仓拒读", "sibling", names["user"], allowed=False)
        elif args.stage == "repo_source_current":
            source_view("共享+直接+Team", ["direct", "shared", "native_team"])
        elif args.stage == "repo_remove_direct":
            repo = state["仓库"]["project"]
            members = api("撤前项目成员修订", f"/governance/repositories/{repo['id']}/members")
            api("只撤直接Developer", f"/governance/repositories/{repo['id']}/members/{state['用户ID']['user']}?revision={members['revision']}",
                "DELETE", expected=204)
            source_view("撤直接后共享+Team", ["shared", "native_team"])
            git("撤直接后项目读", "project", names["user"])
            git("撤直接后项目拒推", "project", names["user"], writable=True, allowed=False)
            git("撤直接后兄弟仓仍拒读", "sibling", names["user"], allowed=False)
        elif args.stage == "repo_remove_team":
            api("只撤Team成员", f"/teams/{state['Team']['id']}/members/{names['user']}", "DELETE", expected=204)
            source_view("撤Team后仅共享", ["shared"])
            git("撤Team后项目读", "project", names["user"])
            git("撤Team后项目拒推", "project", names["user"], writable=True, allowed=False)
            git("撤两项后兄弟仓仍拒读", "sibling", names["user"], allowed=False)
        elif args.stage == "repo_expire":
            expires = int(time.time()) + 12
            repo_share(20, expires)
            state["项目共享到期Unix"] = expires
            save()
            git("项目共享到期前仍读", "project", names["user"])
            time.sleep(max(0, expires + 2 - time.time()))
            source_view("项目共享真实到期", [])
            git("项目共享到期后拒读", "project", names["user"], allowed=False)
            git("兄弟私有仓到期后仍拒读", "sibling", names["user"], allowed=False)
            code, _ = client.call("/api/v1/repos/" + state["仓库"]["project"]["full_name"], user=names["user"])
            check("项目共享到期后正式API隐藏", code, 404)
        elif args.stage == "artifact_prepare":
            repo = state["仓库"]["project"]
            repo_share(20)
            members = api("附件组合前项目成员修订", f"/governance/repositories/{repo['id']}/members")
            api("附件组合增加直接Developer", f"/governance/repositories/{repo['id']}/members/{state['用户ID']['user']}",
                "PUT", {"role": 30, "revision": members["revision"]}, expected=204)
            team = api("核对原生Team单元", f"/teams/{state['Team']['id']}")
            check("Team仅repo.code单元read", team["units_map"], {"repo.code": "read"})
            api("附件组合恢复Team成员", f"/teams/{state['Team']['id']}/members/{names['user']}",
                "PUT", expected=204)
            release = api("创建同仓独立Release", f"/repos/{repo['full_name']}/releases", "POST",
                {"tag_name": "v1-sources-" + names["p"].split("-")[-1], "name": "一期多来源附件验收",
                 "body": "独立夹具", "target_commitish": "main"}, expected=201)
            payload = ("一期项目多来源附件 " + names["p"] + "\n").encode()
            path = f"/api/v1/repos/{repo['full_name']}/releases/{release['id']}/assets?name=source-proof.txt"
            request = urllib.request.Request(args.base.rstrip("/") + path, method="POST", data=payload,
                headers={"Authorization": basic(args.owner), "Content-Type": "application/octet-stream"})
            with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request, timeout=25) as response:
                check("上传同仓Release附件HTTP", response.status, 201)
                uploaded = json.loads(response.read())
            state["项目附件"] = {"ReleaseID": release["id"], "附件ID": uploaded["id"],
                "下载URL": uploaded["browser_download_url"], "内容SHA256": hashlib.sha256(payload).hexdigest()}
            save()
            source_view("附件三来源", ["direct", "shared", "native_team"])
            git("附件三来源项目读", "project", names["user"])
            git("附件三来源项目推", "project", names["user"], writable=True)
            asset("附件三来源下载", names["user"], 200)
            asset("附件初始无关账号拒读", names["out"], DENIED)
            asset("附件Owner基线下载", args.owner, 200)
        elif args.stage == "artifact_remove_direct":
            repo = state["仓库"]["project"]
            members = api("附件撤直接前成员修订", f"/governance/repositories/{repo['id']}/members")
            api("附件仅撤直接Developer", f"/governance/repositories/{repo['id']}/members/{state['用户ID']['user']}?revision={members['revision']}",
                "DELETE", expected=204)
            source_view("附件撤直接后共享+Team", ["shared", "native_team"])
            git("附件撤直接后项目读", "project", names["user"])
            git("附件撤直接后项目拒推", "project", names["user"], writable=True, allowed=False)
            asset("附件撤直接后仍可下载", names["user"], 200)
        elif args.stage == "artifact_remove_team":
            api("附件仅撤Team成员", f"/teams/{state['Team']['id']}/members/{names['user']}",
                "DELETE", expected=204)
            source_view("附件撤Team后仅共享", ["shared"])
            git("附件撤Team后项目读", "project", names["user"])
            git("附件撤Team后项目拒推", "project", names["user"], writable=True, allowed=False)
            asset("附件撤Team后仍可下载", names["user"], 200)
        elif args.stage == "artifact_remove_share":
            repo_share(20, remove=True)
            source_view("附件撤最后共享后无来源", [])
            git("附件撤最后来源后项目拒读", "project", names["user"], allowed=False)
            asset("附件撤最后来源后拒下载", names["user"], DENIED)
            asset("附件撤最后来源后Owner内容未变", args.owner, 200)
        state["阶段"].append(args.stage)
        state["结果"] = "阶段通过"
        save()
        print("阶段通过：", args.stage, "；UI入口：", state.get("UI入口", {}))
    except Exception as error:
        state["结果"] = "失败"
        state["首错"] = {"阶段": args.stage, "原因": str(error)}
        save()
        raise


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("停止：" + str(error), file=sys.stderr)
        sys.exit(1)
