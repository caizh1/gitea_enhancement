#!/usr/bin/env python3
"""在指定隔离实例复跑受保护凭据的 push 与 fork PR 边界。"""

import argparse
import base64
import hashlib
import json
import os
import pathlib
import re
import secrets
import time
import urllib.error
import urllib.parse
import urllib.request


SECRET_NAME = "CI04_PROTECTED_PROBE"
WORKFLOW_PATH = ".gitea/workflows/ci04-probe.yml"
BRANCHES = {"受保护": "ci04-protected", "未保护": "ci04-unprotected"}
WORKFLOW = """name: 一期受保护凭据边界
on:
  push:
    branches: [ci04-protected, ci04-unprotected]
  pull_request:
jobs:
  check:
    runs-on: phase1-v15
    steps:
      - name: 仅核对凭据是否存在
        env:
          CI04_PROTECTED_PROBE: ${{ secrets.CI04_PROTECTED_PROBE }}
        run: |
          if [ -n "$CI04_PROTECTED_PROBE" ]; then observed=true; else observed=false; fi
          if [ "$GITHUB_EVENT_NAME" = push ] && [ "$GITHUB_REF" = refs/heads/ci04-protected ]; then expected=true; else expected=false; fi
          echo "CI04_PROBE event=$GITHUB_EVENT_NAME ref=$GITHUB_REF secret_present=$observed"
          test "$observed" = "$expected"
"""


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("prepare", "fork", "verify"))
    parser.add_argument("--base", required=True, help="已授权隔离实例地址")
    parser.add_argument("--isolated-instance", action="store_true", required=True)
    parser.add_argument("--credentials", required=True, type=pathlib.Path, help="仅本机私有 JSON")
    parser.add_argument("--state", required=True, type=pathlib.Path, help="脱敏原始核对记录")
    parser.add_argument("--org", required=True, help="新仓库所属组织兼容名")
    parser.add_argument("--owner", required=True)
    parser.add_argument("--fork-user", required=True, help="另一名非管理员的账号")
    parser.add_argument("--repo", default="phase1-ci04-probe-" + str(int(time.time())))
    parser.add_argument("--runner-label", default="phase1-v15")
    parser.add_argument("--source", required=True, help="服务端源码提交")
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9_.:-]+", args.runner_label):
        parser.error("Runner 标签只能含英文字母、数字、下划线、点、冒号或连字符")
    base = args.base.rstrip("/")
    if urllib.parse.urlsplit(base).scheme not in ("http", "https"):
        parser.error("实例地址必须为 HTTP(S)")
    credentials = json.loads(args.credentials.read_text())
    script_hash = hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest()
    state = json.loads(args.state.read_text()) if args.state.exists() else {
        "实例": base, "组织": args.org, "仓库": args.repo, "源码": args.source,
        "凭据名称": SECRET_NAME, "Runner标签": args.runner_label,
        "脚本SHA256": script_hash, "结果": [],
    }
    if state["实例"] != base or state["组织"] != args.org or state["源码"] != args.source:
        parser.error("现有样本属于另一实例、组织或源码")
    if state.get("Runner标签") != args.runner_label or state.get("脚本SHA256") != script_hash:
        parser.error("Runner 标签或脚本内容与现有样本不一致")
    api = "/api/v1/repos/" + args.org + "/" + state["仓库"]
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def save():
        args.state.parent.mkdir(parents=True, exist_ok=True)
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")

    def request(path, method="GET", body=None, user=None):
        headers = {"Accept": "application/json", "Content-Type": "application/json"}
        if user:
            password = credentials.get(user, credentials.get("password"))
            if not isinstance(password, str):
                raise ValueError("私有凭据文件缺少账号密码")
            auth = base64.b64encode((user + ":" + password).encode()).decode()
            headers["Authorization"] = "Basic " + auth
        payload = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(base + path, method=method, data=payload, headers=headers)
        try:
            with opener.open(req, timeout=30) as response:
                return response.status, response.read()
        except urllib.error.HTTPError as error:
            return error.code, error.read()

    def checked(path, method="GET", body=None, user=args.owner, codes=(200,)):
        code, raw = request(path, method, body, user)
        if code not in codes:
            raise AssertionError(f"{method} {path} 返回 {code}，预期 {codes}；未继续修改")
        return json.loads(raw) if raw else None

    def record(label, observed, expected):
        ok = observed == expected
        state["结果"].append({"时间": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                             "检查": label, "观察": observed, "预期": expected, "通过": ok})
        save()
        print(("通过：" if ok else "失败：") + label, flush=True)
        if not ok:
            raise AssertionError(label)

    def add_file(path, content, branch, user=args.owner):
        result = checked(api + "/contents/" + path, "POST", {
            "content": base64.b64encode(content.encode()).decode(),
            "branch": branch, "message": "test(actions): 添加一期独立凭据探针",
        }, user=user, codes=(201,))
        return result["commit"]["sha"]

    def source_check():
        repo = checked(api)
        record("来源仓库仍归属独立组织", repo["owner"]["username"], args.org)
        rules = checked(api + "/branch_protections")
        names = [rule.get("rule_name") or rule.get("branch_name") for rule in rules]
        record("来源精确受保护分支规则保留", "ci04-protected" in names, True)
        secrets_list = checked(api + "/actions/secrets")
        # 只读取元数据；API 不返回 Secret 明文。
        entries = secrets_list.get("secrets", secrets_list) if isinstance(secrets_list, dict) else secrets_list
        matching = [item for item in entries if item.get("name") == SECRET_NAME]
        record("来源凭据元数据受保护", len(matching) == 1 and matching[0].get("protected") is True, True)

    if args.mode == "prepare":
        if "仓库ID" in state:
            parser.error("仓库已建立；改用 fork 或 verify，不重复创建")
        for user in (args.owner, args.fork_user):
            me = checked("/api/v1/user", user=user)
            record(user + " 非实例管理员", me["is_admin"], False)
        repo = checked("/api/v1/orgs/" + args.org + "/repos", "POST", {
            "name": state["仓库"], "private": True, "auto_init": True,
            "default_branch": "main", "description": "一期 CI04 独立受保护凭据探针",
        }, codes=(201,))
        state.update({"仓库ID": repo["id"], "页面": repo["html_url"]})
        save()
        secret_value = "一期合成凭据-" + secrets.token_hex(20)
        secret_file = args.state.with_suffix(".secret")
        descriptor = os.open(secret_file, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "w") as private:
            private.write(secret_value)
        checked(api + "/actions/secrets/" + SECRET_NAME, "PUT", {
            "data": secret_value, "description": "一期合成凭据，仅用于存在性测试",
            "protected": True,
        }, codes=(201,))
        checked(api + "/branch_protections", "POST", {
            "rule_name": "ci04-protected", "enable_push": True,
            "enable_force_push": False,
        }, codes=(201,))
        source_check()
        for branch in BRANCHES.values():
            checked(api + "/branches", "POST", {
                "new_branch_name": branch, "old_ref_name": "main",
            }, codes=(201,))
        workflow = WORKFLOW.replace("runs-on: phase1-v15", "runs-on: " + args.runner_label)
        state["工作流提交"] = add_file(WORKFLOW_PATH, workflow, "main")
        save()
        for label, branch in BRANCHES.items():
            sha = add_file(WORKFLOW_PATH, workflow, branch)
            state.setdefault("分支提交", {})[label] = sha
            save()
        print("独立来源已创建；执行 verify 等待两个 push 作业", flush=True)

    elif args.mode == "fork":
        if "仓库ID" not in state or "fork仓库" in state:
            parser.error("先 prepare，且同一 state 只能创建一次 fork")
        source_check()
        fork = checked(api + "/forks", "POST", {"name": state["仓库"]},
                       user=args.fork_user, codes=(202, 201))
        state["fork仓库"] = fork["full_name"]
        state["fork仓库ID"] = fork["id"]
        save()
        fork_api = "/api/v1/repos/" + fork["full_name"]
        checked(fork_api + "/branches", "POST", {
            "new_branch_name": "ci04-fork", "old_ref_name": "main",
        }, user=args.fork_user, codes=(201,))
        fork_sha = checked(fork_api + "/contents/ci04-fork.txt", "POST", {
            "content": base64.b64encode("一期独立 fork 拒发探针\n".encode()).decode(),
            "branch": "ci04-fork", "message": "test(actions): 添加 fork 拒发样本",
        }, user=args.fork_user, codes=(201,))["commit"]["sha"]
        state["fork提交"] = fork_sha
        save()
        pr = checked(api + "/pulls", "POST", {
            "head": args.fork_user + ":ci04-fork", "base": "main",
            "title": "一期 fork 凭据拒发探针", "body": "仅验证合成受保护凭据不向 fork PR 下发。",
        }, user=args.fork_user, codes=(201,))
        state["PR"] = pr["number"]
        state["PR页面"] = pr["html_url"]
        save()
        print("独立 fork PR 已创建；执行 verify 核对真实 Runner", flush=True)

    else:
        if "仓库ID" not in state:
            parser.error("先 prepare")
        source_check()
        expected = {
            "受保护": ("push", state["分支提交"]["受保护"], True),
            "未保护": ("push", state["分支提交"]["未保护"], False),
        }
        if "PR" in state:
            expected["fork PR"] = ("pull_request", None, False)
        deadline = time.monotonic() + 300
        while time.monotonic() < deadline:
            runs = checked(api + "/actions/runs?limit=50")["workflow_runs"]
            found = {}
            for label, (event, sha, _) in expected.items():
                candidates = [run for run in runs if run["event"] == event and
                              run["path"].split("@", 1)[0].endswith("ci04-probe.yml") and
                              ((run["head_sha"] == sha and
                                run["path"].endswith("@refs/heads/" + BRANCHES[label])) if sha else
                               run["path"].endswith("@refs/pull/" + str(state["PR"]) + "/head"))]
                if candidates:
                    found[label] = max(candidates, key=lambda run: run["id"])
            if len(found) == len(expected) and all(run["status"] == "completed" for run in found.values()):
                break
            time.sleep(3)
        else:
            raise TimeoutError("300 秒内未收齐完成运行；保留现有 Run，不自动重跑")
        for label, (event, sha, want_secret) in expected.items():
            run = found[label]
            record(label + " Runner 运行成功", run["conclusion"], "success")
            jobs = checked(api + "/actions/runs/" + str(run["id"]) + "/jobs")["jobs"]
            record(label + " 恰有一项真实 Job", len(jobs), 1)
            job = jobs[0]
            code, log = request(api + "/actions/jobs/" + str(job["id"]) + "/logs", user=args.owner)
            record(label + " 可读取 Job 日志", code, 200)
            secret_value = args.state.with_suffix(".secret").read_text()
            record(label + " 原始日志未出现 Secret 明文", secret_value.encode() in log, False)
            record(label + " 原始日志未出现认证头", bool(re.search(rb"Authorization:\s*(?:Basic|Bearer|token)\s+\S+", log, re.I)), False)
            # 原始日志只在进程内解析，既不打印也不写盘，避免泄漏凭据。
            matches = re.findall(rb"CI04_PROBE event=(push|pull_request) ref=([^\s]+) secret_present=(true|false)", log)
            record(label + " 恰有一条布尔核对日志", len(matches), 1)
            seen_event, seen_ref, seen_secret = (field.decode() for field in matches[0])
            record(label + " 触发事件", seen_event, event)
            record(label + " 凭据下发边界", seen_secret, str(want_secret).lower())
            if label == "fork PR":
                record(label + " 引用为 PR", seen_ref.startswith("refs/pull/"), True)
            else:
                record(label + " 引用", seen_ref, "refs/heads/" + BRANCHES[label])
            state.setdefault("运行", {})[label] = {
                "ID": run["id"], "JobID": job["id"], "页面": run["html_url"],
                "提交": run["head_sha"], "事件": seen_event, "引用": seen_ref,
                "凭据存在": seen_secret, "结论": run["conclusion"],
            }
            save()
        source_check()
        print("三个场景均通过" if len(expected) == 3 else "两个 push 场景均通过；fork 尚未创建", flush=True)


if __name__ == "__main__":
    main()
