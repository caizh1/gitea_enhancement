#!/usr/bin/env python3
"""隔离自签 HTTPS 接收端；仅记录完成 TLS 握手的 HTTP 请求次数。"""

import argparse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import ssl


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cert", required=True, type=Path)
    parser.add_argument("--key", required=True, type=Path)
    parser.add_argument("--log", required=True, type=Path)
    parser.add_argument("--port", required=True, type=int)
    args = parser.parse_args()

    class Receiver(BaseHTTPRequestHandler):
        def do_POST(self):
            with args.log.open("a") as output:
                output.write("已收到完成 TLS 握手的 HTTP 请求\n")
            self.send_response(200)
            self.end_headers()

        def log_message(self, _format, *_args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", args.port), Receiver)
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(str(args.cert), str(args.key))
    server.socket = context.wrap_socket(server.socket, server_side=True)
    print("隔离自签 HTTPS 接收端已监听", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
