#!/usr/bin/env python3
"""
Aggregate JUnit XML results from multiple stability test runs and produce
a Markdown stability report.

Usage:
    python3 aggregate-stability.py \
        --results-dir /tmp/stability-results \
        --output /tmp/stability-results/stability-report.md \
        --modes "oc bc dualnicbc"
"""

import argparse
import glob
import os
import sys
import xml.etree.ElementTree as ET
from collections import defaultdict
from datetime import datetime


def parse_junit_xml(filepath):
    """Parse a Ginkgo JUnit XML file and return a list of test results."""
    results = []
    try:
        tree = ET.parse(filepath)
    except ET.ParseError as e:
        print(f"  WARNING: Could not parse {filepath}: {e}", file=sys.stderr)
        return results

    root = tree.getroot()

    # Ginkgo JUnit XML: <testsuites> -> <testsuite> -> <testcase>
    for testsuite in root.iter("testsuite"):
        suite_name = testsuite.get("name", "")
        for testcase in testsuite.findall("testcase"):
            name = testcase.get("name", "unknown")
            classname = testcase.get("classname", "")
            try:
                duration = float(testcase.get("time", "0"))
            except (TypeError, ValueError):
                duration = 0.0

            failure = testcase.find("failure")
            error = testcase.find("error")
            skipped = testcase.find("skipped")

            if skipped is not None:
                status = "skipped"
                message = skipped.get("message", "")
            elif failure is not None:
                status = "failed"
                message = failure.get("message", "")
                body = failure.text or ""
                if body:
                    message = f"{message}\n{body}".strip()
            elif error is not None:
                status = "failed"
                message = error.get("message", "")
                body = error.text or ""
                if body:
                    message = f"{message}\n{body}".strip()
            else:
                status = "passed"
                message = ""

            full_name = f"{classname} :: {name}" if classname else name

            results.append({
                "name": name,
                "full_name": full_name,
                "classname": classname,
                "suite": suite_name,
                "status": status,
                "duration": duration,
                "message": message,
            })

    return results


def collect_run_results(mode_dir):
    """Collect results from all run-NNN directories under a mode dir."""
    runs = {}
    run_dirs = sorted(glob.glob(os.path.join(mode_dir, "run-*")))

    for run_dir in run_dirs:
        run_name = os.path.basename(run_dir)
        xml_files = glob.glob(os.path.join(run_dir, "*.xml"))
        all_results = []
        if not xml_files:
            print(f"  WARNING: No JUnit XML found in {run_dir}", file=sys.stderr)
        else:
            for xml_file in xml_files:
                all_results.extend(parse_junit_xml(xml_file))

        exit_code_file = os.path.join(run_dir, "exit_code")
        exit_code = None
        if os.path.exists(exit_code_file):
            with open(exit_code_file) as f:
                raw = f.read().strip()
            try:
                exit_code = int(raw)
            except ValueError:
                print(f"  WARNING: Invalid exit_code in {exit_code_file}: {raw!r}", file=sys.stderr)

        duration_file = os.path.join(run_dir, "duration_seconds")
        run_duration = None
        if os.path.exists(duration_file):
            with open(duration_file) as f:
                raw = f.read().strip()
            try:
                run_duration = int(raw)
            except ValueError:
                print(f"  WARNING: Invalid duration_seconds in {duration_file}: {raw!r}", file=sys.stderr)

        runs[run_name] = {
            "results": all_results,
            "exit_code": exit_code,
            "duration": run_duration,
        }

    return runs


def categorize(pass_rate):
    """Assign a stability category based on pass rate."""
    if pass_rate >= 1.0:
        return "Stable"
    if pass_rate >= 0.8:
        return "Intermittent"
    if pass_rate >= 0.2:
        return "Flaky"
    return "Broken"


def aggregate_mode(mode, mode_dir):
    """Aggregate results for a single mode across all runs."""
    runs = collect_run_results(mode_dir)
    if not runs:
        return None

    num_runs = len(runs)

    # Build per-test aggregation: track pass/fail/skip counts and durations
    test_stats = defaultdict(lambda: {
        "passes": 0,
        "failures": 0,
        "skips": 0,
        "durations": [],
        "failure_messages": [],
        "classname": "",
    })

    for run_name, run_data in sorted(runs.items()):
        for result in run_data["results"]:
            key = result["full_name"]
            stats = test_stats[key]
            stats["classname"] = result["classname"]

            if result["status"] == "passed":
                stats["passes"] += 1
            elif result["status"] == "failed":
                stats["failures"] += 1
                if result["message"]:
                    stats["failure_messages"].append(
                        f"[{run_name}] {result['message'][:500]}"
                    )
            elif result["status"] == "skipped":
                stats["skips"] += 1

            stats["durations"].append(result["duration"])

    # Compute aggregated metrics
    tests = []
    for full_name, stats in sorted(test_stats.items()):
        total_non_skip = stats["passes"] + stats["failures"]
        if total_non_skip == 0:
            pass_rate = None  # only skipped
        else:
            pass_rate = stats["passes"] / total_non_skip

        avg_duration = (
            sum(stats["durations"]) / len(stats["durations"])
            if stats["durations"]
            else 0
        )

        # Deduplicate failure messages (keep unique ones)
        unique_failures = []
        seen = set()
        for msg in stats["failure_messages"]:
            # Strip run prefix for dedup comparison
            core = msg.split("] ", 1)[-1] if "] " in msg else msg
            if core not in seen:
                seen.add(core)
                unique_failures.append(msg)

        tests.append({
            "full_name": full_name,
            "classname": stats["classname"],
            "passes": stats["passes"],
            "failures": stats["failures"],
            "skips": stats["skips"],
            "pass_rate": pass_rate,
            "category": categorize(pass_rate) if pass_rate is not None else "Skipped",
            "avg_duration": avg_duration,
            "failure_messages": unique_failures,
        })

    # Run-level stats
    run_durations = [
        r["duration"] for r in runs.values() if r["duration"] is not None
    ]
    run_passes = sum(1 for r in runs.values() if r["exit_code"] == 0)

    return {
        "mode": mode,
        "num_runs": num_runs,
        "run_passes": run_passes,
        "run_failures": num_runs - run_passes,
        "avg_run_duration": (
            sum(run_durations) / len(run_durations) if run_durations else 0
        ),
        "tests": tests,
    }


def format_duration(seconds):
    """Format seconds into a readable string."""
    if seconds < 60:
        return f"{seconds:.1f}s"
    minutes = int(seconds // 60)
    secs = seconds % 60
    return f"{minutes}m{secs:.0f}s"


def generate_report(all_mode_results, output_path):
    """Generate a Markdown stability report."""
    lines = []
    now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")

    lines.append("# PTP-Operator Test Stability Report")
    lines.append("")
    lines.append(f"Generated: {now}")
    lines.append("")

    for mode_result in all_mode_results:
        if mode_result is None:
            continue

        if mode_result.get("_missing"):
            lines.append(f"## Mode: `{mode_result['mode']}`")
            lines.append("")
            lines.append("> **No results found.** The test job for this mode did not produce artifacts.")
            lines.append("")
            lines.append("---")
            lines.append("")
            continue

        mode = mode_result["mode"]
        num_runs = mode_result["num_runs"]
        tests = mode_result["tests"]

        # Filter out skipped-only tests for counting
        active_tests = [t for t in tests if t["category"] != "Skipped"]
        stable = sum(1 for t in active_tests if t["category"] == "Stable")
        intermittent = sum(1 for t in active_tests if t["category"] == "Intermittent")
        flaky = sum(1 for t in active_tests if t["category"] == "Flaky")
        broken = sum(1 for t in active_tests if t["category"] == "Broken")
        total = len(active_tests)

        overall_stability = (
            sum(t["passes"] for t in active_tests)
            / sum(t["passes"] + t["failures"] for t in active_tests)
            * 100
            if active_tests
            else 0
        )

        lines.append(f"## Mode: `{mode}`")
        lines.append("")
        lines.append(f"Runs: **{num_runs}** | "
                      f"Suite passed: **{mode_result['run_passes']}/{num_runs}** | "
                      f"Avg run duration: **{format_duration(mode_result['avg_run_duration'])}**")
        lines.append("")

        lines.append("### Summary")
        lines.append("")
        lines.append("| Metric | Value |")
        lines.append("|--------|-------|")
        lines.append(f"| Total tests | {total} |")
        def pct(n, total=total):
            return f"{n/total*100:.0f}%" if total else "0%"
        lines.append(f"| Stable (100%) | {stable} ({pct(stable)}) |")
        lines.append(f"| Intermittent (80-99%) | {intermittent} ({pct(intermittent)}) |")
        lines.append(f"| Flaky (20-79%) | {flaky} ({pct(flaky)}) |")
        lines.append(f"| Broken (<20%) | {broken} ({pct(broken)}) |")
        lines.append(f"| **Overall stability** | **{overall_stability:.1f}%** |")
        lines.append("")

        # Per-test table, sorted worst-first
        lines.append("### Per-Test Results")
        lines.append("")
        lines.append("| Test | Pass Rate | Category | Avg Duration |")
        lines.append("|------|-----------|----------|--------------|")

        sorted_tests = sorted(
            active_tests,
            key=lambda t: (t["pass_rate"] if t["pass_rate"] is not None else 2),
        )

        for t in sorted_tests:
            total_runs = t["passes"] + t["failures"]
            rate_str = f"{t['passes']}/{total_runs}"
            cat_marker = ""
            if t["category"] == "Broken":
                cat_marker = " :red_circle:"
            elif t["category"] == "Flaky":
                cat_marker = " :orange_circle:"
            elif t["category"] == "Intermittent":
                cat_marker = " :yellow_circle:"

            name_display = t["full_name"]
            if len(name_display) > 80:
                name_display = name_display[:77] + "..."

            lines.append(
                f"| {name_display} | {rate_str} | "
                f"{t['category']}{cat_marker} | {format_duration(t['avg_duration'])} |"
            )

        lines.append("")

        # Failure details (only for non-stable tests)
        failing_tests = [t for t in sorted_tests if t["failure_messages"]]
        if failing_tests:
            lines.append("### Failure Details")
            lines.append("")

            for t in failing_tests:
                total_runs = t["passes"] + t["failures"]
                lines.append(
                    f"#### {t['full_name']} "
                    f"({t['passes']}/{total_runs} passed — {t['category']})"
                )
                lines.append("")
                # Show up to 3 unique failure messages
                for msg in t["failure_messages"][:3]:
                    lines.append("```")
                    for line in msg.split("\n")[:20]:
                        lines.append(line)
                    lines.append("```")
                    lines.append("")

                if len(t["failure_messages"]) > 3:
                    lines.append(
                        f"*... and {len(t['failure_messages']) - 3} more unique failure(s)*"
                    )
                    lines.append("")

        lines.append("---")
        lines.append("")

    # Recommendations
    all_active = []
    for mr in all_mode_results:
        if mr:
            all_active.extend(
                (mr["mode"], t) for t in mr["tests"] if t["category"] != "Skipped"
            )

    broken_all = [(m, t) for m, t in all_active if t["category"] == "Broken"]
    flaky_all = [(m, t) for m, t in all_active if t["category"] in ("Flaky", "Intermittent")]

    if broken_all or flaky_all:
        lines.append("## Recommendations")
        lines.append("")
        if broken_all:
            lines.append(f"### Broken tests ({len(broken_all)}) — investigate immediately")
            lines.append("")
            for mode, t in broken_all:
                lines.append(f"- **[{mode}]** {t['full_name']} "
                              f"(passed {t['passes']}/{t['passes']+t['failures']})")
            lines.append("")
        if flaky_all:
            lines.append(f"### Flaky/Intermittent tests ({len(flaky_all)}) — timing or environment issues")
            lines.append("")
            for mode, t in flaky_all:
                lines.append(f"- **[{mode}]** {t['full_name']} "
                              f"(passed {t['passes']}/{t['passes']+t['failures']})")
            lines.append("")

    report = "\n".join(lines) + "\n"

    os.makedirs(os.path.dirname(output_path) or ".", exist_ok=True)
    with open(output_path, "w") as f:
        f.write(report)

    return report


def main():
    parser = argparse.ArgumentParser(
        description="Aggregate ptp-operator stability test results"
    )
    parser.add_argument(
        "--results-dir",
        required=True,
        help="Base directory containing per-mode result directories",
    )
    parser.add_argument(
        "--output",
        required=True,
        help="Path for the generated Markdown report",
    )
    parser.add_argument(
        "--modes",
        required=True,
        help="Space-separated list of modes to aggregate",
    )
    args = parser.parse_args()

    modes = args.modes.split()
    all_results = []

    for mode in modes:
        mode_dir = os.path.join(args.results_dir, mode)
        if not os.path.isdir(mode_dir):
            print(f"WARNING: No results directory for mode '{mode}' at {mode_dir}",
                  file=sys.stderr)
            all_results.append({
                "mode": mode,
                "num_runs": 0,
                "run_passes": 0,
                "run_failures": 0,
                "avg_run_duration": 0,
                "tests": [],
                "_missing": True,
            })
            continue
        print(f"Aggregating results for mode: {mode}")
        result = aggregate_mode(mode, mode_dir)
        all_results.append(result)

    if not any(all_results):
        print("ERROR: No results to aggregate.", file=sys.stderr)
        sys.exit(1)

    report = generate_report(all_results, args.output)
    print(f"\nReport written to: {args.output}")


if __name__ == "__main__":
    main()
