#!/usr/bin/env python3
"""在隔离仓库记录 v4 产物预授权地址的实际签发、撤权与到期边界。"""

import argparse
import base64
import hashlib
import io
import json
from pathlib import Path
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, new_url):
        return None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("issue", "revoke", "expired"))
    parser.add_argument("--isolated-instance", action="store_true", required=True)
    parser.add_argument("--credentials", type=Path, required=True)
    parser.add_argument("--fixture", type=Path, required=True, help="artifact_v4.py 产生的专用样本状态")
    parser.add_argument("--state", type=Path, required=True, help="含真实签名URL，仅存仓库外私有目录")
    parser.add_argument("--owner", required=True)
    parser.add_argument("--actor", required=True)
    args = parser.parse_args()
    fixture = json.loads(args.fixture.read_text())
    base = fixture["实例"].rstrip("/")
    parsed = urllib.parse.urlsplit(base)
    if parsed.scheme != "http" or parsed.hostname not in ("127.0.0.1", "localhost"):
        parser.error("只允许本机隔离 HTTP 实例")
    if not fixture["仓库"].startswith("a03b-"):
        parser.error("只操作本轮 a03b- 专用仓库")
    creds = json.loads(args.credentials.read_text())
    password = creds.get("password")
    if not isinstance(password, str):
        parser.error("私有凭据需包含 password")
    repo = "/api/v1/repos/" + fixture["组织"] + "/" + fixture["仓库"]
    artifact = next(item for item in fixture["产物"] if item["name"] == "phase1-retain")
    endpoint = base + repo + "/actions/artifacts/" + str(artifact["id"]) + "/zip"
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    state = json.loads(args.state.read_text()) if args.state.exists() else {
        "说明": "签名URL为短期 bearer 能力，仅保存私有文件；公开证据不含URL和签名参数",
        "仓库ID": fixture["仓库ID"], "产物ID": artifact["id"], "步骤": [],
    }
    if state["仓库ID"] != fixture["仓库ID"] or state["产物ID"] != artifact["id"]:
        parser.error("签名状态与专用仓库/产物不符")

    def save():
        args.state.parent.mkdir(parents=True, exist_ok=True)
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")
        args.state.chmod(0o600)

    def request(url, user=None, method="GET", body=None):
        headers = {}
        if user:
            token = base64.b64encode((user + ":" + password).encode()).decode()
            headers["Authorization"] = "Basic " + token
        if body is not None:
            headers["Content-Type"] = "application/json"
            body = json.dumps(body).encode()
        req = urllib.request.Request(url, method=method, data=body, headers=headers)
        try:
            with opener.open(req, timeout=20) as response:
                return response.status, response.headers, response.read()
        except urllib.error.HTTPError as error:
            return error.code, error.headers, error.read()

    def content_digest(body):
        with zipfile.ZipFile(io.BytesIO(body)) as archive:
            return hashlib.sha256(archive.read("payload.txt")).hexdigest()

    if args.mode == "issue":
        if "签名URL" in state:
            parser.error("本状态已含签名URL，避免覆盖首个时序证据")
        grant, _, _ = request(base + repo + "/collaborators/" + args.actor, args.owner, "PUT", {"permission": "read"})
        if grant != 204:
            raise RuntimeError("无法授予专用仓库只读协作权限")
        issued, headers, _ = request(endpoint, args.actor)
        if issued != 302 or not headers.get("Location"):
            raise RuntimeError("未取得原生 302 签名地址")
        signed = headers["Location"]
        expires = int(urllib.parse.parse_qs(urllib.parse.urlsplit(signed).query)["expires"][0])
        remaining = expires - int(time.time())
        direct, _, body = request(signed)
        same = direct == 200 and content_digest(body) == fixture["内容摘要"]
        state.update({"签名URL": signed, "到期Unix秒": expires, "签发身份": args.actor,
                      "签发HTTP": issued, "初始匿名HTTP": direct, "初始摘要一致": same,
                      "签发时剩余秒数": remaining})
        state["步骤"].append({"检查": "授权时签发并匿名下载", "签发HTTP": issued,
                            "匿名HTTP": direct, "摘要一致": same, "剩余秒数": remaining})
        save()
        passed = issued == 302 and same and 3590 <= remaining <= 3600
        print(json.dumps({"模式": args.mode, "通过": passed, "签发HTTP": issued,
                          "匿名HTTP": direct, "剩余秒数": remaining}, ensure_ascii=False))
    elif args.mode == "revoke":
        if "签名URL" not in state:
            parser.error("须先 issue")
        revoked, _, _ = request(base + repo + "/collaborators/" + args.actor, args.owner, "DELETE")
        normal, _, _ = request(endpoint, args.actor)
        signed_code, _, body = request(state["签名URL"])
        same = signed_code == 200 and content_digest(body) == fixture["内容摘要"]
        old_id = str(artifact["id"])
        tampered = state["签名URL"].replace("/" + old_id + "/zip/raw", "/" + str(artifact["id"] + 1) + "/zip/raw")
        tampered_code, _, _ = request(tampered)
        state["步骤"].append({"检查": "撤权后普通新请求及已披露预授权地址", "撤权HTTP": revoked,
                            "普通HTTP": normal, "预授权HTTP": signed_code,
                            "预授权摘要一致": same, "篡改产物ID HTTP": tampered_code})
        save()
        passed = revoked == 204 and normal in (403, 404) and same and tampered_code == 404
        print(json.dumps({"模式": args.mode, "通过": passed, "撤权HTTP": revoked,
                          "普通HTTP": normal, "预授权HTTP": signed_code,
                          "篡改产物ID HTTP": tampered_code}, ensure_ascii=False))
    else:
        if "签名URL" not in state:
            parser.error("须先 issue")
        if time.time() <= state["到期Unix秒"]:
            parser.error("真实到期时刻尚未到；不修改时钟或伪造 expires")
        code, _, _ = request(state["签名URL"])
        state["步骤"].append({"检查": "真实到期后原签名地址新请求", "HTTP": code})
        save()
        passed = code == 401
        print(json.dumps({"模式": args.mode, "通过": passed, "到期后HTTP": code}, ensure_ascii=False))
    if not passed:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
