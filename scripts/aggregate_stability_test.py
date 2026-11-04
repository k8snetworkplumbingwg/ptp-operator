#!/usr/bin/env python3
"""Unit tests for aggregate-stability.py classification and gating."""

import os
import sys
import tempfile
import unittest
import xml.etree.ElementTree as ET

# Allow importing the script next to this file (hyphenated module name).
SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, SCRIPT_DIR)

import importlib.util

_spec = importlib.util.spec_from_file_location(
    "aggregate_stability",
    os.path.join(SCRIPT_DIR, "aggregate-stability.py"),
)
agg = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(agg)


def _write_junit(path, cases):
    """cases: list of (name, status) with status in passed|failed|skipped."""
    suite = ET.Element("testsuite", name="Suite")
    for name, status in cases:
        tc = ET.SubElement(suite, "testcase", name=name, classname="C", time="1.0")
        if status == "failed":
            ET.SubElement(tc, "failure", message="boom")
        elif status == "skipped":
            ET.SubElement(tc, "skipped", message="skip")
    root = ET.Element("testsuites")
    root.append(suite)
    ET.ElementTree(root).write(path)


class TestCategorize(unittest.TestCase):
    def test_thresholds(self):
        self.assertEqual(agg.categorize(1.0), "Stable")
        self.assertEqual(agg.categorize(0.8), "Intermittent")
        self.assertEqual(agg.categorize(0.79), "Flaky")
        self.assertEqual(agg.categorize(0.2), "Flaky")
        self.assertEqual(agg.categorize(0.19), "Broken")
        self.assertEqual(agg.categorize(0.0), "Broken")


class TestComputePassRate(unittest.TestCase):
    def test_skips_count_against_rate(self):
        # 1 pass + 4 skips → 20% → would be Flaky, not Stable
        rate = agg.compute_pass_rate(passes=1, failures=0, missing=0, skips=4)
        self.assertAlmostEqual(rate, 0.2)
        self.assertEqual(agg.categorize(rate), "Flaky")

    def test_skip_only_is_none(self):
        self.assertIsNone(
            agg.compute_pass_rate(passes=0, failures=0, missing=0, skips=5)
        )

    def test_missing_xml_counts(self):
        rate = agg.compute_pass_rate(passes=2, failures=0, missing=3, skips=0)
        self.assertAlmostEqual(rate, 0.4)


class TestAggregateAndGate(unittest.TestCase):
    def test_aggregate_mode_with_skips_and_gate(self):
        with tempfile.TemporaryDirectory() as tmp:
            for i, status in enumerate(["passed", "skipped", "skipped"], start=1):
                run = os.path.join(tmp, f"run-{i:03d}")
                os.makedirs(run)
                _write_junit(os.path.join(run, "out.xml"), [("t1", status)])
                with open(os.path.join(run, "exit_code"), "w") as f:
                    f.write("0" if status == "passed" else "0")
                with open(os.path.join(run, "duration_seconds"), "w") as f:
                    f.write("10")

            result = agg.aggregate_mode("oc", tmp)
            self.assertEqual(result["num_runs"], 3)
            self.assertEqual(len(result["tests"]), 1)
            t = result["tests"][0]
            self.assertEqual(t["passes"], 1)
            self.assertEqual(t["skips"], 2)
            self.assertEqual(t["category"], "Flaky")  # 1/3

            ok, reasons = agg.evaluate_gate([result])
            self.assertTrue(ok, reasons)

    def test_gate_fails_on_broken_and_zero_passes(self):
        broken = {
            "mode": "bc",
            "num_runs": 5,
            "run_passes": 0,
            "run_failures": 5,
            "tests": [{
                "full_name": "t",
                "category": "Broken",
                "passes": 0,
                "failures": 5,
                "missing": 0,
                "skips": 0,
            }],
        }
        ok, reasons = agg.evaluate_gate([broken])
        self.assertFalse(ok)
        self.assertTrue(any("0/5 suite passes" in r for r in reasons))
        self.assertTrue(any("Broken" in r for r in reasons))

    def test_gate_fails_on_missing_mode(self):
        ok, reasons = agg.evaluate_gate([{
            "mode": "tgm",
            "num_runs": 0,
            "run_passes": 0,
            "run_failures": 0,
            "tests": [],
            "_missing": True,
        }])
        self.assertFalse(ok)
        self.assertTrue(any("no results" in r for r in reasons))

    def test_empty_mode_dir_is_missing(self):
        with tempfile.TemporaryDirectory() as tmp:
            result = agg.aggregate_mode("oc", tmp)
            self.assertTrue(result.get("_missing"))


if __name__ == "__main__":
    unittest.main()
