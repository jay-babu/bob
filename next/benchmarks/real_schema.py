#!/usr/bin/env python3
"""Compile matching full-row reads from a private real schema in both designs.

Input schema and generated code stay outside this repository. The baseline uses
the supplied workspace and its pinned Bob version. No source service, module,
workspace or database is modified. Binaries are built, never executed.
"""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import time


def command(args, cwd=None, env=None):
    result = subprocess.run(args, cwd=cwd, env=env, text=True, capture_output=True)
    if result.returncode:
        raise RuntimeError(result.stderr)
    return result.stdout


def module(path):
    while not (path / "go.mod").exists():
        if path == path.parent:
            raise ValueError("module not found")
        path = path.parent
    match = re.search(r"^module\s+(\S+)", (path / "go.mod").read_text(), re.M)
    return path, match.group(1)


def process_memory(pid):
    pending = [pid]
    total = 0
    visited = set()
    while pending:
        current = pending.pop()
        if current in visited:
            continue
        visited.add(current)
        try:
            status = Path(f"/proc/{current}/status").read_text()
            match = re.search(r"^VmRSS:\s+(\d+)", status, re.M)
            total += int(match.group(1)) if match else 0
            for task in Path(f"/proc/{current}/task").iterdir():
                try:
                    pending.extend(int(p) for p in (task / "children").read_text().split())
                except OSError:
                    pass
        except (OSError, ValueError):
            pass
    return total


def measure(folder, env, output, name, parallelism=None, package="."):
    timing = output / f"{name}.time"
    log = output / f"{name}.log"
    args = ["/usr/bin/time", "-f", "%e %M", "-o", str(timing), "go", "build"]
    if parallelism:
        args += ["-p", str(parallelism)]
    args += ["-o", str(folder / "app"), package]
    start = time.monotonic()
    with log.open("w") as stream:
        process = subprocess.Popen(args, cwd=folder, env=env, stdout=stream, stderr=subprocess.STDOUT)
        peak = 0
        while process.poll() is None:
            peak = max(peak, process_memory(process.pid))
            time.sleep(0.05)
    result = {"seconds": time.monotonic() - start, "sampled_process_tree_rss_kib": peak, "exit_code": process.returncode}
    if timing.exists():
        values = timing.read_text().strip().splitlines()
        try:
            elapsed, rss = values[-1].split()
            result.update(seconds=float(elapsed), max_process_rss_kib=int(rss))
        except (ValueError, IndexError):
            pass
    if process.returncode:
        result["error"] = log.read_text() or "build terminated without compiler diagnostics"
    else:
        result["binary_bytes"] = (folder / "app").stat().st_size
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--models", type=Path, required=True)
    parser.add_argument("--legacy-models", type=Path, required=True)
    parser.add_argument("--legacy-workspace", type=Path, required=True)
    parser.add_argument("--compact-module", default="example.org/real")
    parser.add_argument("--out", type=Path)
    parser.add_argument("--parallelism", type=int)
    parser.add_argument("--only", choices=["legacy", "compact"])
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[2]
    source = args.models.resolve()
    old = args.legacy_models.resolve()
    workspace = args.legacy_workspace.resolve()
    storage = Path.home() / ".cache/bob-next-benchmarks"
    storage.mkdir(parents=True, exist_ok=True)
    output = (args.out or Path(tempfile.mkdtemp(prefix="bob-real-build-", dir=storage))).resolve()
    output.mkdir(parents=True, exist_ok=True)
    if any(output.iterdir()):
        raise ValueError("benchmark output must be empty")
    if shutil.disk_usage(output).free < 20 * 1024**3:
        raise ValueError("real-schema builds require at least 20 GiB of disk headroom; avoid a small /tmp filesystem")
    schema = json.loads((source / "schema.json").read_text())
    binding_info = json.loads((source / "go-bindings.json").read_text())
    bindings = binding_info["tables"]
    flat = binding_info["layout"] == "flat"
    old_module_dir, old_module = module(old)
    old_tables = {}
    pattern = re.compile(r'\bvar\s+(\w+)\s*=\s*psql\.New(?:Table|View)x\[.*?\]\(("[^"]*"),\s*("[^"]*")')
    for file in old.rglob("*.go"):
        for name, sql_schema, table in pattern.findall(file.read_text()):
            sql_schema, table = json.loads(sql_schema), json.loads(table)
            if not sql_schema:
                sql_schema = file.relative_to(old).parts[0]
            old_tables[sql_schema + "." + table] = {"path": old_module + "/" + str(file.parent.relative_to(old_module_dir)), "name": name}
    shared = sorted(t["key"] for t in schema["tables"] if t["key"] in old_tables)
    if not shared:
        raise ValueError("no matching table declarations found")
    tables = {t["key"]: t for t in schema["tables"]}
    old_files, new_files = list(old.rglob("*.go")), list(source.rglob("*.go"))
    report = {"logical_objects": len(schema["tables"]), "columns": sum(len(t["columns"]) for t in schema["tables"]), "relationships": len(schema["relations"]), "matching_read_tables": len(shared), "package_parallelism": args.parallelism, "layout": binding_info["layout"], "go": command(["go", "version"]).strip(), "results": {}}
    workspace_info = json.loads(command(["go", "work", "edit", "-json", str(workspace)]))
    original_modules = [(workspace.parent / use["DiskPath"]).resolve() for use in workspace_info["Use"]]
    original_modules = [p for p in original_modules if (p / "go.mod").exists()]
    for mode in ([args.only] if args.only else ["legacy", "compact"]):
        folder = output / mode
        folder.mkdir()
        app_module = "example.org/legacy" if mode == "legacy" else args.compact_module
        (folder / "go.mod").write_text(f"module {app_module}\n\ngo 1.27.1\n")
        uses = original_modules if mode == "legacy" else [root]
        work = output / f"{mode}.work"
        work.write_text("go 1.27.1\nuse (\n" + "\n".join(json.dumps(str(p)) for p in uses + [folder]) + "\n)\n")
        if mode == "compact":
            shutil.copytree(source, folder / "models")
        imports = ['"context"', '"database/sql"', '"fmt"']
        imports.append('runtime "github.com/stephenafamo/bob' + ('/next"' if mode == "compact" else '"'))
        functions = []
        for i, key in enumerate(shared):
            alias = "models" if flat and mode == "compact" else f"m{i}"
            package = old_tables[key]["path"] if mode == "legacy" else bindings[key]["import_path"]
            if not (flat and mode == "compact") or i == 0:
                imports.append(alias + " " + json.dumps(package))
            if mode == "legacy":
                expression = f"r,_ := {alias}.{old_tables[key]['name']}.Query().All(ctx,runtime.NewDB(db));return len(r)"
            else:
                factory = bindings[key]["as"]
                expression = f'c:={alias}.{factory}("r");ctx=runtime.WithExecutor(ctx,db,runtime.ExecutionConfig{{}});r,_:=c.Query().AllContext(ctx);return len(r)'
            functions.append(f"//go:noinline\nfunc read{i}(ctx context.Context,db *sql.DB)int{{{expression}}}")
        calls = ";".join(f"total+=read{i}(ctx,db)" for i in range(len(shared)))
        body = f"func read(ctx context.Context,db *sql.DB)int{{if db==nil{{return 0}};total:=0;{calls};return total}}\nfunc main(){{fmt.Print(read(context.Background(),nil))}}\n"
        (folder / "main.go").write_text("package main\nimport(\n" + "\n".join(imports) + "\n)\n" + "\n".join(functions) + "\n" + body)
        files = old_files if mode == "legacy" else new_files
        results = {"generated_go_files": len(files), "generated_go_bytes": sum(p.stat().st_size for p in files)}
        report["results"][mode] = results
        cache = output / (mode + "-cache")
        cache.mkdir()
        temporary = output / (mode + "-work")
        temporary.mkdir()
        env = dict(os.environ, GOWORK=str(work), GOCACHE=str(cache), GOTMPDIR=str(temporary), TMPDIR=str(temporary), GOTOOLCHAIN="local", GOPROXY="off")
        for phase in ["clean", "warm", "application_edit"]:
            if phase == "application_edit":
                with (folder / "main.go").open("a") as app:
                    app.write("\n// application edit\n")
            results[phase] = measure(folder, env, output, mode + "-" + phase, args.parallelism)
            (output / "results.json").write_text(json.dumps(report, indent=2) + "\n")
            print(mode, phase, results[phase], flush=True)
            if "error" in results[phase]:
                break
    print("Results:", output / "results.json", flush=True)


if __name__ == "__main__":
    main()
