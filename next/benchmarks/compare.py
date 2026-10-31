#!/usr/bin/env python3
"""Reproducible build experiment; output is deliberately outside the checkout.

The fixture has a referenced owner and N-1 referencing tables. Both consumers
select complete owner rows. This is not a feature-equivalence benchmark: the
legacy output includes navigation helpers and compact output uses explicit loads.
Peak RSS is GNU time's maximum process RSS, not aggregate concurrent build memory.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time


def run(command, cwd, env=None):
    result = subprocess.run(command, cwd=cwd, env=env, text=True, capture_output=True)
    if result.returncode:
        raise RuntimeError(result.stdout + result.stderr)
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--tables", type=int, default=150)
    parser.add_argument("--columns", type=int, default=16)
    parser.add_argument("--out", type=Path)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[2]
    out = args.out or Path(tempfile.mkdtemp(prefix="bob-next-compare-"))
    out = out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    tool = out / "generate-comparison"
    run(["go", "build", "-o", str(tool), "./next/cmd/compare"], root)
    generate = [str(tool), "-out", str(out), "-bob", str(root), "-tables", str(args.tables), "-columns", str(args.columns)]
    start = time.monotonic()
    run(generate, root)
    report = {"tables": args.tables, "columns": args.columns, "generation_seconds_both": time.monotonic() - start, "go": run(["go", "version"], root).stdout.strip(), "results": {}}

    def build(mode, phase):
        folder = out / mode
        cache = out / (mode + "-cache")
        cache.mkdir(exist_ok=True)
        env = dict(os.environ, GOWORK="off", GOCACHE=str(cache), GOTOOLCHAIN="local")
        timing = out / (mode + "-" + phase + ".time")
        command = ["/usr/bin/time", "-f", "%e %M", "-o", str(timing), "go", "build", "-mod=mod", "-o", str(folder / "app"), "."]
        try:
            run(command, folder, env)
            elapsed, rss = timing.read_text().strip().split()
            report["results"][mode][phase] = {"seconds": float(elapsed), "max_process_rss_kib": int(rss), "binary_bytes": (folder / "app").stat().st_size}
        except RuntimeError as error:
            report["results"][mode][phase] = {"error": str(error)}
        (out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
        print(mode, phase, report["results"][mode][phase], flush=True)

    for mode in ["legacy", "compact"]:
        files = list((out / mode / "models").rglob("*.go"))
        report["results"][mode] = {"generated_go_files": len(files), "generated_go_bytes": sum(p.stat().st_size for p in files)}
        build(mode, "clean")
        if "error" in report["results"][mode]["clean"]:
            continue
        build(mode, "warm")
        with (out / mode / "main.go").open("a") as source:
            source.write("\n// application-only edit\n")
        build(mode, "application_edit")
    run(generate + ["-change"], root)
    for mode in ["legacy", "compact"]:
        if "error" not in report["results"][mode]["clean"]:
            build(mode, "unrelated_schema_edit")
    print("Results:", out / "results.json", flush=True)


if __name__ == "__main__":
    main()
