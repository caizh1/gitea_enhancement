#!/usr/bin/env python3
"""在独立群组与专属 Runner 验证 S05/S06 生命周期。"""

import argparse
import base64
import http.server
import json
import pathlib
import subprocess
import sys
import threading
import time
import urllib.request

from job_token_boundary import Client


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("prepare", "s06", "s06-verify-ui", "s05"))
    parser.add_argument("--base", default="http://127.0.0.1:13043")
    parser.add_argument("--credentials", required=True, type=pathlib.Path)
    parser.add_argument("--owner", default="phase1-v15-owner")
    parser.add_argument("--runner-binary", type=pathlib.Path)
    parser.add_argument("--state", required=True, type=pathlib.Path)
    parser.add_argument("--result", type=pathlib.Path)
    parser.add_argument("--resume-signal", type=pathlib.Path)
    args = parser.parse_args()
    if args.base.rstrip("/") != "http://127.0.0.1:13043":
        parser.error("只允许本机隔离实例 13043")
    api = Client(args.base, args.credentials, args.owner)
    if args.mode == "prepare":
        if args.state.exists():
            parser.error("夹具已存在，不覆盖首轮证据")
        if not args.runner_binary or not args.runner_binary.is_file():
            parser.error("准备阶段需要专用 Runner 二进制")
        state = {"说明": "S05/S06 独立生命周期真实验收", "实例": args.base,
                 "身份": args.owner, "步骤": [], "结果": "准备中"}
    else:
        if not args.state.exists():
            parser.error("先运行 prepare")
        state = json.loads(args.state.read_text())
    args.state.parent.mkdir(parents=True, exist_ok=True)
    if args.mode == "s06-verify-ui" and (not args.result or args.result.exists()):
        parser.error("UI恢复复核必须指定不存在的独立 --result 文件")
    save_path = args.result or args.state

    def save():
        save_path.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")

    def check(label, actual, expected):
        passed = actual in expected if isinstance(expected, (set, list)) else actual == expected
        state["步骤"].append({"时间": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                          "检查": label, "实际": actual,
                          "期望": sorted(expected) if isinstance(expected, set) else expected,
                          "通过": passed})
        save()
        if not passed:
            raise AssertionError(label + "：" + str(actual))

    def call(label, path, method="GET", body=None, expected=200):
        code, response = api.call(path, method, body)
        check(label, code, expected)
        return response

    def repo_api(name):
        return "/api/v1/repos/" + state["子组"]["兼容名"] + "/" + name

    def workflow(name, content):
        result = call("写入" + name + "工作流", repo_api("active") +
                      "/contents/.gitea/workflows/" + name, "POST", {
                          "content": base64.b64encode(content.encode()).decode(),
                          "branch": state["仓库"]["active"]["默认分支"],
                          "message": "加入一期生命周期隔离验收工作流"}, 201)
        return result["commit"]["sha"]

    def run(run_id):
        code, result = api.call(repo_api("active") + "/actions/runs/" + str(run_id))
        if code != 200:
            raise AssertionError("读取 Run " + str(run_id) + "：HTTP " + str(code))
        return result

    def wait_for(label, predicate, seconds=180):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            value = predicate()
            if value:
                return value
            time.sleep(2)
        raise TimeoutError(label)

    try:
        if args.mode == "prepare":
            me = call("普通 Owner 身份", "/api/v1/user")
            check("不能以实例管理员验收", me["is_admin"], False)
            suffix = str(time.time_ns())
            root = call("新建独立父组", "/api/v1/governance/groups", "POST", {
                "name": "一期生命周期父组", "path": "phase1-lifecycle-" + suffix,
                "parent_id": 0, "visibility": 2}, 201)
            child = call("新建独立子组", "/api/v1/governance/groups", "POST", {
                "name": "一期生命周期子组", "path": "child",
                "parent_id": root["id"], "visibility": 2}, 201)
            state["父组"] = {"ID": root["id"], "兼容名": root["compatibility_name"],
                           "完整路径": root["full_path"]}
            state["子组"] = {"ID": child["id"], "兼容名": child["compatibility_name"],
                           "完整路径": child["full_path"]}
            state["仓库"] = {}
            for name in ("active", "pending"):
                repo = call("新建" + name + "仓", "/api/v1/orgs/" + child["compatibility_name"] + "/repos",
                            "POST", {"name": name, "private": True, "auto_init": True,
                                     "default_branch": "main"}, 201)
                state["仓库"][name] = {"ID": repo["id"], "名称": name,
                                        "默认分支": repo["default_branch"], "页面": repo["html_url"]}
                save()
            token = call("获取专属子组 Runner 注册令牌", "/api/v1/orgs/" + child["compatibility_name"] +
                         "/actions/runners/registration-token", "POST")["token"]
            runner_dir = args.state.parent / "lifecycle-runner"
            runner_dir.mkdir(mode=0o700, exist_ok=False)
            secret = runner_dir / "registration-token.json"
            secret.write_text(json.dumps({"token": token}) + "\n")
            secret.chmod(0o600)
            label = "phase1-lifecycle-" + suffix
            config = runner_dir / "runner.yaml"
            config.write_text("log:\n  level: info\nrunner:\n  file: " + str(runner_dir / ".runner") +
                              "\n  capacity: 1\n  fetch_interval: 2s\n  labels:\n    - " + label +
                              ":host\ncache:\n  enabled: false\nhost:\n  workdir_parent: " +
                              str(runner_dir / "jobs") + "\n")
            config.chmod(0o600)
            runner_name = label
            with (runner_dir / "registration.log").open("w") as log:
                registered = subprocess.run([str(args.runner_binary), "register", "--no-interactive",
                    "--config", str(config), "--instance", args.base, "--token", token,
                    "--name", runner_name, "--labels", label + ":host"],
                    stdout=log, stderr=subprocess.STDOUT, timeout=30)
            check("专属 Runner 注册退出码", registered.returncode, 0)
            with (runner_dir / "daemon.log").open("ab") as log:
                daemon = subprocess.Popen([str(args.runner_binary), "daemon", "--config", str(config)],
                                          stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
            (runner_dir / "daemon.pid").write_text(str(daemon.pid) + "\n")
            runner_id = None
            for _ in range(30):
                listing = call("读取专属子组 Runner", "/api/v1/orgs/" + child["compatibility_name"] +
                               "/actions/runners")
                matches = [runner for runner in listing.get("runners", listing.get("entries", []))
                           if runner["name"] == runner_name and runner["status"] == "online"]
                if len(matches) == 1:
                    runner_id = matches[0]["id"]
                    break
                time.sleep(1)
            if runner_id is None:
                raise TimeoutError("专属 Runner 未上线")
            state["Runner"] = {"ID": runner_id, "名称": runner_name, "标签": label,
                               "PID": daemon.pid, "配置": str(config)}
            state["结果"] = "夹具可用"
            print("夹具可用，父组", root["id"], "子组", child["id"], "Runner", runner_id)
        elif args.mode == "s06":
            if "S06" in state:
                raise RuntimeError("S06 已执行，不重复构造旧运行")
            state["S06"] = {"阶段": "运行中"}
            save()
            reached = threading.Event()
            released = threading.Event()

            class Barrier(http.server.BaseHTTPRequestHandler):
                def do_GET(self):
                    if self.path != "/hold":
                        self.send_error(404)
                        return
                    reached.set()
                    if not released.wait(600):
                        self.send_error(504)
                        return
                    self.send_response(200)
                    self.end_headers()
                    self.wfile.write(b"go\n")

                def log_message(self, *_):
                    pass

            server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Barrier)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            state["S06"]["屏障端口"] = server.server_address[1]
            save()
            try:
                label = state["Runner"]["标签"]
                workflow("hold.yml", "name: S06 持有专属 Runner\non:\n  workflow_dispatch:\njobs:\n"
                         "  hold:\n    runs-on: " + label + "\n    steps:\n"
                         "      - run: curl --fail --silent http://127.0.0.1:" +
                         str(server.server_address[1]) + "/hold\n")
                schedule_sha = workflow("schedule.yml", "name: S06 旧定时任务\non:\n"
                    "  schedule:\n    - cron: '* * * * *'\n  workflow_dispatch:\njobs:\n"
                    "  scheduled:\n    runs-on: " + label + "\n    steps:\n"
                    "      - run: echo S06_TRUSTED_NEW_RUN\n")
                state["S06"]["工作流提交"] = schedule_sha
                dispatched = call("调度占用 Runner 的任务", repo_api("active") +
                    "/actions/workflows/hold.yml/dispatches?return_run_details=true", "POST",
                    {"ref": state["仓库"]["active"]["默认分支"]}, 200)
                hold_id = dispatched["workflow_run_id"]
                state["S06"]["占用RunID"] = hold_id
                save()
                if not reached.wait(90):
                    raise TimeoutError("专属 Runner 未到达 S06 屏障")
                check("S06 真 Runner 执行中", run(hold_id)["status"], "in_progress")

                def old_scheduled():
                    code, listing = api.call(repo_api("active") + "/actions/runs?limit=50")
                    if code != 200:
                        return None
                    matches = [entry for entry in listing.get("workflow_runs", [])
                               if entry.get("event") == "schedule" and
                               entry.get("path", "").split("@", 1)[0] == "schedule.yml" and
                               entry.get("head_sha") == schedule_sha]
                    return matches[0] if matches else None

                old = wait_for("180秒内未创建本机真实 cron Run", old_scheduled, 180)
                check("S06 旧定时 Run 待领取", old["status"], {"waiting", "queued", "pending"})
                state["S06"]["旧定时RunID"] = old["id"]
                save()
                pending_id = state["仓库"]["pending"]["ID"]
                deletion_path = "/api/v1/governance/repositories/" + str(pending_id) + "/deletion"
                before = call("待删仓原状态", deletion_path)
                check("待删仓初始无计划", before["pending"], None)
                pending = call("安排子仓独立待删除", deletion_path, "POST",
                               {"confirmation_path": before["full_path"]})
                check("子仓待删计划存在", pending["pending"] is not None, True)
                state["S06"]["待删仓路径"] = pending["full_path"]
                state["S06"]["待删到期"] = pending["pending"]["due_unix"]
                save()
                group_path = "/api/v1/governance/groups/" + str(state["父组"]["ID"])
                preview = call("父组归档影响预览", group_path + "/archive")
                check("归档影响包含两仓", preview["repositories"], 2)
                archived = call("归档父组", group_path + "/archive", "PUT",
                                {"archived": True, "revision": preview["revision"]})
                check("父组已归档", archived["archived"], True)
                state["S06"]["归档后旧Run"] = {"status": run(old["id"])["status"],
                                             "conclusion": run(old["id"]).get("conclusion")}
                save()
                if args.resume_signal:
                    state["S06"]["阶段"] = "父组已归档，等待页面取证后恢复"
                    save()
                    print("父组已归档；等待独立页面取证后放行恢复", flush=True)
                    wait_for("页面取证恢复信号超时", lambda: args.resume_signal.exists(), 300)
                restored = call("父组恢复前影响预览", group_path + "/archive")
                fresh = call("统一恢复父组", group_path + "/archive", "PUT",
                             {"archived": False, "revision": restored["revision"]})
                check("父组已恢复", fresh["archived"], False)
                active = call("恢复后活跃仓", repo_api("active"))
                pending_repo = call("恢复后待删仓", repo_api("pending").rsplit("/", 1)[0] + "/" +
                                    state["S06"]["待删仓路径"].split("/")[-1], expected=200)
                check("活跃仓解档", active["archived"], False)
                check("待删仓仍归档", pending_repo["archived"], True)
                after = call("恢复后待删状态", deletion_path)
                check("待删计划仍保留", after["pending"]["due_unix"], state["S06"]["待删到期"])
                code, _ = api.call(repo_api("pending") + "/contents/denied.txt", "POST", {
                    "content": base64.b64encode("待删不得写入\n".encode()).decode(),
                    "branch": state["仓库"]["pending"]["默认分支"], "message": "待删写入拒绝"})
                check("待删仓写入拒绝", code, {403, 404, 409, 423})
                old_after = run(old["id"])
                check("恢复后旧定时任务不复活", old_after["status"], "completed")
                check("恢复后旧定时任务已取消", old_after["conclusion"], "cancelled")
                released.set()
                wait_for("占用任务未收尾", lambda: run(hold_id)["status"] == "completed", 90)
                new = call("恢复后正式新触发", repo_api("active") +
                    "/actions/workflows/schedule.yml/dispatches?return_run_details=true", "POST",
                    {"ref": state["仓库"]["active"]["默认分支"]}, 200)
                new_id = new["workflow_run_id"]
                state["S06"]["可信新RunID"] = new_id
                save()
                final = wait_for("可信新运行未成功", lambda: run(new_id) if run(new_id)["status"] == "completed" else None, 120)
                check("可信新运行成功", final["conclusion"], "success")
                check("旧运行仍未复活", run(old["id"])["conclusion"], "cancelled")
                state["S06"]["结果"] = "通过"
                state["结果"] = "S06 通过；S05 待执行"
                print("S06 通过，旧定时 Run", old["id"], "可信新 Run", new_id)
            finally:
                released.set()
                server.shutdown()
        elif args.mode == "s06-verify-ui":
            old_id = state["S06"]["旧定时RunID"]
            pending_id = state["仓库"]["pending"]["ID"]
            group = call("确认普通 Owner UI 已恢复父组", "/api/v1/governance/groups/" + str(state["父组"]["ID"]))
            check("父组 UI 恢复结果", group["archived"], False)
            active = call("UI 恢复后活跃仓", repo_api("active"))
            check("活跃仓已恢复写入范围", active["archived"], False)
            pending = call("UI 恢复后待删计划", "/api/v1/governance/repositories/" + str(pending_id) + "/deletion")
            check("待删仓仍归档", pending["archived"], True)
            check("待删计划到期未变", pending["pending"]["due_unix"], state["S06"]["待删到期"])
            check("待删路径未变", pending["full_path"], state["S06"]["待删仓路径"])
            code, _ = api.call(repo_api("pending") + "/contents/denied.txt", "POST", {
                "content": base64.b64encode("恢复后待删仍不能写入\n".encode()).decode(),
                "branch": state["仓库"]["pending"]["默认分支"], "message": "待删拒绝"})
            check("恢复后待删仓写入拒绝", code, {403, 404, 409, 423})
            previous = run(old_id)
            check("旧定时 Run 不复活", (previous["status"], previous["conclusion"]),
                  ("completed", "cancelled"))
            hold = wait_for("占用任务未收尾", lambda: run(state["S06"]["占用RunID"]) if
                            run(state["S06"]["占用RunID"])["status"] == "completed" else None, 90)
            check("占用任务已自然收尾", hold["conclusion"], "success")
            triggered = call("恢复后正式触发可信新运行", repo_api("active") +
                "/actions/workflows/schedule.yml/dispatches?return_run_details=true", "POST",
                {"ref": state["仓库"]["active"]["默认分支"]}, 200)
            new_id = triggered["workflow_run_id"]
            check("新运行不是旧定时 Run", new_id != old_id, True)
            state["S06"]["可信新RunID"] = new_id
            save()
            final = wait_for("可信新运行未收尾", lambda: run(new_id) if
                             run(new_id)["status"] == "completed" else None, 120)
            check("可信新运行结果", final["conclusion"], "success")
            previous = run(old_id)
            check("可信新运行后旧定时 Run 仍取消", (previous["status"], previous["conclusion"]),
                  ("completed", "cancelled"))
            state["S06"]["阶段"] = "普通Owner UI恢复与可信新运行完成"
            state["S06"]["结果"] = "通过"
            state["结果"] = "S06 通过；S05 待执行"
            print("S06 UI 恢复复核通过，旧 Run", old_id, "可信新 Run", new_id)
        else:
            raise NotImplementedError("S05 执行阶段继续补齐")
    except Exception as error:
        state["结果"] = "失败"
        state["首错"] = str(error)
        raise
    finally:
        save()


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("停止：" + str(error), file=sys.stderr)
        sys.exit(1)
