#!/usr/bin/env python3
"""Reject failed, cancelled, missing, or unexpectedly skipped CI jobs."""
import json
import os


def main():
    required = os.environ.get("FULL_REQUIRED")
    results = {name: os.environ.get(name + "_RESULT", "") for name in ("QUICK", "SECURITY", "FULL")}
    passed = (required in ("true", "false")
              and results["QUICK"] == results["SECURITY"] == "success"
              and (results["FULL"] == "success"
                   or (required == "false" and results["FULL"] == "skipped")))
    print(json.dumps({"passed": passed, "full_required": required, "results": results}))
    return 0 if passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
