#!/usr/bin/env python3
"""在隔离实例固定两项 Team 撤权请求起点，并核对独立治理来源。"""

import argparse
import base64
import hashlib
import json
import os
import pathlib
import subprocess
import threading
import time

from job_token_boundary import Client


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("stage", choices=("prepare_unique", "revoke_unique", "prepare_direct", "revoke_direct"))
    parser.add_argument("--base", required=True)
    parser.add_argument("--credentials", type=pathlib.Path, required=True)
    parser.add_argument("--fixture", type=pathlib.Path, required=True)
    parser.add_argument("--state", type=pathlib.Path, required=True)
    parser.add_argument("--owner", required=True)
    parser.add_argument("--binary", type=pathlib.Path, required=True)
    args = parser.parse_args()
    if args.base.rstrip("/") not in ("http://127.0.0.1:13043", "http://localhost:13043"):
        raise RuntimeError("只允许本机隔离实例13043")
    fixture = json.loads(args.fixture.read_text())
    repo = fixture["仓库"]["project"]
    team_id = fixture["Team"]["id"]
    subject = fixture["名称"]["user"]
    subject_id = fixture["用户ID"]["user"]
    client = Client(args.base, args.credentials, args.owner)
    if args.stage == "prepare_unique":
        if args.state.exists():
            raise RuntimeError("结果文件已存在，不覆盖旧证据")
        state = {"说明": "T02-a 同一独立项目的 Team 成员与仓库关联并发撤销",
                 "运行二进制SHA256": hashlib.sha256(args.binary.read_bytes()).hexdigest(),
                 "对象": {"仓库": repo, "TeamID": team_id, "普通Owner": args.owner,
                         "被测账号": subject, "被测账号ID": subject_id},
                 "阶段": [], "断言": [], "并发请求": []}
    else:
        state = json.loads(args.state.read_text())
        if args.stage in state["阶段"]:
            raise RuntimeError("阶段已经完成，不重复执行")

    def save():
        args.state.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")
        args.state.chmod(0o600)

    def check(label, actual, expected):
        passed = actual in expected if isinstance(expected, set) else actual == expected
        state["断言"].append({"阶段": args.stage, "检查": label, "实际": actual,
                            "期望": sorted(expected) if isinstance(expected, set) else expected, "通过": passed})
        save()
        if not passed:
            raise AssertionError(f"{label}：实际 {actual}，期望 {expected}")

    def request(label, path, method="GET", body=None, user=None, expected=200):
        code, value = client.call("/api/v1" + path, method, body, user)
        check(label + " HTTP", code, expected)
        return value

    def sources(label, expected):
        view = request(label + " 来源页", f"/governance/repositories/{repo['id']}/members/view?q={subject}")
        members = [item for item in view["members"] if item["username"] == subject]
        check(label + " 有效账号数", len(members), 1 if expected else 0)
        kinds = sorted(item["kind"] for item in members[0]["sources"]) if members else []
        check(label + " 来源种类", kinds, sorted(expected))
        state.setdefault("来源快照", {})[label] = kinds
        save()

    def git(label, allowed):
        credentials = json.loads(args.credentials.read_text())
        password = credentials[subject]
        auth = base64.b64encode((subject + ":" + password).encode()).decode()
        env = os.environ.copy()
        env.update(GIT_TERMINAL_PROMPT="0", GIT_CONFIG_COUNT="2", GIT_CONFIG_KEY_0="http.extraHeader",
                   GIT_CONFIG_VALUE_0="Authorization: Basic " + auth,
                   GIT_CONFIG_KEY_1="credential.helper", GIT_CONFIG_VALUE_1="")
        result = subprocess.run(["git", "ls-remote", args.base.rstrip("/") + "/" + repo["full_name"] + ".git"],
                                env=env, capture_output=True, timeout=35)
        state.setdefault("Git协议", []).append({"阶段": args.stage, "检查": label,
            "退出码": result.returncode, "标准输出SHA256": hashlib.sha256(result.stdout).hexdigest()})
        save()
        check(label + " Git成功", result.returncode == 0, allowed)
        if allowed:
            check(label + " 引用非空", bool(result.stdout), True)

    def issue(label, allowed):
        code, _ = client.call("/api/v1/repos/" + repo["full_name"] + "/issues/" + str(state["Issue编号"]),
                              user=subject)
        check(label + " Issue HTTP", code, 200 if allowed else 404)

    member_path = f"/teams/{team_id}/members/{subject}"
    team_repo_path = f"/teams/{team_id}/repos/{repo['full_name']}"

    def relations(label, exists):
        request(label + " Team成员", member_path, expected=200 if exists else 404)
        request(label + " Team仓库", team_repo_path, expected=200 if exists else 404)

    def concurrent_remove(label):
        barrier = threading.Barrier(3, timeout=20)
        records = {}

        def worker(kind, path):
            own_client = Client(args.base, args.credentials, args.owner)
            barrier.wait()
            started = time.time_ns()
            try:
                code, _ = own_client.call("/api/v1" + path, "DELETE")
                records[kind] = {"HTTP": code, "请求发起Unix纳秒": started, "结束Unix纳秒": time.time_ns()}
            except Exception as error:
                records[kind] = {"异常类型": type(error).__name__, "请求发起Unix纳秒": started,
                                 "结束Unix纳秒": time.time_ns()}

        threads = [threading.Thread(target=worker, args=("移除Team成员", member_path)),
                   threading.Thread(target=worker, args=("解除Team仓库授权", team_repo_path))]
        for thread in threads:
            thread.start()
        barrier.wait()
        for thread in threads:
            thread.join(timeout=40)
        check(label + " 两请求结束", all(not thread.is_alive() for thread in threads), True)
        state["并发请求"].append({"阶段": args.stage, "屏障": "两个正式API请求在客户端同一Barrier释放后发起；不代表事务内部固定交错",
                                 "结果": records})
        save()
        check(label + " 移成员返回", records.get("移除Team成员", {}).get("HTTP"), 204)
        check(label + " 解仓授权返回", records.get("解除Team仓库授权", {}).get("HTTP"), 204)

    if args.stage == "prepare_unique":
        owner = request("Owner正式身份", "/user")
        check("Owner非实例管理员", owner["is_admin"], False)
        org = request("P79正式组织配置", "/orgs/" + fixture["群组"]["p"]["compatibility_name"])
        check("仓库管理员无Team授权委托", org["repo_admin_change_team_access"], False)
        sources("准备前无治理或共享来源", [])
        git("准备前私有Git拒读", False)
        created = request("Owner建独立Issue", "/repos/" + repo["full_name"] + "/issues", "POST",
                          {"title": "一期Team并发撤权 " + subject, "body": "独立验收对象"}, expected=201)
        state["Issue编号"] = created["number"]
        save()
        issue("准备前Issue拒读", False)
        team = request("更新Team精确Code与Issue只读单元", f"/teams/{team_id}", "PATCH",
                       {"units_map": {"repo.code": "read", "repo.issues": "read"}})
        check("Team精确只读单元", team["units_map"], {"repo.code": "read", "repo.issues": "read"})
        request("加入Team唯一来源", member_path, "PUT", expected=204)
        relations("唯一Team来源", True)
        sources("唯一Team来源", ["native_team"])
        git("唯一Team来源Git读", True)
        issue("唯一Team来源Issue读", True)
    elif args.stage == "revoke_unique":
        relations("并发前唯一Team来源", True)
        concurrent_remove("唯一来源并发撤权")
        relations("并发后唯一Team来源", False)
        sources("唯一Team来源全撤", [])
        git("唯一来源全撤Git下一请求", False)
        issue("唯一来源全撤Issue下一请求", False)
    elif args.stage == "prepare_direct":
        request("重加Team仓库授权", team_repo_path, "PUT", expected=204)
        request("重加Team成员", member_path, "PUT", expected=204)
        members = request("读取治理直接成员修订", f"/governance/repositories/{repo['id']}/members")
        request("增加独立直接Reporter", f"/governance/repositories/{repo['id']}/members/{subject_id}",
                "PUT", {"role": 20, "revision": members["revision"]}, expected=204)
        relations("直接加Team并存", True)
        sources("直接Reporter与Team并存", ["direct", "native_team"])
        git("直接加Team并存Git读", True)
        issue("直接加Team并存Issue读", True)
    elif args.stage == "revoke_direct":
        relations("并发前直接加Team", True)
        concurrent_remove("保留治理直接来源并发撤权")
        relations("并发后仅治理直接来源", False)
        sources("仅直接Reporter保留", ["direct"])
        git("直接Reporter仍可Git读", True)
        issue("直接Reporter仍可Issue读", True)
        repo_view = request("直接Reporter仓库正式读取", "/repos/" + repo["full_name"], user=subject)
        check("直接Reporter读取同仓", repo_view["id"], repo["id"])
    state["阶段"].append(args.stage)
    save()
    print("阶段通过：", args.stage, "；Issue：", state["Issue编号"])


if __name__ == "__main__":
    main()
