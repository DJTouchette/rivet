#!/usr/bin/env python3
"""Exercise a real isolated `rivet serve` process; never run a project test suite."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile

parser = argparse.ArgumentParser()
parser.add_argument("--binary", required=True)
parser.add_argument("--require-plan", action="store_true", help="require the Witness v0.6.0 plan contract")
args = parser.parse_args()
binary = str(Path(args.binary).resolve())
with tempfile.TemporaryDirectory(prefix="rivet-witness-mcp-") as directory:
    root = Path(directory)
    files = {
        ".rivet/config.yaml": "tools:\n  schema: false\n  vaulty: false\n",
        "native/Cargo.toml": '[package]\nname="native"\nversion="0.1.0"\n',
        "native/src/lib.rs": '#[cfg(test)]\nmod tests { #[test] fn works() {} }\n',
        "web/package.json": '{"devDependencies":{"vitest":"4"}}',
        "web/src/view.ts": 'export const value = 1;\n',
        "web/src/view.test.ts": 'import { value } from "./view";\n',
        "backend/src/Core/Core.csproj": '<Project/>',
        "backend/src/Core/Thing.cs": 'namespace Core; public class Thing {}',
        "backend/checks/Checks.csproj": '<Project><PropertyGroup><IsTestProject>true</IsTestProject></PropertyGroup><ItemGroup><ProjectReference Include="../src/Core/Core.csproj"/></ItemGroup></Project>',
        "backend/checks/ThingTests.cs": 'class ThingTests { [Fact] public void Works() {} }',
    }
    for name, body in files.items():
        p = root / name
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(body)
    subprocess.run(["git", "init", "-q", directory], check=True)
    sentinel = root / "EXECUTED"
    stubs = root / "stubs"
    stubs.mkdir()
    for name in ("cargo", "dotnet", "npm", "npx", "yarn", "pnpm"):
        p = stubs / name
        p.write_text('#!/bin/sh\ntouch "$WITNESS_SENTINEL"\nexit 98\n')
        p.chmod(0o755)
    env = dict(os.environ, PATH=str(stubs) + os.pathsep + os.environ["PATH"], WITNESS_SENTINEL=str(sentinel), RIVET_EMBED_BACKEND="")
    messages = [
        {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"witness-audit","version":"1"}}},
        {"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"witness.select","arguments":{"args":["native/src/lib.rs"]}}},
        {"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"witness.run","arguments":{"args":["native/src/lib.rs","web/src/view.ts","backend/src/Core/Thing.cs"]}}},
    ]
    if args.require_plan:
        messages += [
            {"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"witness.select","arguments":{"args":["--format","plan","native/src/lib.rs","web/src/view.ts","backend/src/Core/Thing.cs"]}}},
            {"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"witness.select","arguments":{"args":["--format","plan","unknown.xyz"]}}},
        ]
    proc = subprocess.run([binary,"serve"],cwd=root,env=env,input="".join(json.dumps(m)+"\n" for m in messages),text=True,capture_output=True,timeout=60)
    assert proc.returncode == 0, proc.stderr
    responses = {x["id"]:x for x in map(json.loads, proc.stdout.splitlines())}
    assert set(responses) == {m["id"] for m in messages}, proc.stdout
    def result(number):
        r = responses[number]["result"]
        text = "".join(c.get("text","") for c in r["content"])
        assert text.strip(), r
        return r,text
    _,text = result(2)
    selection = json.loads(text.split("\n--- stderr ---\n")[0])
    assert isinstance(selection["tests"],list), selection
    _,commands = result(3)
    assert commands.strip()
    if args.require_plan:
        r,text = result(4)
        assert not r.get("isError"), text
        plan = json.loads(text.split("\n--- stderr ---\n")[0])
        assert plan["schema_version"] == 1 and plan["status"] == "ready", plan
        actual = {(c["cwd"],tuple(c["argv"])) for c in plan["commands"]}
        expected = {
            (".",("cargo","test","--manifest-path","./native/Cargo.toml")),
            (".",("dotnet","test","./backend/checks/Checks.csproj")),
            ("web",("npm","exec","--no","--","vitest","run")),
        }
        assert actual == expected, actual
        r,text = result(5)
        assert r.get("isError"), text
        assert json.loads(text.split("\n--- stderr ---\n")[0])["status"] == "incomplete", text
    assert not sentinel.exists(), "planning executed a runner/package manager"
    print(json.dumps({"mcp":"pass","calls":len(messages)-1,"plan_required":args.require_plan,"runner_executed":False}))
