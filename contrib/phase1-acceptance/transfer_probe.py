#!/usr/bin/env python3
"""隔离仓库的等待／执行中转移探针；转移由普通 Owner 在原生 UI 完成。"""

import argparse
import base64
import json
import pathlib
import threading
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("mode", choices=("prepare", "ready", "active", "barrier", "status", "release"))
    p.add_argument("--base", default="http://127.0.0.1:13043")
    p.add_argument("--isolated-instance", action="store_true", required=True)
    p.add_argument("--credentials", type=pathlib.Path, required=True)
    p.add_argument("--owner", default="phase1-v15-owner")
    p.add_argument("--source-org", required=True)
    p.add_argument("--target-org", required=True)
    p.add_argument("--runner-label", default="phase1-token-closure")
    p.add_argument("--state", type=pathlib.Path, required=True)
    p.add_argument("--port", type=int, default=14770)
    a = p.parse_args()
    if a.base != "http://127.0.0.1:13043":
        p.error("此探针仅用于明确授权的本机 13043 隔离实例")
    state = json.loads(a.state.read_text()) if a.state.exists() else {
        "实例": a.base, "源组织": a.source_org, "目标组织": a.target_org,
        "仓库": "phase1-transfer-" + str(time.time_ns()), "步骤": [],
    }
    if (state["实例"], state["源组织"], state["目标组织"]) != (a.base, a.source_org, a.target_org):
        p.error("已有样本与实例／范围不一致")
    client = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def save():
        a.state.parent.mkdir(parents=True, exist_ok=True)
        a.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")

    if a.mode == "barrier":
        released = threading.Event()
        entered = threading.Event()

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                if self.path == "/wait":
                    entered.set()
                    print("Runner 已到达显式执行屏障", flush=True)
                    ok = released.wait(240)
                    code, body = (200, b"released") if ok else (504, b"timeout")
                elif self.path == "/status":
                    code, body = 200, json.dumps({"entered": entered.is_set(), "released": released.is_set()}).encode()
                else:
                    code, body = 404, b""
                self.send_response(code)
                self.end_headers()
                self.wfile.write(body)

            def do_POST(self):
                if self.path == "/release":
                    released.set()
                    self.send_response(200)
                else:
                    self.send_response(404)
                self.end_headers()

            def log_message(self, *_):
                pass

        print("仅监听本机的显式屏障就绪", flush=True)
        ThreadingHTTPServer(("127.0.0.1", a.port), Handler).serve_forever()
        return
    if a.mode in ("status", "release"):
        req = urllib.request.Request(f"http://127.0.0.1:{a.port}/" + a.mode,
                                     method="POST" if a.mode == "release" else "GET")
        with client.open(req, timeout=5) as response:
            print(response.status, response.read().decode())
        return

    credentials = json.loads(a.credentials.read_text())
    password = credentials.get(a.owner, credentials.get("password"))
    auth = base64.b64encode((a.owner + ":" + password).encode()).decode()

    def request(path, method="GET", body=None, expected=200):
        req = urllib.request.Request(a.base + path, method=method,
                                     data=json.dumps(body).encode() if body is not None else None,
                                     headers={"Authorization": "Basic " + auth, "Content-Type": "application/json"})
        try:
            with client.open(req, timeout=30) as response:
                code, raw = response.status, response.read()
        except urllib.error.HTTPError as error:
            code, raw = error.code, error.read()
        state["步骤"].append({"时间": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                             "请求": method + " " + path, "状态": code, "预期": expected})
        save()
        if code != expected:
            raise AssertionError(f"{method} {path} 返回 {code}，预期 {expected}")
        return json.loads(raw) if raw else None

    if a.mode == "prepare":
        if "仓库ID" in state:
            p.error("同一 state 不重复创建")
        me = request("/api/v1/user")
        assert not me["is_admin"], "管理测试账号不得带实例管理员身份"
        repo = request("/api/v1/orgs/" + a.source_org + "/repos", "POST",
                       {"name": state["仓库"], "auto_init": True, "private": True, "default_branch": "main"}, 201)
        state.update({"仓库ID": repo["id"], "原页面": repo["html_url"]})
        owner, label = a.source_org, "phase1-deliberately-unmatched"
        action = "echo '旧范围排队任务不应执行'\n          exit 91"
    else:
        owner, label = a.target_org, a.runner_label
        repo = request("/api/v1/repos/" + owner + "/" + state["仓库"])
        assert repo["id"] == state["仓库ID"]
        state["新页面"] = repo["html_url"]
        action = "echo '目的范围新提交合法运行'"
        if a.mode == "active":
            action = f"curl --noproxy '*' --fail --max-time 250 http://127.0.0.1:{a.port}/wait\n          echo '合法执行中任务按原身份结束'"
    workflow = ("name: 一期转移状态验收\non:\n  push:\n    branches: [main]\njobs:\n  probe:\n"
                + "    runs-on: " + label + "\n    steps:\n      - name: 状态探针\n        run: |\n          " + action + "\n")
    path = "/api/v1/repos/" + owner + "/" + state["仓库"] + "/contents/.gitea/workflows/transfer.yml"
    payload = {"content": base64.b64encode(workflow.encode()).decode(), "branch": "main",
               "message": "test(actions): 一期转移 " + a.mode}
    if a.mode == "prepare":
        result = request(path, "POST", payload, 201)
    else:
        payload["sha"] = request(path)["sha"]
        result = request(path, "PUT", payload)
    state.setdefault("提交", {})[a.mode] = result["commit"]["sha"]
    save()
    print(json.dumps({"仓库ID": state["仓库ID"], "仓库": state["仓库"], "阶段": a.mode, "提交": result["commit"]["sha"]}, ensure_ascii=False))


if __name__ == "__main__":
    main()
