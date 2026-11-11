#!/usr/bin/env python3
"""Implementação Python do benchmark de parsing/agregação.

Equivalente a cmd/benchgo/main.go: mesmas regras de validação, mesmas
agregações e a mesma saída JSON canônica (stdout). Medições vão para stderr.
Usa apenas a biblioteca padrão (json, ipaddress, resource).

    python3 aggregate.py dataset.jsonl
    python3 aggregate.py --noop          # mede só a inicialização
"""
import ipaddress
import json
import platform
import resource
import sys
import time


def is_int(v):
    # Em Python, bool é subclasse de int; o Go rejeitaria true/false.
    return type(v) is int


def is_num(v):
    return type(v) in (int, float)


def main():
    if len(sys.argv) > 1 and sys.argv[1] == "--noop":
        return
    if len(sys.argv) < 2:
        print("uso: aggregate.py dataset.jsonl", file=sys.stderr)
        sys.exit(2)

    start = time.perf_counter()
    valid = invalid = total_bytes = sum_ms = 0
    by_code, endpoints, ips = {}, {}, {}
    loads = json.loads
    parse_ip = ipaddress.ip_address

    with open(sys.argv[1], "rb") as f:
        for raw in f:
            line = raw.rstrip(b"\r\n")
            if not line or line[0] != 0x7B:  # '{'
                invalid += 1
                continue
            try:
                r = loads(line)
            except ValueError:
                invalid += 1
                continue
            if type(r) is not dict:
                invalid += 1
                continue
            ts = r.get("ts")
            ip = r.get("remote_addr")
            method = r.get("method")
            uri = r.get("uri")
            status = r.get("status")
            nbytes = r.get("bytes_sent")
            rt = r.get("request_time")
            if (not is_num(ts) or type(ip) is not str or type(method) is not str
                    or type(uri) is not str or not is_int(status) or not is_int(nbytes)
                    or not is_num(rt) or ts <= 0 or status < 100 or status > 599
                    or nbytes < 0 or rt < 0):
                invalid += 1
                continue
            try:
                parse_ip(ip)
            except ValueError:
                invalid += 1
                continue
            q = uri.find("?")
            if q >= 0:
                uri = uri[:q]
            valid += 1
            total_bytes += nbytes
            sum_ms += int(rt * 1000 + 0.5)
            by_code[status] = by_code.get(status, 0) + 1
            key = method + " " + uri
            endpoints[key] = endpoints.get(key, 0) + 1
            ips[ip] = ips.get(ip, 0) + 1

    def top(m, n=10):
        return [[k, v] for k, v in sorted(m.items(), key=lambda kv: (-kv[1], kv[0]))[:n]]

    classes = {"1xx": 0, "2xx": 0, "3xx": 0, "4xx": 0, "5xx": 0}
    for c, n in by_code.items():
        classes[f"{c // 100}xx"] += n
    result = {
        "events_valid": valid,
        "events_invalid": invalid,
        "bytes_sent": total_bytes,
        "sum_request_ms": sum_ms,
        "classes": classes,
        "by_code": {str(c): n for c, n in by_code.items()},
        "distinct_ips": len(ips),
        "top_endpoints": top(endpoints),
        "top_ips": top(ips),
    }
    elapsed = time.perf_counter() - start
    print(json.dumps(result, sort_keys=True, separators=(",", ":"), ensure_ascii=False))

    ru = resource.getrusage(resource.RUSAGE_SELF)
    events = valid + invalid
    print(json.dumps({
        "lang": "python", "version": platform.python_version(), "workers": 1,
        "events": events, "elapsed_s": round(elapsed, 3),
        "events_per_s": int(events / elapsed),
        "cpu_user_s": round(ru.ru_utime, 3), "cpu_sys_s": round(ru.ru_stime, 3),
        "max_rss_kb": ru.ru_maxrss,
    }, separators=(",", ":")), file=sys.stderr)


if __name__ == "__main__":
    main()
