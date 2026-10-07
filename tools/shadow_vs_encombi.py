#!/usr/bin/env python3
"""Hourly shadow plan vs Encombi fact on ze (docs/reports/shadow_vs_encombi_2026-09.md).

Inputs (CSV, no header), exported from the VM:
  hourly.csv: kyiv "YYYY-MM-DD HH", plan_kw, shadow_kw, fact_kw, load_kw, pv_kw, grid_kw, n
              (hourly averages of control_decisions, site ze)
  dam.csv:    "YYYY-MM-DD", hour 1..24, price_uah_per_mwh (zone 2)

  docker exec deploy-timescaledb-1 psql -U postgres -d telemetry -At -F"," -c "
    SELECT to_char(h AT TIME ZONE 'Europe/Kyiv', 'YYYY-MM-DD HH24'),
           round(plan,1), round(shadow,1), round(fact,1), round(load,1), round(pv,1), round(grid,1), n
    FROM (SELECT time_bucket('1 hour', time) AS h,
                 avg((record->'inputs'->>'p_bess_plan_kw')::numeric) AS plan,
                 avg(p_bess_virtual_kw)::numeric AS shadow,
                 avg((record->'inputs'->>'ess_power_kw')::numeric) AS fact,
                 avg((record->'inputs'->>'load_power_kw')::numeric) AS load,
                 avg((record->'inputs'->>'pv_power_kw')::numeric) AS pv,
                 avg((record->'inputs'->>'grid_power_kw')::numeric) AS grid,
                 count(*) AS n
          FROM control_decisions WHERE site_id = 'ze' AND time >= '2026-09-04T12:00Z'
          GROUP BY 1) s ORDER BY h" > hourly.csv
  ... -c "SELECT to_char(delivery_date, 'YYYY-MM-DD'), hour, price_uah_per_mwh
          FROM market_dam_prices WHERE zone = 2 AND delivery_date >= '2026-09-04' ORDER BY 1, 2" > dam.csv

Two corrections against the September draft:
  * control_decisions.inputs.ess_power_kw on ze carries the wrong sign
    (edge ess_discharge_sign: -1); the fact is −ess_power_kw.
  * load_power_kw is 40503 of the PV SmartLogger = 40505 + PV; it does
    not see the BESS on the other logger. Plant load = load_kw + fact.
"""

import argparse
import csv
import statistics


def load_rows(hourly_path, dam_path, start, end, fixed):
    prices = {}
    for row in csv.reader(open(dam_path)):
        if len(row) != 3:
            continue
        prices[(row[0], int(row[1]) - 1)] = float(row[2]) / 1000

    rows = []
    for row in csv.reader(open(hourly_path)):
        if len(row) != 8:
            continue
        dh, plan, shadow, fact, load, pv, grid, n = row
        d, hh = dh.split(" ")
        if not (start <= d <= end):
            continue
        fact = float(fact or 0)
        load = float(load or 0)
        # 40503 = 40505 + PV holds to the watt except in hours with
        # garbage 40505/40503 samples (hundreds of MW); drop those.
        if abs(load - (float(grid or 0) + float(pv or 0))) > 2:
            continue
        if fixed:
            fact = -fact
            load = load + fact
        rows.append(dict(
            d=d, h=int(hh), shadow=float(shadow or 0), fact=fact, load=load,
            pv=float(pv or 0), grid=float(grid or 0), n=int(n),
            price=prices.get((d, int(hh))),
        ))
    return [r for r in rows if r["n"] >= 3000]


def report(full):
    out = {}
    act = [r for r in full if abs(r["shadow"]) > 10 or abs(r["fact"]) > 10]
    both = [r for r in act if abs(r["shadow"]) > 10 and abs(r["fact"]) > 10]
    out["hours"] = len(full)
    out["sign_any"] = (sum(r["shadow"] * r["fact"] > 0 for r in act), len(act))
    out["sign_both"] = (sum(r["shadow"] * r["fact"] > 0 for r in both), len(both))

    xs = [r["shadow"] for r in full]
    ys = [r["fact"] for r in full]
    mx, my = statistics.mean(xs), statistics.mean(ys)
    cov = sum((x - mx) * (y - my) for x, y in zip(xs, ys))
    out["corr"] = cov / ((sum((x - mx) ** 2 for x in xs) * sum((y - my) ** 2 for y in ys)) ** 0.5)

    exp_hours = []
    for r in full:
        deficit = max(0.0, r["load"] - r["pv"])
        r["deficit"] = deficit
        r["exp_kw"] = max(0.0, r["shadow"] - deficit)
        if r["exp_kw"] > 50:
            exp_hours.append(r)
    out["exp_hours"] = len(exp_hours)
    out["exp_fact"] = (
        sum(r["fact"] > 10 for r in exp_hours),
        sum(r["fact"] < -10 for r in exp_hours),
    )

    eve = [r for r in full if 18 <= r["h"] <= 22]
    out["eve_plan_dis"] = sum(max(0, r["shadow"]) for r in eve) / 1000
    out["eve_fact_dis"] = sum(max(0, r["fact"]) for r in eve) / 1000
    out["eve_fact_chg"] = sum(max(0, -r["fact"]) for r in eve) / 1000
    out["eve_deficit"] = sum(r["deficit"] for r in eve) / 1000

    noon = [r for r in full if 9 <= r["h"] <= 15]
    out["noon_fact_chg"] = sum(max(0, -r["fact"]) for r in noon) / 1000
    out["noon_fact_dis"] = sum(max(0, r["fact"]) for r in noon) / 1000

    lost = 0.0
    for r in exp_hours:
        if r["price"] is None:
            continue
        delivered = max(0.0, r["fact"] - r["deficit"])
        lost += max(0.0, r["exp_kw"] - delivered) * r["price"] * 0.95
    out["lost_uah"] = lost
    out["enc_export_hours"] = sum(r["fact"] - r["deficit"] > 50 for r in full)
    out["top"] = sorted(exp_hours, key=lambda r: -(r["exp_kw"] - r["fact"]))[:8]
    return out


def show(title, o):
    print(f"== {title}: {o['hours']} full hours")
    a, n = o["sign_any"]
    b, m = o["sign_both"]
    print(f"  sign match any-active {a}/{n} = {100 * a / max(n, 1):.0f}%, both-active {b}/{m} = {100 * b / max(m, 1):.0f}%")
    print(f"  hourly corr shadow~fact {o['corr']:+.2f}")
    print(f"  plan export hours (>50 kW) {o['exp_hours']}: fact discharging {o['exp_fact'][0]}, charging {o['exp_fact'][1]}")
    print(f"  evening 18-22 MWh: plan discharge {o['eve_plan_dis']:.1f}, fact discharge {o['eve_fact_dis']:.2f}, "
          f"fact charge {o['eve_fact_chg']:.2f}, plant deficit {o['eve_deficit']:.1f}")
    print(f"  midday 09-15 MWh: fact charge {o['noon_fact_chg']:.2f}, fact discharge {o['noon_fact_dis']:.2f}")
    print(f"  unrealised export revenue (model, DAM-5%): {o['lost_uah']:,.0f} UAH")
    print(f"  hours Encombi exported >50 kW above deficit: {o['enc_export_hours']}")
    for r in o["top"]:
        print(f"    {r['d']} {r['h']:02d}:00 price {r['price'] or 0:5.2f} shadow {r['shadow']:+5.0f} "
              f"(export {r['exp_kw']:4.0f}) fact {r['fact']:+5.0f} plant load {r['load']:4.0f}")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--hourly", default="/tmp/sve/hourly.csv")
    ap.add_argument("--dam", default="/tmp/sve/dam.csv")
    ap.add_argument("--start", default="2026-09-04")
    ap.add_argument("--end", default="2026-09-17")
    args = ap.parse_args()
    show("as published (inverted sign, PV-logger load)",
         report(load_rows(args.hourly, args.dam, args.start, args.end, fixed=False)))
    show("corrected (fact = −ess_power_kw, load = 40503 + fact)",
         report(load_rows(args.hourly, args.dam, args.start, args.end, fixed=True)))


if __name__ == "__main__":
    main()
