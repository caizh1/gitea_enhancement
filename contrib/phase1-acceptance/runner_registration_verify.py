#!/usr/bin/env python3
"""验收独立群组 UI 轮换后的旧令牌拒绝、新注册及撤权披露。"""

import argparse
import hashlib
import json
import pathlib
import subprocess
import sys

from job_token_boundary import Client


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--credentials", type=pathlib.Path, required=True)
    parser.add_argument("--fixture", type=pathlib.Path, required=True)
    parser.add_argument("--runner-binary", type=pathlib.Path, required=True)
    parser.add_argument("--state", type=pathlib.Path, required=True)
    args = parser.parse_args()
    if args.state.exists():
        raise RuntimeError("结果已存在；不得覆盖轮换证据")
    args.state.parent.mkdir(parents=True, exist_ok=True)
    fixture = json.loads(args.fixture.read_text())
    api = Client(args.base, args.credentials, fixture["identities"]["owner"])
    old = json.loads((args.fixture.parent / "registration-token-before.json").read_text())["token"]
    group = fixture["group"]
    state = {"说明": "S13-a UI轮换后正式Runner注册和Owner撤权披露", "groupID": group["id"], "步骤": []}

    def save():
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")

    def call(label, path, method="GET", body=None, user=None, expected=200):
        code, result = api.call(path, method, body, user)
        state["步骤"].append({"检查": label, "实际": code, "期望": expected, "通过": code == expected})
        save()
        if code != expected:
            raise AssertionError(f"{label}：HTTP {code}")
        return result

    def register(label, token, should_succeed):
        directory = args.state.parent / label
        directory.mkdir(mode=0o700, exist_ok=False)
        config = directory / "runner.yaml"
        config.write_text(f"runner:\n  file: {directory}/.runner\n  labels:\n    - s13-registration:host\ncache:\n  enabled: false\n")
        config.chmod(0o600)
        name = "phase1-s13-" + label + "-" + str(group["id"])
        log = directory / "registration.log"
        with log.open("w") as output:
            result = subprocess.run([str(args.runner_binary), "register", "--no-interactive", "--config", str(config),
                "--instance", args.base, "--token", token, "--name", name,
                "--labels", "s13-registration:host"], stdout=output, stderr=subprocess.STDOUT, timeout=30)
        passed = (result.returncode == 0) == should_succeed
        state["步骤"].append({"检查": label + "注册结果", "实际退出码": result.returncode,
            "期望成功": should_succeed, "通过": passed})
        save()
        if not passed:
            raise AssertionError(label + "注册退出码 " + str(result.returncode))
        return name

    try:
        rotated = call("轮换后源 Owner 读取当前注册令牌", fixture["tokenAPI"], "POST")["token"]
        new_hash = hashlib.sha256(rotated.encode()).hexdigest()
        if new_hash == fixture["tokenBeforeSHA256"] or old == rotated:
            raise AssertionError("UI 轮换后令牌未改变")
        secret = args.state.parent / "registration-token-after.json"
        secret.write_text(json.dumps({"token": rotated}) + "\n")
        secret.chmod(0o600)
        state["tokenBeforeSHA256"] = fixture["tokenBeforeSHA256"]
        state["tokenAfterSHA256"] = new_hash
        save()
        old_name = register("old", old, False)
        new_name = register("new", rotated, True)
        listing = call("回读独立群组 Runner", "/api/v1/orgs/" + group["compatibility_name"] + "/actions/runners")
        found = [runner for runner in listing.get("runners", listing.get("entries", [])) if runner["name"] == new_name]
        if len(found) != 1 or any(runner["name"] == old_name for runner in listing.get("runners", listing.get("entries", []))):
            raise AssertionError("新旧 Runner 注册列表不符")
        state["registeredRunnerID"] = found[0]["id"]
        current = call("读取独立群组修订", f"/api/v1/governance/groups/{group['id']}")
        call("撤销临时 Owner", f"/api/v1/governance/groups/{group['id']}/members/{fixture['temporaryOwnerID']}?revision={current['revision']}",
            "DELETE", expected=204)
        code, result = api.call(fixture["tokenAPI"], "POST", user=fixture["identities"]["temporaryOwner"])
        if code != 403 or isinstance(result, dict) and "token" in result:
            raise AssertionError("撤权后注册令牌披露未拒绝")
        state["撤权后临时Owner读取"] = {"HTTP": code, "含令牌字段": False}
        still = call("源 Owner 撤权后仍可读取", fixture["tokenAPI"], "POST")["token"]
        if hashlib.sha256(still.encode()).hexdigest() != new_hash:
            raise AssertionError("撤临时 Owner 意外改变有效注册令牌")
        state["临时OwnerUI"] = fixture["runnerUI"]
        state["结果"] = "通过"
    except Exception as error:
        state["结果"], state["首错"] = "失败", str(error)
        raise
    finally:
        save()
    print("通过：群组", group["id"], "新Runner", state["registeredRunnerID"], "撤权后HTTP", state["撤权后临时Owner读取"]["HTTP"])


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("停止：" + str(error), file=sys.stderr)
        sys.exit(1)
