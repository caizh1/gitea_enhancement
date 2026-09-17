#!/usr/bin/env python3
"""在独立 SQLite 和 PostgreSQL 数据库执行治理基础回归，保存中文结果。"""

import argparse
import hashlib
import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import time
import uuid


def run(args, *, env=None, check=True):
    result = subprocess.run(args, cwd=ROOT, env=env, text=True, capture_output=True)
    if check and result.returncode:
        raise RuntimeError(f"命令执行失败：{args[0]}\n{result.stderr[-4000:]}")
    return result


def source_digest():
    paths = run(["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"]).stdout.split("\0")
    digest = hashlib.sha256()
    for relative in sorted(set(paths)):
        path = ROOT / relative
        if not relative or not path.is_file() or path.suffix != ".go" and path.name not in ("go.mod", "go.sum"):
            continue
        digest.update(relative.encode())
        digest.update(b"\0")
        digest.update(path.read_bytes())
    return digest.hexdigest()


def test_database(go, name, env, integration_pattern=None):
    print(f"正在执行{name}治理基础回归……", flush=True)
    arguments = [go, "test", "-json", "-count=1"]
    if integration_pattern:
        arguments.extend(["-tags", "bindata", "./tests/integration", "-run", integration_pattern])
    else:
        arguments.append("./models/governance")
    result = run(arguments, env=env, check=False)
    cases, errors, outputs = [], [], {}
    for line in result.stdout.splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            errors.append(line)
            continue
        if event.get("Test") and event.get("Action") in ("pass", "fail", "skip"):
            cases.append({"用例": event["Test"], "结果": {"pass": "通过", "fail": "失败", "skip": "跳过"}[event["Action"]], "秒数": event.get("Elapsed")})
            if event["Action"] == "fail":
                errors.extend(outputs.get(event["Test"], []))
        if event.get("Action") == "output" and event.get("Test"):
            outputs.setdefault(event["Test"], []).append(event.get("Output", "").rstrip())
    return {"数据库": name, "退出码": result.returncode, "用例": cases,
            "输出摘要校验": hashlib.sha256((result.stdout + result.stderr).encode()).hexdigest(),
            "失败原始证据": errors, "诊断": (result.stdout[-12000:] + result.stderr[-4000:]) if result.returncode else ""}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", default=shutil.which("go"), help="Go 1.26.4 可执行文件")
    parser.add_argument("--postgres-image", default="postgres:17.11-bookworm", help="隔离验收使用的 PostgreSQL 镜像")
    parser.add_argument("--integration-pattern", help="改为验证指定原生集成用例；需要本机架构的当前二进制")
    parser.add_argument("--report", type=Path, help="中文 JSON 报告路径，默认保存到治理验收记录目录")
    args = parser.parse_args()
    if not args.go:
        parser.error("未找到 Go，请用 --go 指定已核对的 Go 1.26.4 工具链")
    if not (ROOT / "go.mod").read_text().startswith("module gitea.dev"):
        parser.error("当前目录不是目标 Gitea 源码")
    name = "gitea-governance-check-" + uuid.uuid4().hex[:12]
    report = {"任务": "原生治理基础回归", "UTC时间": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
              "源码校验": source_digest(), "工具链": run([args.go, "version"]).stdout.strip(),
              "范围说明": "只验证治理基础模型，不代表原生网页、HTTP/SSH Git、升级、容量或生产验收通过。", "数据库结果": []}
    created = False
    temporary_config = None
    try:
        env = os.environ.copy()
        env.pop("GOVERNANCE_TEST_POSTGRES_DSN", None)
        if args.integration_pattern:
            env["GITEA_TEST_DATABASE"] = "sqlite"
            report["范围说明"] = "执行指定原生集成用例，不代表完整功能、容量或生产验收通过。"
            report["集成用例筛选"] = args.integration_pattern
        report["数据库结果"].append(test_database(args.go, "SQLite", env, args.integration_pattern))
        print("正在启动本次专用 PostgreSQL 容器……", flush=True)
        run(["docker", "run", "--detach", "--platform", "linux/amd64", "--name", name,
             "--label", "codex.task=gitea-native-governance", "--publish", "127.0.0.1::5432",
             "--env", "POSTGRES_USER=governance_test", "--env", "POSTGRES_DB=governance_test",
             "--env", "POSTGRES_HOST_AUTH_METHOD=trust", args.postgres_image])
        created = True
        info = json.loads(run(["docker", "inspect", name]).stdout)[0]
        report["PostgreSQL镜像校验"] = info["Image"]
        binding = info["NetworkSettings"]["Ports"]["5432/tcp"][0]
        if binding["HostIp"] != "127.0.0.1":
            raise RuntimeError("验收数据库未限制为本机访问")
        deadline = time.monotonic() + 45
        # 官方初始化临时服务器只监听 Unix socket；TCP 就绪才代表最终服务已启动。
        while run(["docker", "exec", name, "pg_isready", "--host=127.0.0.1", "--username=governance_test"], check=False).returncode:
            if time.monotonic() >= deadline:
                raise RuntimeError("专用 PostgreSQL 在 45 秒内未就绪")
            time.sleep(0.5)
        env["GOVERNANCE_TEST_POSTGRES_DSN"] = f"host=127.0.0.1 port={binding['HostPort']} user=governance_test dbname=governance_test sslmode=disable"
        if args.integration_pattern:
            config_name = "governance-pgsql-"+uuid.uuid4().hex[:12]
            temporary_config = ROOT / "tests" / (config_name+".ini")
            template = (ROOT / "tests/sqlite.ini.tmpl").read_text()
            database_section = "[database]\nDB_TYPE = postgres\nHOST = {{TEST_PGSQL_HOST}}\nNAME = governance_test_integration\nUSER = governance_test\nSSL_MODE = disable\nSCHEMA = public\n\n"
            template, replacements = re.subn(r"(?ms)^\[database\]\n.*?(?=^\[)", lambda _:database_section, template, count=1)
            if replacements != 1:
                raise RuntimeError("无法生成隔离 PostgreSQL 本地存储配置")
            temporary_config.with_suffix(".ini.tmpl").write_text(template)
            env.update({"GITEA_TEST_DATABASE":config_name, "TEST_PGSQL_HOST":"127.0.0.1:"+binding["HostPort"]})
        report["数据库结果"].append(test_database(args.go, "PostgreSQL", env, args.integration_pattern))
    except Exception as error:
        report["执行错误"] = str(error)
    finally:
        if temporary_config:
            temporary_config.unlink(missing_ok=True)
            temporary_config.with_suffix(".ini.tmpl").unlink(missing_ok=True)
        if created:
            cleanup = run(["docker", "rm", "--force", "--volumes", name], check=False)
            report["专用容器清理"] = "完成" if cleanup.returncode == 0 else "失败，需要核对本次容器"
        report["测试期间源码变化"] = source_digest() != report["源码校验"]
        passed = ("执行错误" not in report and not report["测试期间源码变化"] and len(report["数据库结果"]) == 2
                  and all(item["退出码"] == 0 and item["用例"] for item in report["数据库结果"]))
        report["基础回归结论"] = "通过" if passed else "失败"
        report["整项实施结论"] = "未完成"
        target = args.report or ROOT / "治理验收记录" / (time.strftime("%Y%m%d-%H%M%S") + "-基础回归.json")
        target.parent.mkdir(parents=True, exist_ok=True)
        with target.open("x", encoding="utf-8") as output:
            json.dump(report, output, ensure_ascii=False, indent=2)
            output.write("\n")
        print(f"基础回归{report['基础回归结论']}；报告：{target}", flush=True)
    return 0 if passed else 1


ROOT = Path(__file__).resolve().parents[1]

if __name__ == "__main__":
    raise SystemExit(main())
