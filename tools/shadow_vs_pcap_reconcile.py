#!/usr/bin/env python3
"""Reconcile shadow-engine decisions against Encombi PCAP write commands.

MVP-2 proof (ems-spec ems_mvp_edge_shadow_spec.md): compares
`cmd/edge -replay` output (would_write_40381) with the actual commands
Encombi sent to the ESS SmartLogger (write_commands.csv from the PCAP
analysis), and reports agreement metrics:

  sign_agreement_pct  - matched pairs with the same sign class
                        (charge / discharge / hold, deadband ±2 kW)
  mae_kw, bias_kw     - magnitude agreement on matched pairs
  lag_seconds         - decision-vs-command lag that maximizes sign
                        agreement (scanned ±lag-scan seconds)
  coverage_pct        - share of PCAP commands with a decision nearby

Usage:
  python3 tools/shadow_vs_pcap_reconcile.py \
      --decisions control_decisions.csv \
      --pcap reference/pcap_analysis/daily/write_commands.csv \
      --register 40381 \
      --out-json reconcile_report.json --out-md reconcile_report.md

Stdlib only (csv/json/datetime); no pandas required.
"""

from __future__ import annotations

import argparse
import csv
import json
import math
import sys
from collections import Counter, defaultdict
from datetime import datetime, timezone

DEADBAND_KW = 2.0


def sign_class(v: float, deadband: float = DEADBAND_KW) -> int:
    if v > deadband:
        return 1
    if v < -deadband:
        return -1
    return 0


def load_decisions(path: str):
    """Returns {unix_second: (value_kw, reason_code)} plus row count."""
    out = {}
    rows = 0
    with open(path, newline="") as f:
        r = csv.DictReader(f)
        if not r.fieldnames or "would_write_40381" not in r.fieldnames:
            raise SystemExit(f"{path}: not a replay decisions CSV (missing would_write_40381)")
        for row in r:
            rows += 1
            ts_raw = (row.get("ts") or "").strip()
            val_raw = (row.get("would_write_40381") or "").strip()
            if not ts_raw or not val_raw:
                continue
            try:
                ts = datetime.fromisoformat(ts_raw.replace("Z", "+00:00"))
                val = float(val_raw)
            except ValueError:
                continue
            sec = int(ts.astimezone(timezone.utc).timestamp())
            out[sec] = (val, (row.get("reason_code") or "").strip())
    return out, rows


def load_pcap_commands(path: str, register: int):
    """Returns [(epoch_float, value_kw)] for one register, sorted."""
    cmds = []
    with open(path, newline="") as f:
        r = csv.DictReader(f)
        for row in r:
            try:
                if int(row["addr"]) != register:
                    continue
                if int(row.get("func", 16)) not in (6, 16):
                    continue
                cmds.append((float(row["epoch"]), float(row["scaled_gain10"])))
            except (KeyError, ValueError):
                continue
    cmds.sort()
    return cmds


def match(decisions, cmds, lag: int, tolerance: float):
    """Pairs each PCAP command with the nearest decision second after
    shifting by `lag`. Returns list of (cmd_kw, dec_kw, reason, epoch)."""
    pairs = []
    max_off = max(1, int(math.ceil(tolerance)))
    for epoch, cmd_kw in cmds:
        target = epoch + lag
        base = round(target)
        best = None
        for off in range(0, max_off + 1):
            for cand in ((base + off,) if off == 0 else (base - off, base + off)):
                if abs(cand - target) > tolerance:
                    continue
                if cand in decisions:
                    best = cand
                    break
            if best is not None:
                break
        if best is None:
            continue
        dec_kw, reason = decisions[best]
        pairs.append((cmd_kw, dec_kw, reason, epoch))
    return pairs


def agreement(pairs, deadband: float):
    if not pairs:
        return 0.0
    ok = sum(1 for c, d, _, _ in pairs if sign_class(c, deadband) == sign_class(d, deadband))
    return 100.0 * ok / len(pairs)


def build_report(decisions, dec_rows, cmds, args):
    # Lag scan: positive lag means the shadow decision trails the
    # Encombi command by that many seconds.
    scan = {}
    for lag in range(-args.lag_scan, args.lag_scan + 1):
        pairs = match(decisions, cmds, lag, args.tolerance)
        scan[lag] = agreement(pairs, args.deadband)
    best_lag = max(scan, key=lambda l: (scan[l], -abs(l)))

    pairs = match(decisions, cmds, best_lag, args.tolerance)
    n = len(pairs)
    coverage = 100.0 * n / len(cmds) if cmds else 0.0
    sign_pct = agreement(pairs, args.deadband)
    mae = sum(abs(d - c) for c, d, _, _ in pairs) / n if n else 0.0
    bias = sum(d - c for c, d, _, _ in pairs) / n if n else 0.0
    rmse = math.sqrt(sum((d - c) ** 2 for c, d, _, _ in pairs) / n) if n else 0.0

    mismatch_reasons = Counter(
        reason or "(none)"
        for c, d, reason, _ in pairs
        if sign_class(c, args.deadband) != sign_class(d, args.deadband)
    )

    per_day = defaultdict(lambda: {"n": 0, "sign_ok": 0, "abs_err": 0.0})
    for c, d, _, epoch in pairs:
        day = datetime.fromtimestamp(epoch, tz=timezone.utc).strftime("%Y-%m-%d")
        bucket = per_day[day]
        bucket["n"] += 1
        bucket["abs_err"] += abs(d - c)
        if sign_class(c, args.deadband) == sign_class(d, args.deadband):
            bucket["sign_ok"] += 1
    days = {
        day: {
            "matched": b["n"],
            "sign_agreement_pct": round(100.0 * b["sign_ok"] / b["n"], 1),
            "mae_kw": round(b["abs_err"] / b["n"], 1),
        }
        for day, b in sorted(per_day.items())
    }

    return {
        "register": args.register,
        "decisions_csv": args.decisions,
        "pcap_csv": args.pcap,
        "decision_rows": dec_rows,
        "decision_seconds": len(decisions),
        "pcap_commands": len(cmds),
        "matched_pairs": n,
        "coverage_pct": round(coverage, 1),
        "sign_agreement_pct": round(sign_pct, 1),
        "mae_kw": round(mae, 1),
        "bias_kw": round(bias, 1),
        "rmse_kw": round(rmse, 1),
        "lag_seconds": best_lag,
        "lag_scan": {str(l): round(a, 1) for l, a in sorted(scan.items())},
        "deadband_kw": args.deadband,
        "tolerance_s": args.tolerance,
        "mismatch_reason_codes": dict(mismatch_reasons.most_common()),
        "per_day": days,
        "notes": args.note or [],
    }


def render_md(rep: dict) -> str:
    lines = [
        f"# Shadow vs PCAP reconcile — register {rep['register']}",
        "",
        f"- Decisions: `{rep['decisions_csv']}` ({rep['decision_rows']} rows, {rep['decision_seconds']} seconds)",
        f"- PCAP commands: `{rep['pcap_csv']}` ({rep['pcap_commands']} writes to {rep['register']})",
        "",
        "## Metrics",
        "",
        "| metric | value |",
        "| --- | ---: |",
        f"| coverage_pct | {rep['coverage_pct']} % |",
        f"| sign_agreement_pct | {rep['sign_agreement_pct']} % |",
        f"| mae_kw | {rep['mae_kw']} kW |",
        f"| bias_kw | {rep['bias_kw']} kW |",
        f"| rmse_kw | {rep['rmse_kw']} kW |",
        f"| lag_seconds | {rep['lag_seconds']} s |",
        f"| matched_pairs | {rep['matched_pairs']} |",
        "",
        f"Sign classes use a ±{rep['deadband_kw']} kW deadband; pairs matched within ±{rep['tolerance_s']} s.",
        "",
        "## Per day",
        "",
        "| day (UTC) | matched | sign_agreement_pct | mae_kw |",
        "| --- | ---: | ---: | ---: |",
    ]
    for day, b in rep["per_day"].items():
        lines.append(f"| {day} | {b['matched']} | {b['sign_agreement_pct']} % | {b['mae_kw']} |")
    if rep["mismatch_reason_codes"]:
        lines += ["", "## Reason codes among sign mismatches", ""]
        for reason, cnt in rep["mismatch_reason_codes"].items():
            lines.append(f"- `{reason}`: {cnt}")
    if rep["notes"]:
        lines += ["", "## Notes", ""]
        for note in rep["notes"]:
            lines.append(f"- {note}")
    lines.append("")
    return "\n".join(lines)


def self_test() -> int:
    """Synthetic sanity check: decisions trail commands by 4 s."""
    base = 1_780_500_000
    cmds = []
    decisions = {}
    for i in range(0, 7200, 3):
        epoch = base + i
        val = 100.0 if (i // 3600) % 2 == 0 else -50.0
        cmds.append((float(epoch), val))
    for sec in range(-40, 7240):
        t = base + sec
        src = t - 4  # decision at t mirrors the command state 4 s earlier
        i = src - base
        val = 100.0 if (i // 3600) % 2 == 0 else -50.0
        decisions[t] = (val + (0.5 if sec % 2 else -0.5), "self_test")

    class A:
        register = 40381
        decisions = "self-test"
        pcap = "self-test"
        lag_scan = 10
        tolerance = 2.0
        deadband = DEADBAND_KW
        note = []

    rep = build_report(decisions, len(decisions), cmds, A)
    ok = (
        rep["lag_seconds"] == 4
        and rep["sign_agreement_pct"] >= 99.0
        and rep["coverage_pct"] >= 99.0
        and rep["mae_kw"] <= 1.0
    )
    print(json.dumps({k: rep[k] for k in (
        "lag_seconds", "sign_agreement_pct", "coverage_pct", "mae_kw")}, indent=2))
    print("SELF-TEST", "PASS" if ok else "FAIL")
    return 0 if ok else 1


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--decisions", help="replay output control_decisions.csv")
    p.add_argument("--pcap", help="write_commands.csv from pcap_analysis")
    p.add_argument("--register", type=int, default=40381)
    p.add_argument("--tolerance", type=float, default=2.0, help="match window, seconds")
    p.add_argument("--lag-scan", type=int, default=30, help="scan ±N seconds for best lag")
    p.add_argument("--deadband", type=float, default=DEADBAND_KW)
    p.add_argument("--replay-input", help="optional replay input CSV to detect synthesized SOC")
    p.add_argument("--note", action="append", help="extra note for the report (repeatable)")
    p.add_argument("--out-json", default="reconcile_report.json")
    p.add_argument("--out-md", default="reconcile_report.md")
    p.add_argument("--self-test", action="store_true")
    args = p.parse_args()

    if args.self_test:
        return self_test()
    if not args.decisions or not args.pcap:
        p.error("--decisions and --pcap are required (or use --self-test)")

    args.note = args.note or []
    if args.replay_input:
        with open(args.replay_input, newline="") as f:
            r = csv.DictReader(f)
            if r.fieldnames and "soc_source" in r.fieldnames:
                sources = {(row.get("soc_source") or "").strip() for row in r}
                if "synth" in sources:
                    args.note.append(
                        "soc_percent was (partly) synthesized from charge/discharge "
                        "counters — SOC-dependent clamps are approximate")

    decisions, dec_rows = load_decisions(args.decisions)
    cmds = load_pcap_commands(args.pcap, args.register)
    if not cmds:
        raise SystemExit(f"no writes to register {args.register} in {args.pcap}")
    if not decisions:
        raise SystemExit(f"no usable decisions in {args.decisions}")

    rep = build_report(decisions, dec_rows, cmds, args)

    with open(args.out_json, "w") as f:
        json.dump(rep, f, indent=2, ensure_ascii=False)
    with open(args.out_md, "w") as f:
        f.write(render_md(rep))

    print(json.dumps({k: rep[k] for k in (
        "pcap_commands", "matched_pairs", "coverage_pct",
        "sign_agreement_pct", "mae_kw", "bias_kw", "lag_seconds")}, indent=2))
    print(f"report: {args.out_json}, {args.out_md}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
