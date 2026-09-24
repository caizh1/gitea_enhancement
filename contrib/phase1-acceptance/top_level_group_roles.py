#!/usr/bin/env python3
"""用独立账号验收 M01-a 顶级群组创建权限和原生组织同步。"""

import argparse
import json
import pathlib
import secrets
import subprocess
import sys
import time

from job_token_boundary import Client


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--binary", type=pathlib.Path, required=True)
    parser.add_argument("--work-path", type=pathlib.Path, required=True)
    parser.add_argument("--config", type=pathlib.Path, required=True)
    parser.add_argument("--private", type=pathlib.Path, required=True)
    parser.add_argument("--state", type=pathlib.Path, required=True)
    args = parser.parse_args()
    if args.state.exists() or args.private.exists():
        raise RuntimeError("结果或凭据文件已存在；不得覆盖既有夹具")
    if args.private.resolve().parent != args.state.resolve().parent:
        raise RuntimeError("凭据与结果应放在同一独立私有目录")
    args.state.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    suffix = format(time.time_ns(), "x")[-11:]
    names = {role: "p1m01-" + role + "-" + suffix for role in ("admin", "yes", "no")}
    passwords = {name: secrets.token_urlsafe(27) for name in names.values()}
    state = {"说明": "M01-a 独立账号顶级群组创建权限验收", "步骤": [], "账号": names,
             "运行入口": args.base.rstrip("/"), "临时管理员已停用": False}

    def save():
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")

    def check(label, actual, expected):
        passed = actual == expected
        state["步骤"].append({"检查": label, "实际": actual, "期望": expected, "通过": passed})
        save()
        if not passed:
            raise AssertionError(f"{label}：实际 {actual}，期望 {expected}")

    def call(api, label, path, method="GET", body=None, user=None, expected=200):
        code, result = api.call(path, method, body, user)
        check(label, code, expected)
        return result

    admin = names["admin"]
    try:
        command = [str(args.binary), "--work-path", str(args.work_path), "--config", str(args.config),
                   "admin", "user", "create", "--username", admin,
                   "--email", admin + "@example.invalid", "--password", passwords[admin],
                   "--admin", "--must-change-password=false"]
        process = subprocess.run(command, capture_output=True, text=True, timeout=90)
        check("正式 CLI 创建专属临时管理员", process.returncode, 0)
        args.private.write_text(json.dumps(passwords, ensure_ascii=False, indent=2) + "\n")
        args.private.chmod(0o600)
        api = Client(args.base, args.private, admin)
        admin_profile = call(api, "临时管理员身份", "/api/v1/user", user=admin)
        check("临时账号为管理员", admin_profile.get("is_admin"), True)

        for role in ("yes", "no"):
            name = names[role]
            created = call(api, "正式 API 新建普通账号 " + role, "/api/v1/admin/users", "POST",
                           {"username": name, "email": name + "@example.invalid",
                            "password": passwords[name], "must_change_password": False,
                            "restricted": False}, expected=201)
            check("普通账号非实例管理员 " + role, created.get("is_admin"), False)
            profile = call(api, "普通账号可登录 " + role, "/api/v1/user", user=name)
            check("普通账号登录身份非管理员 " + role, profile.get("is_admin"), False)
            state.setdefault("账号ID", {})[role] = profile["id"]
            save()

        call(api, "正式管理员接口关闭无权账号顶级创建", "/api/v1/admin/users/" + names["no"],
             "PATCH", {"allow_create_organization": False})
        group_path = "p1m01-group-" + suffix
        denied_path = "p1m01-denied-" + suffix
        body = {"name": "顶级群组角色边界验收", "path": group_path,
                "parent_id": 0, "visibility": 2}
        group = call(api, "有权普通账号创建私有顶级群组", "/api/v1/governance/groups",
                     "POST", body, user=names["yes"], expected=201)
        check("群组父级为顶级", group.get("parent_id"), 0)
        state["群组"] = {key: group[key] for key in ("id", "compatibility_name", "parent_id", "revision")}
        state["群组"]["path"] = group_path
        save()
        group_id = group["id"]
        fresh = call(api, "刷新后治理群组仍可读", f"/api/v1/governance/groups/{group_id}",
                     user=names["yes"])
        check("刷新后群组ID保持", fresh.get("id"), group_id)
        native = call(api, "原生组织 API 与治理群组同步", "/api/v1/orgs/" + group["compatibility_name"],
                      user=names["yes"])
        check("原生组织 ID 与治理群组相同", native.get("id"), group_id)
        own_groups = call(api, "有权账号顶级列表", "/api/v1/governance/groups?parent_id=0",
                          user=names["yes"])
        check("有权账号列表包含新群组", any(item.get("id") == group_id for item in own_groups), True)

        denied = dict(body, path=denied_path)
        call(api, "无权账号创建顶级群组被隐藏", "/api/v1/governance/groups", "POST",
             denied, user=names["no"], expected=404)
        call(api, "无权账号读取私有群组被隐藏", f"/api/v1/governance/groups/{group_id}",
             user=names["no"], expected=404)
        call(api, "无权账号读取原生私有组织被隐藏", "/api/v1/orgs/" + group["compatibility_name"],
             user=names["no"], expected=404)
        denied_groups = call(api, "无权账号顶级列表", "/api/v1/governance/groups?parent_id=0",
                             user=names["no"])
        check("无权账号列表不泄露新群组", any(item.get("id") == group_id for item in denied_groups), False)
        call(api, "被拒创建的原生组织不存在", "/api/v1/orgs/" + denied_path,
             user=names["yes"], expected=404)
        state["UI待核"] = {
            "有权账号": names["yes"], "无权账号": names["no"],
            "入口": args.base.rstrip("/") + "/governance/groups?create=1",
            "群组": args.base.rstrip("/") + "/" + group_path,
            "无权原生创建入口": args.base.rstrip("/") + "/org/create",
            "断言": "有权账号可见私有顶级群组、刷新后仍在原生组织；无权账号创建被拒并且私有群组404"}
        save()

        call(api, "停用专属临时管理员", "/api/v1/admin/users/" + admin, "PATCH",
             {"active": False, "admin": False, "prohibit_login": True})
        state["临时管理员已停用"] = True
        save()
        code, _ = api.call("/api/v1/user", user=admin)
        check("停用后临时管理员凭据拒绝", code in (401, 403), True)
        state["结果"] = "API全断言通过；等待原生UI人工核对"
    except Exception as error:
        state["结果"] = "停止"
        state["首错"] = str(error)
        raise
    finally:
        save()
    print("M01-a API 通过，群组 ID", state["群组"]["id"], "；原生 UI 待核")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("停止：" + str(error), file=sys.stderr)
        sys.exit(1)
