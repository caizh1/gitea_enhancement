#!/usr/bin/env python3
"""最终候选就绪后触发并核对同仓祖先 CI、PR 与 v4 产物。"""

import argparse
import hashlib
import io
import json
import os
import pathlib
import subprocess
import sys
import time
import zipfile

from job_token_boundary import Client


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("stage", choices=("trigger", "observe", "verify-ui"))
    parser.add_argument("--fixture", required=True, type=pathlib.Path)
    parser.add_argument("--state", required=True, type=pathlib.Path)
    parser.add_argument("--server-binary", required=True, type=pathlib.Path)
    parser.add_argument("--expected-server-sha256", required=True)
    parser.add_argument("--source-fingerprint", required=True)
    args = parser.parse_args()
    fixture = json.loads(args.fixture.read_text())
    if fixture["阶段"] != "准备完成；等待来源UI登记及最终候选后触发":
        parser.error("夹具尚未达到准备完成阶段")
    if args.stage == "trigger" and args.state.exists():
        parser.error("Run 状态已存在，保留首轮结果，不重复触发")
    if args.stage != "trigger" and not args.state.exists():
        parser.error("先执行 trigger")
    binary_hash = hashlib.sha256(args.server_binary.read_bytes()).hexdigest()
    if binary_hash != args.expected_server_sha256:
        parser.error("当前运行入口的二进制摘要与指定候选不一致")
    state = json.loads(args.state.read_text()) if args.state.exists() else {
        "说明": "同一父级授权主链最终候选真实触发与观察",
        "实例": fixture["实例"], "候选二进制SHA256": binary_hash,
        "源码指纹": args.source_fingerprint, "步骤": [], "阶段": "触发中",
    }
    if state["候选二进制SHA256"] != binary_hash or state["源码指纹"] != args.source_fingerprint:
        parser.error("已有状态绑定另一候选")
    args.state.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    api_client = Client(fixture["实例"], fixture["私有凭据文件"], fixture["普通父Owner"])
    consumer = fixture["仓库"]["消费"]
    repo = consumer["full_path"]
    api_path = "/api/v1/repos/" + repo

    def save():
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")
        args.state.chmod(0o600)

    def check(label, actual, expected):
        passed = actual == expected
        state["步骤"].append({"检查": label, "实际": actual, "期望": expected, "通过": passed})
        save()
        if not passed:
            raise AssertionError(label + "；首错与现场已保留")

    def api(label, path, method="GET", body=None, expected=200, user=None):
        status, result = api_client.call(path, method, body, user)
        check(label, status, expected)
        return result

    if args.stage == "trigger":
        save()
        current = api("读取准确消费仓", api_path)
        check("消费仓ID", current["id"], consumer["id"])
        developer = api("读取Developer身份", "/api/v1/user", user=fixture["Developer"])
        check("Developer非管理员", developer["is_admin"], False)
        group = api("读取Developer子组授权", "/api/v1/governance/groups/92", user=fixture["Developer"])
        check("Developer仅继承", [(g["source"], g["role"]) for g in group["grants"]], [("inherited", 30)])
        team = api("复核子组原生Owners Team", "/api/v1/teams/81/members")
        check("父Owner无子组直接Team", 76 in {u["id"] for u in team}, False)
        check("Developer无子组直接Team", developer["id"] in {u["id"] for u in team}, False)
        code, _ = api_client.call(api_path + "/contents/.gitea/workflows")
        check("消费仓没有本级YAML", code, 404)

        checkout = args.state.parent / "checkout"
        if checkout.exists():
            raise RuntimeError("私有 checkout 已存在；不覆盖首轮 Git 现场")
        askpass = args.state.parent / "askpass.sh"
        askpass.write_text("#!/bin/sh\ncase \"$1\" in *Username*) printf '%s\\n' \"$PHASE1_TEST_GIT_USER\";; *) printf '%s\\n' \"$PHASE1_TEST_GIT_PASSWORD\";; esac\n")
        askpass.chmod(0o700)
        credentials = json.loads(pathlib.Path(fixture["私有凭据文件"]).read_text())
        env = os.environ.copy()
        env.update({"GIT_ASKPASS": str(askpass), "GIT_TERMINAL_PROMPT": "0",
                    "PHASE1_TEST_GIT_USER": fixture["Developer"],
                    "PHASE1_TEST_GIT_PASSWORD": credentials[fixture["Developer"]]})

        def git(label, *parts, cwd=None):
            command = ["git", "-c", "credential.helper=", *parts]
            result = subprocess.run(command, cwd=cwd, env=env, stdout=subprocess.PIPE,
                                    stderr=subprocess.STDOUT, timeout=60)
            state["步骤"].append({"检查": label, "退出码": result.returncode, "通过": result.returncode == 0})
            save()
            if result.returncode:
                log = args.state.parent / ("git-" + label + ".log")
                log.write_bytes(result.stdout)
                log.chmod(0o600)
                raise RuntimeError(label + "失败；私有原始日志已保留")
            return result.stdout.decode().strip()

        remote = fixture["实例"] + "/" + repo + ".git"
        git("clone", "clone", remote, str(checkout))
        branch = "phase1-main-chain-" + str(time.time_ns())[-10:]
        git("new-branch", "checkout", "-b", branch, cwd=checkout)
        git("author-name", "config", "user.name", fixture["Developer"], cwd=checkout)
        git("author-email", "config", "user.email", fixture["Developer"] + "@example.invalid", cwd=checkout)
        (checkout / "phase1-main-chain.txt").write_text("一期父级继承 Developer 真实 Git 提交\n")
        git("add", "add", "phase1-main-chain.txt", cwd=checkout)
        git("commit", "commit", "-m", "test(governance): 验收父级CI主链\n\nAssisted-by: Codex:gpt-6", cwd=checkout)
        commit = git("commit-sha", "rev-parse", "HEAD", cwd=checkout)
        git("push", "push", "origin", "HEAD:refs/heads/" + branch, cwd=checkout)
        remote_ref = git("remote-ref", "ls-remote", remote, "refs/heads/" + branch)
        check("真实远端分支SHA", remote_ref.split()[0], commit)
        state.update({"Developer提交SHA": commit, "分支": branch,
                      "Git远端": remote, "触发时间UTC": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())})
        save()
        pr = api("Developer正式创建同仓PR", api_path + "/pulls", "POST", {
            "head": branch, "base": consumer["default_branch"],
            "title": "一期父级继承CI与v4产物主链",
            "body": "专用隔离仓库；父组 scoped 来源提供工作流，消费仓无本级 YAML。",
        }, 201, user=fixture["Developer"])
        state.update({"PR编号": pr["number"], "PR内部ID": pr["id"], "PR页面": pr["html_url"],
                      "阶段": "PR已创建；等待真实Runner完成"})
        save()
        print(json.dumps({"PR": state["PR页面"], "提交SHA": commit,
                          "候选SHA256": binary_hash}, ensure_ascii=False))
        return

    if args.stage == "observe":
        deadline = time.monotonic() + 300
        while time.monotonic() < deadline:
            runs = api("读取消费仓真实Run列表", api_path + "/actions/runs?limit=30")["workflow_runs"]
            matches = [r for r in runs if r["event"] == "pull_request" and
                       r["path"].split("@", 1)[0].endswith("main-chain.yml")]
            if len(matches) > 1:
                raise AssertionError("同一专用仓有多个主链PR Run，保留现场人工核对")
            if matches:
                run = matches[0]
                state["Run"] = {k: run.get(k) for k in ("id", "event", "path", "head_sha", "status", "conclusion", "html_url")}
                save()
                if run["status"] == "completed":
                    break
            time.sleep(3)
        else:
            raise TimeoutError("300秒内真实Runner未完成；不自动重触发")
        check("祖先scoped Run成功", run["conclusion"], "success")
        jobs = api("读取真实Job", api_path + "/actions/runs/" + str(run["id"]) + "/jobs")
        entries = jobs.get("jobs", [])
        check("同Run只有一个真实Job", len(entries), 1)
        job = entries[0]
        state["Job"] = {k: job.get(k) for k in ("id", "status", "conclusion", "runner_id", "html_url")}
        save()
        check("专属父Runner领取Job", job.get("runner_id"), fixture["Runner"]["ID"])
        check("Job成功", job.get("conclusion"), "success")
        artifacts = api("读取同Run v4产物", api_path + "/actions/runs/" + str(run["id"]) + "/artifacts")
        items = artifacts.get("artifacts", [])
        check("同Run仅有固定v4产物", [a["name"] for a in items], ["phase1-chain-v4"])
        artifact = items[0]
        status, raw = api_client.call(api_path + "/actions/artifacts/" + str(artifact["id"]) + "/zip")
        check("Owner正式API下载v4 ZIP", status, 200)
        with zipfile.ZipFile(io.BytesIO(raw)) as archive:
            content = archive.read("payload.txt")
        check("v4内容摘要", hashlib.sha256(content).hexdigest(), fixture["内容摘要"])
        state["产物"] = {"ID": artifact["id"], "名称": artifact["name"], "内容SHA256": fixture["内容摘要"]}
        approval = api("读取同PR审批状态", "/api/v1/governance/pulls/" + str(state["PR内部ID"]) + "/approval-state")
        state["审批状态"] = {k: approval.get(k) for k in ("satisfied", "required", "eligible_count", "approved_user_ids")}
        state["阶段"] = "真实Runner及v4完成；等待原生UI审批合并和产物删除"
        save()
        print(json.dumps({"Run": state["Run"], "Job": state["Job"],
                          "产物": state["产物"], "PR": state["PR页面"],
                          "审批状态": state["审批状态"]}, ensure_ascii=False))
        return

    pr = api("核对原生UI合并结果", api_path + "/pulls/" + str(state["PR编号"]))
    check("PR已合并", pr["merged"], True)
    status, _ = api_client.call(api_path + "/actions/artifacts/" + str(state["产物"]["ID"]) + "/zip")
    check("UI删除后旧产物不可下载", status, 404)
    state["阶段"] = "同仓主链完成"
    save()
    print("同仓父级主链已完成，Run/Job/PR/产物ID保存在私有状态")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("已停止并保留首错：" + str(error), file=sys.stderr)
        raise
