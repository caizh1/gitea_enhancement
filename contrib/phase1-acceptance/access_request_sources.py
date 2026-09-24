#!/usr/bin/env python3
"""在独立夹具验收访问申请、私有改名、授权到期和共享来源。"""

import argparse
import base64
import hashlib
import json
import os
import pathlib
import subprocess
import time
import urllib.error
import urllib.request

from job_token_boundary import Client


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("stage", choices=("prepare", "withdraw", "private_rename", "restore_request", "approve_overlap", "expire_direct", "revoke_share"))
    parser.add_argument("--base", required=True)
    parser.add_argument("--credentials", type=pathlib.Path, required=True)
    parser.add_argument("--state", type=pathlib.Path, required=True)
    parser.add_argument("--owner", required=True)
    parser.add_argument("--subject", required=True)
    parser.add_argument("--outsider", required=True)
    parser.add_argument("--binary", type=pathlib.Path, required=True)
    parser.add_argument("--ttl", type=int, default=90)
    args = parser.parse_args()
    if args.base.rstrip("/") not in ("http://127.0.0.1:13043", "http://localhost:13043"):
        raise RuntimeError("仅允许本机隔离实例13043")
    if args.ttl < 25:
        raise RuntimeError("到期观察需要至少25秒窗口")
    if args.stage == "prepare":
        if args.state.exists():
            raise RuntimeError("独立结果已存在，不覆盖")
        suffix = format(time.time_ns(), "x")[-10:]
        state = {"说明": "M07独立访问申请来源验收", "阶段": [], "步骤": [],
                 "名称": {"目标": "p1m07-target-" + suffix, "受邀": "p1m07-invited-" + suffix},
                 "身份": {"Owner": args.owner, "申请人": args.subject, "无关": args.outsider},
                 "运行实例": args.base.rstrip("/"),
                 "二进制SHA256": hashlib.sha256(args.binary.read_bytes()).hexdigest()}
    else:
        state = json.loads(args.state.read_text())
        if args.stage in state["阶段"]:
            raise RuntimeError("阶段已完成，不重复执行")
        if state["身份"] != {"Owner": args.owner, "申请人": args.subject, "无关": args.outsider}:
            raise RuntimeError("身份与已建夹具不一致")
    args.state.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    client = Client(args.base, args.credentials, args.owner)

    def save():
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")
        args.state.chmod(0o600)

    def check(label, actual, expected):
        passed = actual in expected if isinstance(expected, set) else actual == expected
        state["步骤"].append({"阶段": args.stage, "检查": label, "实际": actual,
                          "期望": sorted(expected) if isinstance(expected, set) else expected, "通过": passed})
        save()
        if not passed:
            raise AssertionError(f"{label}：实际{actual}，期望{expected}")

    def api(label, path, method="GET", body=None, user=None, expected=200):
        code, result = client.call("/api/v1" + path, method, body, user)
        check(label + " HTTP", code, expected)
        return result

    def group(kind, user=None):
        return api("读取群组" + kind, "/governance/groups/" + str(state["群组"][kind]["id"]), user=user)

    def requests(user=None):
        return api("读取申请状态", "/governance/groups/" + str(state["群组"]["目标"]["id"]) + "/access-requests", user=user)

    def own_request(request_id):
        items = api("读取本人申请快照", "/governance/access-requests", user=args.subject)
        return next((item for item in items if item["id"] == request_id), None)

    def git(label, path, user, allowed):
        password = client.credentials.get(user)
        if not isinstance(password, str):
            raise RuntimeError("私有凭据缺少账号")
        authorization = base64.b64encode((user + ":" + password).encode()).decode()
        env = os.environ.copy()
        env.update(GIT_TERMINAL_PROMPT="0", GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL="/dev/null",
                   GIT_CONFIG_COUNT="2", GIT_CONFIG_KEY_0="credential.helper", GIT_CONFIG_VALUE_0="",
                   GIT_CONFIG_KEY_1="http.extraHeader", GIT_CONFIG_VALUE_1="Authorization: Basic " + authorization)
        completed = subprocess.run(["git", "ls-remote", args.base.rstrip("/") + "/" + path + ".git"],
                                   env=env, capture_output=True, timeout=35)
        check(label + " Git可读", completed.returncode == 0 and bool(completed.stdout.strip()), allowed)
        state.setdefault("Git退出码", {})[label] = completed.returncode
        save()

    def web_status(path):
        request = urllib.request.Request(args.base.rstrip("/") + path)
        try:
            with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request, timeout=25) as response:
                return response.status, response.read().decode(errors="replace")
        except urllib.error.HTTPError as error:
            content = error.read().decode(errors="replace")
            code = error.code
            error.close()
            return code, content

    try:
        if args.stage == "prepare":
            owner = api("普通Owner身份", "/user")
            check("Owner非实例管理员", owner["is_admin"], False)
            for label, name in (("申请人", args.subject), ("无关", args.outsider)):
                identity = api(label + "身份", "/user", user=name)
                check(label + "非实例管理员", identity["is_admin"], False)
            state["群组"] = {}
            for kind, visibility in (("目标", 1), ("受邀", 2)):
                created = api("创建独立" + kind + "组", "/governance/groups", "POST",
                              {"path": state["名称"][kind], "name": "一期访问申请" + kind + "验收",
                               "parent_id": 0, "visibility": visibility}, expected=201)
                state["群组"][kind] = {key: created[key] for key in ("id", "full_path", "compatibility_name")}
                save()
            invited = group("受邀")
            subject = api("读取申请人身份ID", "/user", user=args.subject)
            state["申请人ID"] = subject["id"]
            api("申请人加入独立受邀组", f"/governance/groups/{invited['id']}/members/{subject['id']}",
                "PUT", {"role": 20, "revision": invited["revision"]}, expected=204)
            target = group("目标")
            repo = api("创建目标组私有仓", "/orgs/" + target["compatibility_name"] + "/repos", "POST",
                       {"name": "request-private", "private": True, "auto_init": True}, expected=201)
            state["仓库"] = {"id": repo["id"], "name": repo["name"], "初始全路径": repo["full_name"]}
            state["旧路径"] = target["full_path"]
            state["UI入口"] = {
                "目标组": args.base.rstrip("/") + "/" + target["full_path"],
                "目标组申请": args.base.rstrip("/") + "/governance/groups/" + str(target["id"]) + "/access-requests",
                "申请人列表": args.base.rstrip("/") + "/governance/access-requests",
            }
            save()
            view = requests(args.subject)
            check("内部组非成员允许申请", view["can_request"], True)
            check("内部组申请人无有效来源", len(group("目标", args.subject)["grants"]), 0)
            git("内部组未获仓库读权", state["旧路径"] + "/request-private", args.subject, False)
        elif args.stage == "withdraw":
            target = group("目标")
            path = f"/governance/groups/{target['id']}/access-requests"
            created = api("内部组提交第一份申请", path, "POST", user=args.subject, expected=201)
            check("第一份申请路径快照", created["scope_path"], state["旧路径"])
            check("本人能找到第一份申请", own_request(created["id"])["id"], created["id"])
            api("本人撤回第一份申请", "/governance/access-requests/" + str(created["id"]), "DELETE",
                user=args.subject, expected=204)
            check("撤回后本人申请消失", own_request(created["id"]), None)
            created = api("内部组再提交待私有化申请", path, "POST", user=args.subject, expected=201)
            state["私有化申请ID"] = created["id"]
            save()
            git("提交申请未自动授权", state["旧路径"] + "/request-private", args.subject, False)
        elif args.stage == "private_rename":
            target = group("目标")
            anonymous_code, anonymous_body = web_status(f"/governance/groups/{target['id']}")
            check("内部组匿名治理页可读取有限信息", anonymous_code, 200)
            check("匿名无访问申请入口文字", "访问申请" in anonymous_body, False)
            check("匿名无访问申请链接", "access-requests" in anonymous_body, False)
            api("目标组切换私有", "/orgs/" + target["compatibility_name"], "PATCH",
                {"visibility": "private"})
            private = group("目标")
            check("私有可见性", private["visibility"], 2)
            check("申请人不能读取私有组", client.call("/api/v1/governance/groups/" + str(target["id"]), user=args.subject)[0], 404)
            new_path = state["旧路径"] + "-private"
            moved = api("私有组正式改名", f"/governance/groups/{target['id']}/move", "POST",
                        {"path": new_path, "parent_id": 0, "revision": private["revision"]})
            state["新路径"] = moved["full_path"]
            save()
            snapshot = own_request(state["私有化申请ID"])
            check("改名后申请人仍见申请时旧路径", snapshot["scope_path"], state["旧路径"])
            check("改名后申请快照不含新路径", state["新路径"] in json.dumps(snapshot), False)
            check("私有组禁止新申请", client.call(f"/api/v1/governance/groups/{target['id']}/access-requests",
                                            "POST", user=args.outsider)[0], 404)
            for path in (state["旧路径"], state["新路径"]):
                code, body = web_status("/" + path)
                check("匿名私有路径HTTP " + path, code, 404)
                check("匿名私有页不泄漏新路径 " + path, state["新路径"] in body, False)
                git("私有旧新路径未授权 " + path, path + "/request-private", args.subject, False)
            api("私有改名后凭稳定ID撤回", "/governance/access-requests/" + str(state["私有化申请ID"]),
                "DELETE", user=args.subject, expected=204)
            check("私有撤回后本人申请消失", own_request(state["私有化申请ID"]), None)
        elif args.stage == "restore_request":
            target = group("目标")
            api("恢复内部可见性", "/orgs/" + target["compatibility_name"], "PATCH",
                {"visibility": "limited"})
            check("恢复内部可见性值", group("目标")["visibility"], 1)
            view = requests(args.subject)
            check("恢复后允许申请", view["can_request"], True)
            created = api("恢复后提交新路径申请", f"/governance/groups/{target['id']}/access-requests",
                          "POST", user=args.subject, expected=201)
            check("恢复后申请使用当前路径", created["scope_path"], state["新路径"])
            state["到期申请ID"] = created["id"]
            save()
        elif args.stage == "approve_overlap":
            target = group("目标")
            state["直接授权到期Unix"] = int(time.time()) + args.ttl
            save()
            api("普通Owner批准短期Reporter", f"/governance/groups/{target['id']}/access-requests/{state['到期申请ID']}/approve",
                "POST", {"role": 20, "expires_unix": state["直接授权到期Unix"],
                         "revision": requests()["revision"]}, expected=204)
            check("审批后待处理申请消失", own_request(state["到期申请ID"]), None)
            git("批准后新路径可读", state["新路径"] + "/request-private", args.subject, True)
            git("批准后旧别名可读", state["旧路径"] + "/request-private", args.subject, True)
            current = group("目标")
            invited = group("受邀")
            api("增加独立受邀组共享Reporter", f"/governance/groups/{target['id']}/shares/{invited['id']}",
                "PUT", {"max_role": 20, "expires_unix": 0, "revision": current["revision"]}, expected=204)
            grants = group("目标", args.subject)["grants"]
            state["双来源快照"] = [{key: value.get(key) for key in ("source", "scope_type", "scope_id", "role", "expires_unix")}
                                     for value in grants]
            save()
            check("审批与共享两种有效来源", len(grants), 2)
            check("共享已可访问时禁止重复申请", client.call(f"/api/v1/governance/groups/{target['id']}/access-requests",
                                                "POST", user=args.subject)[0], 409)
            git("双来源期间仍可读", state["新路径"] + "/request-private", args.subject, True)
        elif args.stage == "expire_direct":
            before = state["直接授权到期Unix"] - int(time.time())
            if before > 0:
                time.sleep(before + 2)
            check("已跨真实墙钟到期秒", int(time.time()) > state["直接授权到期Unix"], True)
            grants = group("目标", args.subject)["grants"]
            state["到期后来源快照"] = [{key: value.get(key) for key in ("source", "scope_type", "scope_id", "role", "expires_unix")}
                                     for value in grants]
            save()
            check("直接授权到期后只剩共享", len(grants), 1)
            git("直接授权到期但共享仍可读", state["新路径"] + "/request-private", args.subject, True)
            check("共享成员仍不能新申请", client.call(f"/api/v1/governance/groups/{state['群组']['目标']['id']}/access-requests",
                                              "POST", user=args.subject)[0], 409)
        elif args.stage == "revoke_share":
            target = group("目标")
            invited = group("受邀")
            api("撤销最后共享来源", f"/governance/groups/{target['id']}/shares/{invited['id']}?revision={target['revision']}",
                "DELETE", expected=204)
            check("撤共享后无有效来源", len(group("目标", args.subject)["grants"]), 0)
            git("撤最后来源新路径拒读", state["新路径"] + "/request-private", args.subject, False)
            git("撤最后来源旧别名拒读", state["旧路径"] + "/request-private", args.subject, False)
            git("Owner原仓内容保留", state["新路径"] + "/request-private", args.owner, True)
            check("恢复内部后可再次申请", requests(args.subject)["can_request"], True)
    except Exception as error:
        state["首错"] = {"阶段": args.stage, "类型": type(error).__name__, "说明": str(error)}
        save()
        raise
    state["阶段"].append(args.stage)
    save()
    print(json.dumps({"阶段": args.stage, "通过断言": sum(item["通过"] for item in state["步骤"]),
                      "失败断言": sum(not item["通过"] for item in state["步骤"]),
                      "群组": state.get("群组"), "UI入口": state.get("UI入口")}, ensure_ascii=False))


if __name__ == "__main__":
    main()
