#!/usr/bin/env python3
"""Analyze leader counter stdout to compute window-based AE/verify metrics.

Usage: analyze.py <leader-stdout> [--summary <loadkv-out>]

Parses countersReport lines from the leader's stdout (printed once per second).
The measurement load window is the contiguous run of counter lines where
VerifyDone grows (the load). Computes:
  VerifyDone, VerifyWaited (last line in window)
  ratio = VerifyWaited / VerifyDone
  AESent_start, AESent_end (first/last line in window)
  window_s = number of counter lines in the window
  AE_per_peer_per_s = (AESent_end - AESent_start) / 2 / window_s
  verifyRedispatched / verifyRedispatchSuppressed (last line, if present)
"""
import re, sys

LINE_RE = re.compile(
    r"VerifyDone=(\d+)\s+VerifyWaited=(\d+)\s+AESent=(\d+)"
    r"(?:\s+VerifyRedispatched=(\d+)\s+VerifyRedispatchSuppressed=(\d+))?")

def main():
    stdout_path = sys.argv[1]
    rows = []
    with open(stdout_path) as f:
        for line in f:
            m = LINE_RE.search(line)
            if m:
                vd = int(m.group(1)); vw = int(m.group(2)); ae = int(m.group(3))
                vr = int(m.group(4)) if m.group(4) else None
                vs = int(m.group(5)) if m.group(5) else None
                rows.append((vd, vw, ae, vr, vs))
    if not rows:
        print("NO COUNTER ROWS"); return
    max_vd = rows[-1][0]
    # window: from first line with VerifyDone>2% of max to the last line
    start = 0
    for i, (vd, *_ ) in enumerate(rows):
        if vd > 0.02 * max_vd:
            start = i
            break
    end = len(rows) - 1
    # trim trailing lines after VerifyDone stops growing (post-load)
    for i in range(len(rows)-1, start-1, -1):
        if rows[i][0] < max_vd or i == len(rows)-1:
            end = i
            break
    vd_s, vw_s, ae_s, *_ = rows[start]
    vd_e, vw_e, ae_e, vr_e, vs_e = rows[end]
    window_s = end - start + 1
    d_ae = ae_e - ae_s
    ae_per_peer = d_ae / 2.0 / window_s
    ratio = vw_e / vd_e * 100.0 if vd_e else 0.0
    print(f"window_lines={window_s} (idx {start}..{end} of {len(rows)})")
    print(f"VerifyDone={vd_e} VerifyWaited={vw_e} ratio={ratio:.3f}%")
    print(f"AESent_start={ae_s} AESent_end={ae_e} dAESent={d_ae}")
    print(f"AE_per_peer_per_s={ae_per_peer:.2f}")
    print(f"verifyRedispatched={vr_e} verifyRedispatchSuppressed={vs_e}")

if __name__ == "__main__":
    main()
