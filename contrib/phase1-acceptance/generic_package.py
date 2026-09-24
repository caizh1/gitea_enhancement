#!/usr/bin/env python3
"""在本机隔离实例用正式 Generic 协议验收发布、下载、删除与撤权。"""

import argparse
import base64
import hashlib
import json
import pathlib
import platform
import re
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


DENIED = {401, 403, 404}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", default="http://127.0.0.1:13043")
    parser.add_argument("--isolated-instance", action="store_true", required=True)
    parser.add_argument("--credentials", type=pathlib.Path, required=True)
    parser.add_argument("--owner", required=True)
    parser.add_argument("--reporter", required=True)
    parser.add_argument("--outsider", required=True)
    parser.add_argument("--binary", type=pathlib.Path, required=True)
    parser.add_argument("--expected-binary-sha256", required=True)
    parser.add_argument("--source-head", required=True)
    parser.add_argument("--state", type=pathlib.Path, required=True)
    args = parser.parse_args()
    if args.base.rstrip("/") not in ("http://127.0.0.1:13043", "http://localhost:13043"):
        parser.error("本脚本只允许本机隔离实例 13043")
    if args.state.exists():
        parser.error("结果文件已存在；不得覆盖或重试同一批对象")
    source_head = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    if source_head != args.source_head:
        parser.error("当前源码 HEAD 与指定版本不同")
    binary_sha = hashlib.sha256(args.binary.read_bytes()).hexdigest()
    if binary_sha != args.expected_binary_sha256:
        parser.error("二进制摘要与指定候选不同")
    credentials = json.loads(args.credentials.read_text())
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    suffix = str(time.time_ns())
    group_path = "phase1-generic-" + suffix
    package_name = "phase1-generic-package-" + suffix
    state = {
        "结果": "执行中", "客户端": "Python urllib 标准库 Generic HTTP",
        "源码HEAD": source_head, "二进制路径": str(args.binary), "二进制SHA256": binary_sha,
        "平台": platform.platform(), "架构": platform.machine(), "实例": args.base,
        "身份": {"Owner": args.owner, "Reporter": args.reporter, "无关": args.outsider},
        "群组路径": group_path, "仓库名称": "generic-evidence", "包名": package_name, "步骤": [],
    }
    args.state.parent.mkdir(parents=True, exist_ok=True)

    def save():
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")

    def request(path, method="GET", user=None, body=None, content_type="application/json"):
        headers = {"Accept": "application/json"}
        if user:
            password = credentials.get(user, credentials.get("password"))
            if not isinstance(password, str):
                raise ValueError("私有凭据缺少指定账号")
            auth = base64.b64encode((user + ":" + password).encode()).decode()
            headers["Authorization"] = "Basic " + auth
        if body is not None:
            headers["Content-Type"] = content_type
        req = urllib.request.Request(args.base.rstrip("/") + path, method=method, headers=headers, data=body)
        try:
            with opener.open(req, timeout=30) as response:
                return response.status, response.read()
        except urllib.error.HTTPError as error:
            code, raw = error.code, error.read(1000)
            error.close()
            return code, raw

    def check(label, actual, expected, raw=b""):
        ok = actual in expected if isinstance(expected, (set, list, tuple)) else actual == expected
        item = {"检查": label, "实际": actual, "期望": sorted(expected) if isinstance(expected, set) else expected, "通过": ok}
        if not ok:
            detail = raw.decode("utf-8", "replace")[:500]
            for value in [credentials.get("password"), credentials.get(args.owner), credentials.get(args.reporter), credentials.get(args.outsider)]:
                if isinstance(value, str) and value:
                    detail = detail.replace(value, "[脱敏]")
            item["错误正文"] = re.sub(r"(?i)(authorization|token|password)\s*[:=]\s*\S+", r"\1=[脱敏]", detail)
        state["步骤"].append(item)
        save()
        if not ok:
            raise AssertionError(label + "：实际 " + str(actual))

    def api(path, method="GET", user=args.owner, body=None, expected=200):
        payload = json.dumps(body).encode() if body is not None else None
        code, raw = request(path, method, user, payload)
        check(method + " " + path, code, expected, raw)
        return json.loads(raw) if raw else None

    def generic(version, method="GET", user=args.owner, payload=None, filename="payload.txt"):
        name = urllib.parse.quote(package_name, safe="")
        owner = urllib.parse.quote(state["群组兼容名"], safe="")
        path = f"/api/packages/{owner}/generic/{name}/{version}"
        if method != "DELETE":
            path += "/" + urllib.parse.quote(filename, safe="")
        return request(path, method, user, payload, "application/octet-stream")

    def download(label, version, user, payload):
        code, raw = generic(version, user=user)
        check(label + " 状态", code, 200, raw)
        check(label + " SHA256", hashlib.sha256(raw).hexdigest(), hashlib.sha256(payload).hexdigest())

    save()
    try:
        code, raw = request("/api/healthz")
        check("隔离实例健康", code, 200, raw)
        for role, user in state["身份"].items():
            identity = api("/api/v1/user", user=user)
            check(role + " 非实例管理员", identity["is_admin"], False)
            state.setdefault("用户ID", {})[role] = identity["id"]
            save()

        group = api("/api/v1/governance/groups", "POST", body={
            "name": "一期 Generic 独立协议验收", "path": group_path, "parent_id": 0, "visibility": 2,
        }, expected=201)
        state["群组ID"] = group["id"]
        state["群组兼容名"] = group["compatibility_name"]
        save()
        repo = api("/api/v1/orgs/" + state["群组兼容名"] + "/repos", "POST", body={
            "name": state["仓库名称"], "private": True, "auto_init": True,
        }, expected=201)
        state["仓库ID"] = repo["id"]
        save()

        group = api("/api/v1/governance/groups/" + str(group["id"]))
        api(f"/api/v1/governance/groups/{group['id']}/members/{state['用户ID']['Reporter']}", "PUT",
            body={"role": 20, "revision": group["revision"]}, expected=204)
        reporter_view = api("/api/v1/governance/groups/" + str(group["id"]), user=args.reporter)
        grants = [grant for grant in reporter_view["grants"] if grant.get("source") == "direct"]
        check("Reporter 仅本独立群组直接授权", len(grants), 1)
        check("Reporter 角色", grants[0]["role"], 20)
        code, raw = request("/api/v1/governance/groups/" + str(group["id"]), user=args.outsider)
        check("无关账号群组拒读", code, DENIED, raw)

        first = ("一期 Generic 发布版本一 " + package_name + "\n").encode()
        retain = ("一期 Generic 保留版本二 " + package_name + "\n").encode()
        state["内容SHA256"] = {"1.0.0": hashlib.sha256(first).hexdigest(), "2.0.0": hashlib.sha256(retain).hexdigest()}
        save()
        for version, payload in (("1.0.0", first), ("2.0.0", retain)):
            code, raw = generic(version, "PUT", payload=payload)
            check("Owner 发布 " + version, code, 201, raw)
            download("Owner 下载 " + version, version, args.owner, payload)
            download("Reporter 下载 " + version, version, args.reporter, payload)

        for method, filename, body in (("PUT", "denied.txt", first), ("DELETE", "payload.txt", None)):
            code, raw = generic("1.0.0", method, args.reporter, body, filename)
            check("Reporter 拒绝 " + method, code, DENIED, raw)
            download("拒写后 Owner 对照", "1.0.0", args.owner, first)
        code, raw = generic("1.0.0", user=args.outsider)
        check("无关账号拒读", code, DENIED, raw)
        code, raw = generic("1.0.0", "DELETE", args.outsider)
        check("无关账号拒删", code, DENIED, raw)
        download("越权后 Owner 对照", "1.0.0", args.owner, first)

        current = api("/api/v1/governance/groups/" + str(group["id"]))
        api(f"/api/v1/governance/groups/{group['id']}/members/{state['用户ID']['Reporter']}?revision={current['revision']}",
            "DELETE", expected=204)
        state["撤权完成时间"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        save()
        for version, payload in (("1.0.0", first), ("2.0.0", retain)):
            code, raw = generic(version, user=args.reporter)
            check("撤权后 Reporter 下一请求拒读 " + version, code, DENIED, raw)
            download("撤权后 Owner 对照 " + version, version, args.owner, payload)

        code, raw = generic("1.0.0", "DELETE")
        check("Owner 删除指定版本", code, 204, raw)
        code, raw = generic("1.0.0")
        check("已删版本下一请求拒读", code, 404, raw)
        download("另一版本未受影响", "2.0.0", args.owner, retain)
        state["结果"] = "通过"
    except Exception as error:
        state["结果"] = "失败"
        state["首错"] = str(error)
        save()
        print("失败：" + str(error) + "；原始结果：" + str(args.state))
        return 1
    save()
    print("通过：Generic 完整闭环；原始结果：" + str(args.state))
    return 0


if __name__ == "__main__":
    sys.exit(main())
