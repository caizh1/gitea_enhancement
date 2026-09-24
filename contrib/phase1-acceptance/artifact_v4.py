#!/usr/bin/env python3
"""在明确指定的隔离实例复跑 v4 产物链路；浏览器删除另行人工操作并记录。"""

import argparse
import base64
import hashlib
import io
import json
import pathlib
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile


UPLOAD = "https://github.com/ChristopherHX/gitea-upload-artifact@81f940d004763f986ba3582c007fd842dd5cb0d7"
DOWNLOAD = "https://github.com/ChristopherHX/gitea-download-artifact@75635f32b4c1c41c4b3d64e8f85210112ed4c9c7"
NAMES = ("phase1-single", "phase1-bulk-a", "phase1-bulk-b", "phase1-retain")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("prepare", "trigger", "wait", "denied", "archive", "restore", "verify"))
    parser.add_argument("--base", required=True)
    parser.add_argument("--isolated-instance", action="store_true", required=True)
    parser.add_argument("--credentials", type=pathlib.Path, required=True, help="私有 JSON：password，或按用户名保存的密码")
    parser.add_argument("--owner", required=True)
    parser.add_argument("--reporter", required=True)
    parser.add_argument("--outsider", required=True)
    parser.add_argument("--org", required=True, help="实际已授权的原生组织兼容名")
    parser.add_argument("--repo", default="phase1-v4-" + str(int(time.time())))
    parser.add_argument("--runner-label", required=True)
    parser.add_argument("--state", type=pathlib.Path, required=True)
    parser.add_argument("--source", required=True, help="被测服务端源码版本／构建摘要")
    parser.add_argument("--missing", nargs="*", default=[])
    parser.add_argument("--run-deleted", action="store_true", help="核对已由原生 UI 删除的整次运行")
    args = parser.parse_args()
    base = args.base.rstrip("/")
    if urllib.parse.urlsplit(base).scheme not in ("http", "https"):
        parser.error("实例地址必须为 HTTP(S)")
    credentials = json.loads(args.credentials.read_text())
    state = json.loads(args.state.read_text()) if args.state.exists() else {
        "源码": args.source, "实例": base, "组织": args.org, "仓库": args.repo,
        "上传客户端": UPLOAD, "下载客户端": DOWNLOAD, "步骤": [],
    }
    if state["实例"] != base or state["组织"] != args.org or state["源码"] != args.source:
        parser.error("保存的样本与本次实例、组织或源码不符，请使用独立 state 文件")
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
                raise ValueError("私有文件缺少对应账号密码")
            headers["Authorization"] = "Basic " + base64.b64encode((user + ":" + password).encode()).decode()
        req = urllib.request.Request(base + path, method=method, headers=headers,
                                     data=json.dumps(body).encode() if body is not None else None)
        try:
            with opener.open(req, timeout=30) as response:
                return response.status, response.read()
        except urllib.error.HTTPError as error:
            return error.code, error.read()

    def check(label, actual, expected):
        ok = actual == expected
        state["步骤"].append({"时间": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                             "检查": label, "观察": actual, "预期": expected, "通过": ok})
        save()
        print(("通过：" if ok else "失败：") + label, flush=True)
        if not ok:
            raise AssertionError(label)

    def json_call(path, method="GET", body=None, user=args.owner, expected=200):
        code, raw = request(path, method, body, user)
        check(method + " " + path, code, expected)
        return json.loads(raw) if raw else None

    def artifacts():
        return json_call(api + "/actions/runs/" + str(state["运行ID"]) + "/artifacts")["artifacts"]

    def download(artifact, user):
        code, raw = request(api + "/actions/artifacts/" + str(artifact["id"]) + "/zip", user=user)
        check(user + " 下载 " + artifact["name"], code, 200)
        with zipfile.ZipFile(io.BytesIO(raw)) as archive:
            content = archive.read("payload.txt")
        digest = hashlib.sha256(content).hexdigest()
        check(user + " 内容摘要 " + artifact["name"], digest, state["内容摘要"])

    if args.mode == "prepare":
        if "仓库ID" in state:
            parser.error("样本已建立；改用 wait/verify，不重复触发")
        me = json_call("/api/v1/user")
        check("Owner 不带实例管理员身份", me["is_admin"], False)
        repo = json_call("/api/v1/orgs/" + args.org + "/repos", "POST",
                         {"name": state["仓库"], "private": True, "auto_init": True,
                          "default_branch": "main", "description": "一期 v4 独立产物验收"}, expected=201)
        state.update({"仓库ID": repo["id"], "页面": repo["html_url"]})
        save()
        json_call(api + "/collaborators/" + args.reporter, "PUT", {"permission": "read"}, expected=204)
        content = "一期 v4 内容核验 " + state["仓库"] + "\n"
        state["内容摘要"] = hashlib.sha256(content.encode()).hexdigest()
        content_b64 = base64.b64encode(content.encode()).decode()
        workflow = ("name: 一期 v4 产物完整链路\non:\n  push:\n    branches: [main]\njobs:\n  artifact:\n"
                    + "    runs-on: " + args.runner_label + "\n    steps:\n"
                    + "      - name: 记录真实客户端运行环境\n        run: |\n          uname -sm\n          node --version\n          git --version\n          python3 --version\n"
                    + "      - name: 生成合成样本\n        run: |\n"
                    + "          mkdir -p phase1-v4\n          cd phase1-v4\n"
                    + "          python3 -c \"import base64; open('payload.txt','wb').write(base64.b64decode('" + content_b64 + "'))\"\n")
        for name in NAMES:
            workflow += "      - uses: " + UPLOAD + "\n        with:\n          name: " + name + "\n          path: phase1-v4/payload.txt\n          if-no-files-found: error\n"
        workflow += ("      - uses: " + DOWNLOAD + "\n        with:\n          name: phase1-retain\n          path: phase1-v4-downloaded\n"
                     + "      - name: 下载内容核验\n        run: |\n"
                     + "          python3 -c \"import hashlib; from pathlib import Path; assert hashlib.sha256(Path('phase1-v4-downloaded/payload.txt').read_bytes()).hexdigest() == '"
                     + state["内容摘要"] + "'; print('v4 内容摘要一致')\"\n")
        # 保存实际工作流，方便在另一隔离实例复现；仅含合成样本，不含凭据。
        args.state.with_suffix(".workflow.yml").write_text(workflow)
        created = json_call(api + "/contents/.gitea/workflows/artifact-v4.yml", "POST",
                            {"content": base64.b64encode(workflow.encode()).decode(),
                             "branch": "main", "message": "test(actions): 固定 v4 客户端验收"}, expected=201)
        state["提交"] = created["commit"]["sha"]
        save()
    elif args.mode == "trigger":
        # 调用方先复制 state，保留第一次运行证据；新提交由正常 push 通知触发。
        created = json_call(api + "/contents/phase1-trigger-" + str(time.time_ns()) + ".txt", "POST",
                            {"content": base64.b64encode("一期整次运行删除样本\n".encode()).decode(),
                             "branch": "main", "message": "test(actions): 触发独立整次删除样本"}, expected=201)
        state["前次运行ID"] = state.pop("运行ID", None)
        state["提交"] = created["commit"]["sha"]
        save()
    elif args.mode == "wait":
        deadline = time.monotonic() + 300
        while time.monotonic() < deadline:
            code, raw = request(api + "/actions/runs?limit=20", user=args.owner)
            if code != 200:
                check("读取真实运行列表", code, 200)
            runs = json.loads(raw)["workflow_runs"]
            runs = [run for run in runs if run["head_sha"] == state["提交"] and run["path"].split("@", 1)[0] == "artifact-v4.yml"]
            if runs:
                run = runs[0]
                state.update({"运行ID": run["id"], "运行页面": run["html_url"]})
                save()
                if run["status"] == "completed":
                    check("真实 Runner v4 上传及下载成功", run["conclusion"], "success")
                    entries = artifacts()
                    check("四份 v4 产物可列出", sorted(a["name"] for a in entries), sorted(NAMES))
                    state["产物"] = [{"id": a["id"], "name": a["name"], "size_in_bytes": a["size_in_bytes"]} for a in entries]
                    save()
                    for artifact in entries:
                        download(artifact, args.owner)
                        download(artifact, args.reporter)
                    break
            time.sleep(3)
        else:
            raise TimeoutError("300 秒内未完成，保留样本及日志，不自动重跑")
    elif args.mode == "denied":
        for artifact in artifacts():
            for user in (args.reporter, args.outsider):
                code, _ = request(api + "/actions/artifacts/" + str(artifact["id"]), "DELETE", user=user)
                check(user + " 拒绝删除 " + artifact["name"], code in (401, 403, 404), True)
            code, _ = request(api + "/actions/artifacts/" + str(artifact["id"]) + "/zip", user=args.outsider)
            check("无关账号拒绝下载 " + artifact["name"], code in (401, 403, 404), True)
            download(artifact, args.owner)
    elif args.mode == "archive":
        json_call(api, "PATCH", {"archived": True})
        for artifact in artifacts():
            code, _ = request(api + "/actions/artifacts/" + str(artifact["id"]), "DELETE", user=args.owner)
            check("归档拒绝删除 " + artifact["name"], code, 423)
            download(artifact, args.owner)
    elif args.mode == "restore":
        json_call(api, "PATCH", {"archived": False})
    elif args.run_deleted:
        code, _ = request(api + "/actions/runs/" + str(state["运行ID"]), user=args.owner)
        check("UI 整次删除后运行不可读取", code, 404)
        for artifact in state["产物"]:
            code, _ = request(api + "/actions/artifacts/" + str(artifact["id"]) + "/zip", user=args.owner)
            check("整次删除后旧产物下载拒绝 " + artifact["name"], code, 404)
    else:
        entries = artifacts()
        present = {a["name"] for a in entries}
        for name in args.missing:
            check("UI 删除后不再列出 " + name, name in present, False)
        for artifact in entries:
            download(artifact, args.owner)
        for artifact in state.get("产物", []):
            if artifact["name"] in args.missing:
                code, _ = request(api + "/actions/artifacts/" + str(artifact["id"]) + "/zip", user=args.owner)
                check("已删产物旧下载拒绝 " + artifact["name"], code, 404)


if __name__ == "__main__":
    main()
