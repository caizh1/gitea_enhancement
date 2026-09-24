#!/usr/bin/env python3
"""在隔离仓库用真实 HTTP 正文屏障复测 Release 附件归档、撤权。"""

import argparse
import base64
import hashlib
import http.client
import http.cookiejar
import json
from pathlib import Path
import threading
import time
import urllib.error
import urllib.parse
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("archive", "revoke"))
    parser.add_argument("--isolated-instance", action="store_true", required=True)
    parser.add_argument("--credentials", type=Path, required=True)
    parser.add_argument("--fixture", type=Path, required=True, help="已有专用 Release 状态 JSON")
    parser.add_argument("--owner", required=True)
    parser.add_argument("--actor", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    fixture = json.loads(args.fixture.read_text())
    creds = json.loads(args.credentials.read_text())
    base = fixture["实例"].rstrip("/")
    parsed = urllib.parse.urlsplit(base)
    if parsed.hostname not in ("127.0.0.1", "localhost") or parsed.scheme != "http":
        parser.error("仅允许明确的本机隔离 HTTP 实例")
    if not fixture["仓库"].startswith("a03b-"):
        parser.error("只操作本轮 a03b- 专用仓库")
    repo = "/api/v1/repos/" + fixture["组织"] + "/" + fixture["仓库"]
    assets = repo + "/releases/" + str(fixture["ReleaseID"]) + "/assets"
    password = creds.get("password")
    if not isinstance(password, str):
        parser.error("私有凭据需包含 password")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def auth(user):
        return "Basic " + base64.b64encode((user + ":" + password).encode()).decode()

    def request(path, method="GET", body=None, user=args.owner, content_type="application/json"):
        headers = {"Authorization": auth(user), "Content-Type": content_type}
        data = json.dumps(body).encode() if isinstance(body, dict) else body
        req = urllib.request.Request(base + path, method=method, data=data, headers=headers)
        try:
            with opener.open(req, timeout=20) as resp:
                return resp.status, resp.read()
        except urllib.error.HTTPError as error:
            return error.code, error.read()

    prior_code, prior_raw = request(assets)
    if prior_code != 200:
        raise RuntimeError("开始前附件列表不可读")
    prior = {entry["name"]: entry["id"] for entry in json.loads(prior_raw)}
    if prior.get("baseline.txt") != fixture["原附件ID"]:
        raise RuntimeError("原附件与专用状态不一致")
    name = "barrier-" + args.mode + "-" + str(time.time_ns()) + ".txt"
    payload = b"A03b-real-http-body\n" * 25000
    sent = threading.Event()
    resume = threading.Event()
    outcome = {}

    def upload():
        try:
            conn = http.client.HTTPConnection(parsed.hostname, parsed.port, timeout=30)
            conn.putrequest("POST", assets + "?name=" + name)
            conn.putheader("Authorization", auth(args.owner if args.mode == "archive" else args.actor))
            conn.putheader("Content-Type", "application/octet-stream")
            conn.putheader("Content-Length", str(len(payload)))
            conn.endheaders()
            conn.send(payload[:16384])
            sent.set()
            if not resume.wait(20):
                raise TimeoutError("正文屏障未释放")
            conn.send(payload[16384:])
            resp = conn.getresponse()
            outcome["上传HTTP"] = resp.status
            outcome["响应前缀"] = resp.read(300).decode(errors="replace")
            conn.close()
        except Exception as error:
            outcome["客户端错误"] = str(error)
            sent.set()

    if args.mode == "revoke":
        grant, _ = request(repo + "/collaborators/" + args.actor, "PUT", {"permission": "write"})
        if grant != 204:
            raise RuntimeError("无法给专用协作者授予本仓写权")
        outcome["授权HTTP"] = grant

    thread = threading.Thread(target=upload, daemon=True)
    thread.start()
    if not sent.wait(5):
        raise TimeoutError("正文首块未发送")
    time.sleep(0.2)
    if args.mode == "archive":
        outcome["范围变更HTTP"], _ = request(repo, "PATCH", {"archived": True})
    else:
        outcome["范围变更HTTP"], _ = request(repo + "/collaborators/" + args.actor, "DELETE")
    resume.set()
    thread.join(35)
    if thread.is_alive():
        raise TimeoutError("上传请求未完成")
    if args.mode == "archive":
        outcome["恢复HTTP"], _ = request(repo, "PATCH", {"archived": False})

    list_code, raw = request(assets)
    after = {entry["name"]: entry["id"] for entry in json.loads(raw)} if list_code == 200 else {}
    outcome["附件列表HTTP"] = list_code
    outcome["原附件ID保持"] = after.get("baseline.txt") == prior["baseline.txt"]
    outcome["其他附件集合保持"] = after == prior
    outcome["被拒新附件不存在"] = name not in after
    if args.mode == "revoke":
        outcome["撤权后新请求HTTP"], _ = request(assets + "?name=next-denied.txt", "POST", b"next", args.actor, "application/octet-stream")

    cookies = http.cookiejar.CookieJar()
    session = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(cookies))
    session.open(base + "/user/login", timeout=10).read()
    login = urllib.parse.urlencode({"user_name": args.owner, "password": password}).encode()
    session.open(urllib.request.Request(base + "/user/login", data=login, method="POST"), timeout=10).read()
    with session.open(fixture["原附件下载URL"], timeout=10) as resp:
        outcome["原附件下载HTTP"] = resp.status
        outcome["原附件摘要一致"] = hashlib.sha256(resp.read()).hexdigest() == fixture["原附件SHA256"]

    expected = 423 if args.mode == "archive" else 403
    passed = (outcome.get("范围变更HTTP") == (200 if args.mode == "archive" else 204)
              and outcome.get("上传HTTP") == expected and outcome["其他附件集合保持"]
              and outcome["原附件摘要一致"] and outcome["原附件下载HTTP"] == 200)
    if args.mode == "archive":
        passed = passed and outcome.get("恢复HTTP") == 200
    else:
        passed = passed and outcome.get("撤权后新请求HTTP") in (403, 404)
    result = {"说明": "仅证明真实 HTTP 附件正文首块后的范围变化，未定位数据库事务内部交错",
              "模式": args.mode, "实例": base, "仓库ID": fixture["仓库ID"], "ReleaseID": fixture["ReleaseID"],
              "上传字节": len(payload), "首块字节": 16384, "结果": outcome, "通过": bool(passed)}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"模式": args.mode, "通过": passed, "结果": {k: v for k, v in outcome.items() if k != "响应前缀"}}, ensure_ascii=False))
    if not passed:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
