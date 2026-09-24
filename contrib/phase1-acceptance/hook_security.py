#!/usr/bin/env python3
"""在隔离实例验证 Hook 出站主机限制和自签 TLS 拒绝。"""

import argparse
import base64
import json
import os
from pathlib import Path
import socket
import sqlite3
import subprocess
import sys
import time

from capacity_probe import API, write_json


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--credentials", required=True, type=Path)
    parser.add_argument("--work", required=True, type=Path)
    parser.add_argument("--sqlite-db", required=True, type=Path)
    parser.add_argument("--tls-port", required=True, type=int)
    parser.add_argument("--isolated-instance", action="store_true", required=True)
    args = parser.parse_args()
    state_path = args.work / "state.json"
    state = json.loads(state_path.read_text())
    if args.base.rstrip("/") != state["实例"].rstrip("/") or "移出结果" not in state:
        raise RuntimeError("隔离实例或移出夹具不匹配")
    password = json.loads(args.credentials.read_text())["password"]
    api = API(args.base, state["根Owner"], password)
    result_path = args.work / "security.json"
    if result_path.exists():
        raise RuntimeError("安全验证已有原始结果，不得覆盖")
    work = args.work / "security"
    work.mkdir(mode=0o700, exist_ok=False)
    cert, key = work / "cert.pem", work / "key.pem"
    with (work / "certificate-generation.log").open("w") as output:
        created = subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                                  "-days", "1", "-keyout", str(key), "-out", str(cert),
                                  "-subj", "/CN=127.0.0.1", "-addext", "subjectAltName=IP:127.0.0.1"],
                                 stdout=output, stderr=subprocess.STDOUT, timeout=30)
    if created.returncode:
        raise RuntimeError("本机自签证书生成失败")
    os.chmod(key, 0o600)
    log = work / "requests.log"
    receiver = Path(__file__).with_name("hook_tls_receiver.py")
    with (work / "receiver.log").open("w") as output:
        server = subprocess.Popen([sys.executable, str(receiver), "--cert", str(cert), "--key", str(key),
                                   "--log", str(log), "--port", str(args.tls_port)],
                                  stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
    (work / "receiver.pid").write_text(str(server.pid) + "\n")
    try:
        for _ in range(30):
            try:
                with socket.create_connection(("127.0.0.1", args.tls_port), timeout=0.2):
                    break
            except OSError:
                time.sleep(0.1)
        else:
            raise TimeoutError("自签 HTTPS 接收端未就绪")
        group = state["移出目标群组"]["compatibility_name"]
        repo = state["移出结果"]["新仓库"]
        hooks = {}
        for label, url in (("host", f"http://127.0.0.2:{args.tls_port}/blocked"),
                           ("tls", f"https://127.0.0.1:{args.tls_port}/self-signed")):
            path = f"/orgs/{group}/hooks"
            code, value, _, _ = api.call("POST", path, {"type": "gitea", "name": "一期隔离安全" + label,
                                                 "config": {"url": url, "content_type": "json"},
                                                 "events": ["push"], "active": True})
            if code != 201:
                raise RuntimeError(f"{label} 测试 Hook 创建返回 {code}")
            hooks[label] = value["id"]
            write_json(result_path, {"测试Hook": hooks, "阶段": "已创建；后续失败现场保留"})
        marker = str(time.time_ns())
        path = f"/repos/{repo}/contents/hook-security-{marker}.txt"
        code, _, _, _ = api.call("PUT", path, {"branch": state["仓库"]["default_branch"],
                                            "message": "验证一期 Hook 出站限制",
                                            "content": base64.b64encode(marker.encode()).decode()})
        if code != 201:
            raise RuntimeError(f"隔离 push 返回 {code}")
        database = sqlite3.connect("file:" + str(args.sqlite_db.resolve()) + "?mode=ro", uri=True)
        results = {}
        for _ in range(60):
            for label, hook_id in hooks.items():
                row = database.execute("SELECT is_delivered, is_succeed, response_content FROM hook_task "
                                       "WHERE hook_id = ? ORDER BY id DESC LIMIT 1", (hook_id,)).fetchone()
                if not row or not row[0]:
                    continue
                response = json.loads(row[2]) if row[2] else {}
                body = response.get("body", "")
                results[label] = {"已处理": bool(row[0]), "成功": bool(row[1]),
                                  "主机限制拒绝": "can only call allowed HTTP servers" in body,
                                  "证书拒绝": "x509:" in body}
            if len(results) == len(hooks):
                break
            time.sleep(0.25)
        else:
            raise TimeoutError("安全测试 Hook 任务未全部处理")
        requests = log.read_text().splitlines() if log.exists() else []
        result = {"候选实例": args.base, "仓库编号": state["仓库"]["id"], "测试Hook": hooks,
                  "结果": results, "自签接收端HTTP次数": len(requests)}
        write_json(result_path, result)
        if results["host"]["成功"] or not results["host"]["主机限制拒绝"] or results["tls"]["成功"] \
                or not results["tls"]["证书拒绝"] or requests:
            raise RuntimeError("主机限制或 TLS 自签拒绝不符合预期；保留失败现场")
        for hook_id in hooks.values():
            code, _, _, _ = api.call("DELETE", f"/orgs/{group}/hooks/{hook_id}")
            if code != 204:
                raise RuntimeError(f"安全测试 Hook {hook_id} 清理返回 {code}")
        result["测试Hook已删除"] = True
        write_json(result_path, result)
        print("主机 allow-list 与自签 TLS 均拒绝；接收端零 HTTP；临时 Hook 已删除")
    finally:
        server.terminate()
        try:
            server.wait(timeout=5)
        except subprocess.TimeoutExpired:
            server.kill()
            server.wait(timeout=5)


if __name__ == "__main__":
    main()
