#!/usr/bin/env python3
"""一期 Hook 隔离接收端；仅记录脱敏投递事实。"""

import argparse
import datetime as dt
import hashlib
import hmac
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import threading


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--work", required=True, type=Path)
    parser.add_argument("--port", required=True, type=int)
    args = parser.parse_args()
    work = args.work.resolve()
    keys = {p.name: p.read_bytes().strip() for p in work.glob("signing-key-*")}
    if not keys:
        raise SystemExit("缺少隔离签名密钥")
    lock = threading.Lock()

    class Receiver(BaseHTTPRequestHandler):
        def do_POST(self):
            payload = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            signature = self.headers.get("X-Gitea-Signature", "")
            matched = [name for name, key in keys.items() if hmac.compare_digest(
                signature, hmac.new(key, payload, hashlib.sha256).hexdigest())]
            try:
                data = json.loads(payload)
            except ValueError:
                data = {}
            status = int((work / "response-status").read_text().strip())
            record = {
                "时间": dt.datetime.now(dt.timezone.utc).isoformat(),
                "事件": self.headers.get("X-Gitea-Event"),
                "投递标识": self.headers.get("X-Gitea-Delivery"),
                "事件标识": self.headers.get("X-Gitea-Event-ID"),
                "事件操作": data.get("action"),
                "仓库编号": (data.get("repository") or {}).get("id"),
                "载荷摘要": hashlib.sha256(payload).hexdigest(),
                "签名密钥标签": matched,
                "响应状态": status,
            }
            with lock, (work / "receipts.jsonl").open("a", encoding="utf-8") as stream:
                stream.write(json.dumps(record, ensure_ascii=False) + "\n")
            self.send_response(status)
            self.end_headers()

        def log_message(self, _format, *_args):
            pass

    print(f"隔离接收端监听 127.0.0.1:{args.port}", flush=True)
    ThreadingHTTPServer(("127.0.0.1", args.port), Receiver).serve_forever()


if __name__ == "__main__":
    main()
