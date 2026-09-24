#!/usr/bin/env python3
"""在专用真实 Runner 大产物下载流中删除产物，核对新请求和其他对象。"""

import argparse
import base64
import hashlib
import http.client
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
    parser.add_argument("--isolated-instance", action="store_true", required=True)
    parser.add_argument("--credentials", type=Path, required=True)
    parser.add_argument("--fixture", type=Path, required=True, help="artifact_v4.py 建立的专用仓库状态")
    parser.add_argument("--large-state", type=Path, required=True, help="独立大产物 Run/ID 与预期摘要")
    parser.add_argument("--owner", required=True)
    parser.add_argument("--output", type=Path, required=True, help="仅存私有目录")
    args = parser.parse_args()
    fixture = json.loads(args.fixture.read_text())
    large = json.loads(args.large_state.read_text())
    creds = json.loads(args.credentials.read_text())
    base = fixture["实例"].rstrip("/")
    parsed = urllib.parse.urlsplit(base)
    if parsed.scheme != "http" or parsed.hostname not in ("127.0.0.1", "localhost"):
        parser.error("仅允许本机隔离 HTTP 实例")
    if not fixture["仓库"].startswith("a03b-") or large["仓库ID"] != fixture["仓库ID"]:
        parser.error("只操作匹配的 a03b- 专用仓库")
    password = creds.get("password")
    if not isinstance(password, str):
        parser.error("私有凭据需包含 password")
    auth = "Basic " + base64.b64encode((args.owner + ":" + password).encode()).decode()
    repo = "/api/v1/repos/" + fixture["组织"] + "/" + fixture["仓库"]
    artifact_id = large["产物ID"]
    endpoint = repo + "/actions/artifacts/" + str(artifact_id) + "/zip"
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    follow = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    result = {"说明": "真实 Runner 大产物下载流已读取首块后执行同 ID 删除；各阶段立即落私有证据",
              "仓库ID": fixture["仓库ID"], "RunID": large["运行ID"], "产物ID": artifact_id,
              "阶段": [], "通过": False}

    def save(stage):
        result["阶段"].append(stage)
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")

    def request(path, method="GET", signed=False, follow_redirect=False):
        url = path if path.startswith("http") else base + path
        headers = {} if signed else {"Authorization": auth}
        client = follow if follow_redirect else opener
        try:
            with client.open(urllib.request.Request(url, method=method, headers=headers), timeout=30) as response:
                return response.status, response.headers, response.read()
        except urllib.error.HTTPError as error:
            return error.code, error.headers, error.read()

    try:
        if large.get("运行结论") != "success":
            raise RuntimeError("真实 Runner 大产物运行未成功")
        metadata, _, _ = request(repo + "/actions/artifacts/" + str(artifact_id))
        if metadata != 200:
            raise RuntimeError("大产物开始前不可读")
        issued, headers, _ = request(endpoint)
        signed_url = headers.get("Location")
        if issued != 302 or not signed_url:
            raise RuntimeError("未获得原生签名下载地址")
        save({"检查": "删除前产物元数据及签发", "元数据HTTP": metadata, "签发HTTP": issued})

        url = urllib.parse.urlsplit(signed_url)
        conn = http.client.HTTPConnection(url.hostname, url.port, timeout=50)
        conn.request("GET", url.path + "?" + url.query)
        response = conn.getresponse()
        stream_http = response.status
        total_header = response.getheader("Content-Length")
        first = response.read(65536)
        if stream_http != 200 or len(first) != 65536:
            raise RuntimeError("大产物流未读满首块")
        save({"检查": "签名下载流首块", "HTTP": stream_http,
              "已读字节": len(first), "声明总字节": total_header})
        time.sleep(0.15)
        deleted, _, _ = request(repo + "/actions/artifacts/" + str(artifact_id), "DELETE")
        save({"检查": "流未读完时删除同一产物", "HTTP": deleted})
        body = first + response.read()
        conn.close()
        with zipfile.ZipFile(io.BytesIO(body)) as archive:
            content = archive.read("payload-large.bin")
        digest = hashlib.sha256(content).hexdigest()
        same = digest == large["预期原内容SHA256"]
        save({"检查": "已开始的流继续读完", "ZIP字节": len(body),
              "原内容字节": len(content), "原内容SHA256": digest, "摘要一致": same})

        normal, _, _ = request(endpoint)
        old_signed, _, _ = request(signed_url, signed=True)
        keep = next(item for item in fixture["产物"] if item["name"] == "phase1-retain")
        keep_path = repo + "/actions/artifacts/" + str(keep["id"]) + "/zip"
        keep_http, _, keep_body = request(keep_path, follow_redirect=True)
        with zipfile.ZipFile(io.BytesIO(keep_body)) as archive:
            keep_digest = hashlib.sha256(archive.read("payload.txt")).hexdigest()
        keep_same = keep_digest == fixture["内容摘要"]
        save({"检查": "删除后新请求及其他对象", "普通新请求HTTP": normal,
              "旧签名新请求HTTP": old_signed, "其他产物HTTP": keep_http,
              "其他产物摘要一致": keep_same})
        result["通过"] = deleted == 204 and same and normal == 404 and old_signed == 404 and keep_http == 200 and keep_same
    except Exception as error:
        save({"检查": "探针失败", "客户端错误": str(error)})
        raise
    finally:
        args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"通过": result["通过"], "仓库ID": fixture["仓库ID"],
                      "RunID": large["运行ID"], "产物ID": artifact_id}, ensure_ascii=False))
    if not result["通过"]:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
