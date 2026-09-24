#!/usr/bin/env python3
"""从当前工作树构建可追溯候选；不部署、不覆盖旧产物或运行配置。"""

import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import subprocess


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--output", required=True, type=Path)
    p.add_argument("--go", default="go")
    p.add_argument("--targets", nargs="+", default=["linux/amd64"])
    p.add_argument("--reuse-installed", action="store_true", help="显式复用锁文件完全一致的本地依赖，不允许pnpm自动清理目录")
    a = p.parse_args()
    root = Path(__file__).resolve().parents[2]
    a.output = a.output.resolve()
    a.output.mkdir(parents=True, exist_ok=False)
    env = os.environ.copy()
    env["PATH"] = str(Path(a.go).resolve().parent) + os.pathsep + env["PATH"] if os.path.sep in a.go else env["PATH"]
    tags = "bindata sqlite sqlite_unlock_notify"

    def source_files():
        tracked = subprocess.check_output(["git", "ls-files", "-z"], cwd=root).decode().split("\0")
        added = subprocess.check_output(["git", "ls-files", "--others", "--exclude-standard", "-z"], cwd=root).decode().split("\0")
        return {name: hashlib.sha256((root / name).read_bytes()).hexdigest()
                for name in sorted(set(tracked + added)) if name and (root / name).is_file()
                and name != "gitea" and not name.startswith(("outputs/", "docs/", ".git/"))}

    manifest = {
        "说明": "当前工作树候选，构建成功不等于目标平台运行或发布验收通过",
        "时间UTC": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "HEAD": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
        "Go": subprocess.check_output([a.go, "version"], cwd=root, text=True).strip(),
        "文件": source_files(), "命令": [], "二进制SHA256": {},
    }
    manifest["源码指纹"] = hashlib.sha256(json.dumps(manifest["文件"], sort_keys=True).encode()).hexdigest()
    (a.output / "source-changes.patch").write_bytes(subprocess.check_output(["git", "diff", "--binary"], cwd=root))

    def save():
        (a.output / "source-manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")

    def run(command, extra=None):
        index = len(manifest["命令"])
        with (a.output / f"build-{index}.log").open("w") as log:
            result = subprocess.run(command, cwd=root, env={**env, **(extra or {})}, stdout=log, stderr=subprocess.STDOUT)
        manifest["命令"].append({"命令": command, "环境增量": extra or {}, "退出码": result.returncode})
        save()
        print("构建步骤", index, "退出码", result.returncode, flush=True)
        if result.returncode:
            raise SystemExit(result.returncode)

    frontend_env = {}
    if a.reuse_installed:
        lock = root / "pnpm-lock.yaml"
        installed = root / "node_modules/.pnpm/lock.yaml"
        if not installed.is_file() or lock.read_bytes() != installed.read_bytes():
            raise RuntimeError("本地依赖锁文件与源码不一致，拒绝复用")
        manifest["复用依赖锁SHA256"] = hashlib.sha256(lock.read_bytes()).hexdigest()
        frontend_env["pnpm_config_verify_deps_before_run"] = "warn"
    run(["make", "frontend"], frontend_env)
    run([a.go, "generate", "-tags=bindata", "./modules/templates", "./modules/options", "./modules/public"])
    for target in a.targets:
        system, arch = target.split("/")
        output = a.output / ("gitea-" + system + "-" + arch)
        run([a.go, "build", "-mod=readonly", "-tags=" + tags, "-o", str(output), "."],
            {"GOOS": system, "GOARCH": arch, "CGO_ENABLED": "0"})
        manifest["二进制SHA256"][output.name] = hashlib.sha256(output.read_bytes()).hexdigest()
    manifest["嵌入资源SHA256"] = {name: hashlib.sha256((root / name).read_bytes()).hexdigest()
                                for name in ("modules/templates/bindata.dat", "modules/options/bindata.dat", "modules/public/bindata.dat")}
    manifest["源码构建期间未变化"] = manifest["文件"] == source_files()
    save()
    if not manifest["源码构建期间未变化"]:
        raise RuntimeError("构建期间源码发生变化，保留本次产物但不得当作冻结候选")
    print("候选源码指纹", manifest["源码指纹"])
    print(json.dumps(manifest["二进制SHA256"], ensure_ascii=False))


if __name__ == "__main__":
    main()
