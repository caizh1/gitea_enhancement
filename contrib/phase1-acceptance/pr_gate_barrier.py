#!/usr/bin/env python3
"""为独立 PR 必选工作流提供可显式放行的本机 HTTP 屏障。"""

import argparse
import http.server
import json
import pathlib
import secrets
import threading
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--isolated-instance", action="store_true", required=True)
    parser.add_argument("--state", required=True, type=pathlib.Path)
    args = parser.parse_args()
    if args.state.exists():
        parser.error("屏障状态文件已存在；使用新文件以免混淆运行")
    token = secrets.token_hex(12)
    reached = threading.Event()
    released = threading.Event()
    state = {"时间": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
             "令牌": token, "已到达": False, "已放行": False}

    def save():
        args.state.parent.mkdir(parents=True, exist_ok=True)
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")

    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path == "/state":
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps({"已到达": reached.is_set(),
                                             "已放行": released.is_set()}, ensure_ascii=False).encode())
                return
            if self.path != "/ready/" + token:
                self.send_error(404)
                return
            reached.set()
            state["已到达"] = True
            save()
            if not released.wait(900):
                self.send_error(504, "屏障在十五分钟内未放行")
                return
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ready\n")

        def do_POST(self):
            if self.path != "/release/" + token:
                self.send_error(404)
                return
            state["已放行"] = True
            save()
            released.set()
            self.send_response(204)
            self.end_headers()

        def log_message(self, *_):
            pass

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    state["端口"] = server.server_address[1]
    save()
    print("本机屏障已监听；端口=" + str(server.server_address[1]), flush=True)
    server.serve_forever(poll_interval=0.2)


if __name__ == "__main__":
    main()
