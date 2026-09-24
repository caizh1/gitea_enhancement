#!/usr/bin/env python3
"""独立隔离实例的 OCI 同摘要多标签验收；删除由普通 Owner 原生 UI 完成。"""

import argparse
import base64
import hashlib
import json
import os
import pathlib
import re
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("prepare", "denied", "archive", "restore", "revoke", "verify-one", "verify-last"))
    parser.add_argument("--isolated-instance", action="store_true", required=True)
    parser.add_argument("--accounts", type=pathlib.Path, required=True)
    parser.add_argument("--state", type=pathlib.Path, required=True)
    parser.add_argument("--oras", type=pathlib.Path, required=True)
    parser.add_argument("--source", required=True)
    args = parser.parse_args()
    accounts = json.loads(args.accounts.read_text())
    base = accounts["实例"].rstrip("/")
    if urllib.parse.urlsplit(base).hostname not in ("localhost", "127.0.0.1"):
        parser.error("本探针仅用于本机隔离实例")
    state = json.loads(args.state.read_text()) if args.state.exists() else {
        "源码与构建": args.source, "实例": base, "群组": accounts["群组路径"],
        "包名": "phase1-multitag-" + str(time.time_ns()), "步骤": [],
        "客户端摘要": hashlib.sha256(args.oras.read_bytes()).hexdigest(),
    }
    if state["实例"] != base or state["源码与构建"] != args.source:
        parser.error("状态与指定实例或源码不一致")
    client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    image = urllib.parse.urlsplit(base).netloc + "/" + state["群组"] + "/" + state["包名"]
    payload = ("一期同摘要多标签合成样本 " + state["包名"] + "\n").encode()
    state["内容摘要"] = hashlib.sha256(payload).hexdigest()

    def save():
        args.state.parent.mkdir(parents=True, exist_ok=True)
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")

    def check(label, actual, expected):
        ok = actual == expected
        state["步骤"].append({"时间": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                            "检查": label, "实际": actual, "预期": expected, "通过": ok})
        save()
        print(("通过：" if ok else "失败：") + label, flush=True)
        if not ok:
            raise AssertionError(label)

    def api(path, method="GET", body=None, role="Owner", expected=200):
        identity = accounts["账号"][role]
        token = base64.b64encode((identity["用户名"] + ":" + identity["密码"]).encode()).decode()
        request = urllib.request.Request(base + path, method=method,
            data=json.dumps(body).encode() if body is not None else None,
            headers={"Authorization": "Basic " + token, "Content-Type": "application/json"})
        try:
            with client.open(request, timeout=30) as response:
                code, raw = response.status, response.read()
        except urllib.error.HTTPError as error:
            code, raw = error.code, error.read()
        check(role + " " + method + " " + path, code, expected)
        return json.loads(raw) if raw else None

    with tempfile.TemporaryDirectory(prefix="phase1-oci-") as temporary:
        work = pathlib.Path(temporary)
        (work / "payload.txt").write_bytes(payload)
        count = 0

        def oras(role, verb, tag, tail=()):
            nonlocal count
            count += 1
            identity = accounts["账号"][role]
            reference = image + (tag if tag.startswith("@") else ":" + tag)
            command = [str(args.oras), *verb, "--plain-http", "--username", identity["用户名"],
                       "--password-stdin", "--registry-config", str(work / "auth.json"), reference]
            target = work / ("pull-" + str(count))
            if verb == ["push"]:
                command += ["--no-tty", "--image-spec", "v1.0", "--artifact-type", "application/vnd.phase1.probe.v1", "payload.txt:text/plain"]
            elif verb == ["pull"]:
                target.mkdir()
                command += ["--no-tty", "--output", str(target)]
            elif verb == ["manifest", "delete"]:
                command += ["--force"]
            command += list(tail)
            env = {key: value for key, value in os.environ.items() if key.lower() not in ("http_proxy", "https_proxy", "all_proxy")}
            result = subprocess.run(command, input=identity["密码"] + "\n", text=True,
                capture_output=True, cwd=work, env=env, timeout=50)
            detail = result.stderr.replace(identity["密码"], "[脱敏]")
            detail = re.sub(r"(?i)(token|authorization|password)\s*[:=]\s*\S+", r"\1=[脱敏]", detail)
            downloaded = target / "payload.txt"
            return result.returncode, result.stdout.strip(), detail[:700], downloaded.read_bytes() if downloaded.exists() else None

        def pull(role, tag, allowed=True):
            code, _, detail, body = oras(role, ["pull"], tag)
            check(role + " 拉取 " + tag, code == 0, allowed)
            if allowed:
                check(role + " " + tag + " 摘要", hashlib.sha256(body or b"").hexdigest(), state["内容摘要"])
            else:
                check(role + " " + tag + " 明确拒绝", bool(re.search(r"401|403|404|not found|unauthorized", detail, re.I)), True)

        group_path = "/api/v1/governance/groups/" + str(accounts["群组ID"])
        if args.mode == "prepare":
            if "manifest摘要" in state:
                parser.error("样本已创建，不重复上传")
            for role in accounts["账号"]:
                check(role + " 非管理员", api("/api/v1/user", role=role)["is_admin"], False)
            check("Owner 上传第一标签", oras("Owner", ["push"], "first")[0], 0)
            check("创建指向同一 manifest 的第二标签", oras("Owner", ["tag"], "first", ["second"])[0], 0)
            digests = [oras("Owner", ["resolve"], tag) for tag in ("first", "second")]
            check("两个摘要查询成功", [r[0] for r in digests], [0, 0])
            check("两个标签摘要相同", digests[0][1], digests[1][1])
            check("manifest 摘要格式", bool(re.fullmatch("sha256:[a-f0-9]{64}", digests[0][1])), True)
            state["manifest摘要"] = digests[0][1]
            for role in ("Owner", "Reporter"):
                for tag in ("first", "second"):
                    pull(role, tag)
            save()
        elif args.mode in ("archive", "restore"):
            group = api(group_path)
            api(group_path + "/archive", "PUT", {"archived": args.mode == "archive", "revision": group["revision"]})
            if args.mode == "archive":
                for tag in ("first", "second"):
                    code, _, detail, _ = oras("Owner", ["manifest", "delete"], tag)
                    state["步骤"].append({"观察": "归档删除协议反馈", "标签": tag, "退出码": code, "错误": detail})
                    check("归档拒删 " + tag, code != 0 and bool(re.search("401|403|423", detail)), True)
                    pull("Owner", tag)
        elif args.mode == "denied":
            for role in ("Reporter", "无关用户"):
                code, _, detail, _ = oras(role, ["manifest", "delete"], "first")
                check(role + " 拒删", code != 0 and bool(re.search("401|403|404", detail)), True)
                pull("Owner", "first")
            pull("无关用户", "first", False)
        elif args.mode == "revoke":
            group = api(group_path)
            api(group_path + "/members/" + str(accounts["账号"]["Reporter"]["用户ID"])
                + "?revision=" + str(group["revision"]), "DELETE", expected=204)
            pull("Reporter", "first", False)
            pull("Owner", "first")
        else:
            pull("Owner", "first", False)
            pull("Owner", "second", args.mode == "verify-one")
            pull("Owner", "@" + state["manifest摘要"], args.mode == "verify-one")


if __name__ == "__main__":
    main()
