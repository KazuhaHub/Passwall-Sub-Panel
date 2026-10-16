"""Read-only CPU diagnostics for the owned 65-Node traffic profile.

The resource window is a subset of traffic, separate from acceptance. Process
coordinates and commands stay private; only counters and quality flags leave
this helper. It never signals a process or changes its scheduling.
"""

import argparse
import json
import os
from pathlib import Path, PurePosixPath
import subprocess
import time


ROOT_TEST = "^TestNativeDestinationGlobalBatchPressure$"
MAX_SAFE = (1 << 53) - 1


class ScopeError(ValueError):
    def __init__(self, code):
        self.code = code
        super().__init__("owned process scope")


def process_stat(text):
    end = text.rfind(")")
    if end < 0 or "(" not in text[:end]:
        raise ValueError("process stat")
    fields = text[end + 1:].split()
    if len(fields) < 20:
        raise ValueError("process fields")
    parent, user, system, birth = (int(fields[i]) for i in (1, 11, 12, 19))
    if min(parent, user, system) < 0 or birth <= 0 or user + system > MAX_SAFE:
        raise ValueError("process counters")
    return parent, user + system, birth


def option(args, name):
    positions = [i for i, value in enumerate(args) if value == name]
    if len(positions) != 1 or positions[0] + 1 >= len(args):
        raise ValueError("process option")
    return args[positions[0] + 1]


def core_command(args, data):
    base = PurePosixPath(data)
    return (base.is_absolute() and str(base) == data and ".." not in base.parts and args == [
        str(base / "cores/xray/versions/26.6.27/xray"), "run", "-config",
        str(base / "runtime/xray/current.json"),
    ])


def file_identity(path):
    value = Path(path).stat()
    return value.st_dev, value.st_ino


class Proc:
    def __init__(self, root=Path("/proc")):
        self.root = root
        self.names = {}

    def stat(self, pid):
        return process_stat((self.root / str(pid) / "stat").read_text()[:8192])

    def exe(self, pid):
        return file_identity(self.root / str(pid) / "exe")

    def args(self, pid):
        raw = (self.root / str(pid) / "cmdline").read_bytes()
        if not raw or len(raw) > 8192 or not raw.endswith(b"\0"):
            raise ValueError("process command")
        return raw[:-1].decode("utf-8").split("\0")

    def rows(self):
        rows = {}
        self.names = {}
        for entry in self.root.iterdir():
            if not entry.name.isdecimal():
                continue
            try:
                raw = (entry / "stat").read_text()[:8192]
                rows[int(entry.name)] = process_stat(raw)
                self.names[int(entry.name)] = raw[raw.index("(") + 1:raw.rfind(")")]
            except (OSError, ValueError, UnicodeError):
                continue
        return rows

    def test_candidate(self, pid):
        # Linux comm is limited to 15 bytes. This only narrows discovery;
        # executable identity and the exact test root remain authoritative.
        return self.names.get(pid) == "destination-global-batch.test"[:15]


def bind_processes(proc, test_identity, node_identity):
    rows = proc.rows()
    mains = []
    for pid in rows:
        if not proc.test_candidate(pid):
            continue
        try:
            if proc.exe(pid) == test_identity and option(proc.args(pid), "-test.run") == ROOT_TEST:
                mains.append(pid)
        except (OSError, ValueError, UnicodeError):
            continue
    if len(mains) != 1:
        raise ScopeError(10)
    main = mains[0]
    identities = {main: test_identity}
    nodes, cores, directories = [], [], set()
    for pid, (parent, _, _) in sorted(rows.items()):
        if parent != main:
            continue
        if proc.exe(pid) != node_identity:
            continue
        identities[pid] = node_identity
        data = option(proc.args(pid), "--data-dir")
        directory = PurePosixPath(data)
        if not directory.is_absolute() or str(directory) != data or ".." in directory.parts or data in directories:
            raise ScopeError(11)
        directories.add(data)
        children = []
        for child, (owner, _, _) in rows.items():
            if owner == pid and core_command(proc.args(child), data):
                expected = file_identity(Path(data) / "cores/xray/versions/26.6.27/xray")
                if proc.exe(child) != expected:
                    raise ScopeError(12)
                identities[child] = expected
                children.append(child)
        if len(children) != 1:
            raise ScopeError(13)
        nodes.append(pid)
        cores.append(children[0])
    if len(nodes) != 65 or len(set(cores)) != 65:
        raise ScopeError(14)
    # Reuse only the executable coordinates just checked in this discovery
    # call. counters() immediately revalidates every identity at the baseline
    # and each subsequent sample; no process observation survives a run.
    bindings = [(pid, rows[pid][0], rows[pid][2], identities[pid]) for pid in [main] + nodes + cores]
    if len({row[0] for row in bindings}) != 131:
        raise ScopeError(15)
    return bindings


def counters(proc, bindings, previous=None):
    if previous is not None and len(previous) != len(bindings):
        raise ScopeError(16)
    values = []
    for i, (pid, parent, birth, executable) in enumerate(bindings):
        current_parent, cpu, current_birth = proc.stat(pid)
        if current_parent != parent or current_birth != birth or proc.exe(pid) != executable:
            raise ScopeError(17)
        if previous is not None and cpu < previous[i]:
            raise ScopeError(18)
        values.append(cpu)
    return values


def sample(elapsed, values, initial):
    delta = [value - base for value, base in zip(values, initial)]
    if len(delta) != 131 or any(value < 0 or value > MAX_SAFE for value in delta):
        raise ValueError("owned resource delta")
    return {"elapsed_ms": elapsed, "panel_cpu_ticks": delta[0],
            "node_cpu_ticks": delta[1:66], "core_cpu_ticks": delta[66:]}


def cpu_budget(text):
    quota, period = text.split()
    unlimited = quota == "max"
    period = int(period)
    quota = 0 if unlimited else int(quota)
    if period <= 0 or (quota <= 0 and not unlimited) or max(period, quota) > MAX_SAFE:
        raise ValueError("resource CPU budget")
    return {"available": 1, "limited": int(quota > 0), "quota_us": quota, "period_us": period}


def cgroup_limits(proc, main):
    # Optional context, never an inferred unlimited budget when unavailable.
    result = {"available": 0, "limited": 0, "quota_us": 0, "period_us": 0}
    try:
        lines = (proc.root / str(main) / "cgroup").read_text().splitlines()
        group, = [line[3:] for line in lines if line.startswith("0::")]
        if not group.startswith("/") or ".." in Path(group).parts:
            return result
        mount = Path("/sys/fs/cgroup").resolve(strict=True)
        directory = (mount / group.lstrip("/")).resolve(strict=True)
        directory.relative_to(mount)
        return cpu_budget((directory / "cpu.max").read_text())
    except (OSError, ValueError):
        return result


def report(hz):
    return {"schema_version": 1, "traffic_subset": 1, "complete": 0, "reason": 0, "detail": 0,
            "clock_ticks_per_second": hz, "phase_poll_bound_ms": 0,
            "window_limit_ms": 118000, "binding_ms": 0, "affinity_cpus": 0,
            "cgroup": {"available": 0, "limited": 0, "quota_us": 0, "period_us": 0},
            "samples": []}


def run(root):
    output = root / "destination-global-process-resources.json"
    stop = root / "destination-global-process-resources.stop"
    phase = root / "destination-global-setup.cpu.pprof"
    phase_end = root / "destination-global-setup.alloc-after.pprof"
    hz = 0
    try:
        value = subprocess.run(["getconf", "CLK_TCK"], check=True, capture_output=True, timeout=2).stdout
        hz = int(value.strip())
        if not 0 < hz <= 1000000:
            hz = 0
    except (OSError, ValueError, subprocess.SubprocessError):
        pass
    data = report(hz)
    # The helper owns this one new file; raw coordinates never enter it.
    with output.open("x", encoding="utf-8") as stream:
        json.dump(data, stream)

    def save():
        with output.open("w", encoding="utf-8") as stream:
            json.dump(data, stream, separators=(",", ":"))

    try:
        if phase.exists() or phase.is_symlink() or phase_end.exists() or stop.exists():
            data["reason"] = 1
            return
        waiting = time.monotonic()
        last_absent = waiting
        while not phase.exists():
            if stop.exists() or time.monotonic() - waiting > 1320:
                data["reason"] = 2
                return
            last_absent = time.monotonic()
            time.sleep(0.25)
        if phase.is_symlink() or not phase.is_file():
            data["reason"] = 3
            return
        observed = time.monotonic()
        data["phase_poll_bound_ms"] = max(1, int((observed - last_absent) * 1000) + 1)
        phase_identity = file_identity(phase)
        proc = Proc()
        bindings = bind_processes(proc, file_identity(root / "destination-global-batch.test"), file_identity(root / "destination-node"))
        initial = counters(proc, bindings)
        previous = initial
        data["binding_ms"] = int((time.monotonic() - observed) * 1000)
        if data["phase_poll_bound_ms"] > 500 or data["binding_ms"] > 500 or not hz:
            data["reason"] = 4
            return
        data["cgroup"] = cgroup_limits(proc, bindings[0][0])
        data["affinity_cpus"] = len(os.sched_getaffinity(bindings[0][0]))
        data["samples"].append(sample(0, initial, initial))
        started, next_sample = time.monotonic(), 5.0
        while True:
            elapsed = time.monotonic() - started
            if stop.exists() or phase_end.exists():
                data["reason"] = 5
                return
            if file_identity(phase) != phase_identity:
                data["reason"] = 6
                return
            if elapsed >= next_sample or elapsed >= 118:
                current = counters(proc, bindings, previous)
                if phase_end.exists():
                    data["reason"] = 5
                    return
                measured_ms = int((time.monotonic() - started) * 1000)
                if measured_ms > 118750:
                    data["reason"] = 8
                    return
                data["samples"].append(sample(measured_ms, current, initial))
                previous = current
                save()
                next_sample += 5
                if elapsed >= 118:
                    data["complete"] = 1
                    return
            time.sleep(0.25)
    except ScopeError as error:
        data["reason"], data["detail"] = 7, error.code
    except (OSError, ValueError, UnicodeError):
        data["reason"] = 7
        data["detail"] = 99
    finally:
        save()


def validate_report(data):
    if not isinstance(data, dict) or set(data) != set(report(0)):
        raise ValueError("resource schema")
    for key in set(data) - {"cgroup", "samples"}:
        if type(data[key]) is not int or not 0 <= data[key] <= MAX_SAFE:
            raise ValueError("resource number")
    if data["schema_version"] != 1 or data["traffic_subset"] != 1 or data["complete"] not in (0, 1) or data["reason"] not in range(9) or data["window_limit_ms"] != 118000:
        raise ValueError("resource scope")
    if data["detail"] not in (0, 10, 11, 12, 13, 14, 15, 16, 17, 18, 99) or (data["detail"] and data["reason"] != 7):
        raise ValueError("resource failure detail")
    group = data["cgroup"]
    if not isinstance(group, dict) or set(group) != {"available", "limited", "quota_us", "period_us"} or any(type(v) is not int or not 0 <= v <= MAX_SAFE for v in group.values()):
        raise ValueError("resource cgroup")
    if group["available"] not in (0, 1) or group["limited"] not in (0, 1):
        raise ValueError("resource cgroup flags")
    if (not group["available"] and any(group.values())) or (group["available"] and (not group["period_us"] or group["limited"] != int(group["quota_us"] > 0))):
        raise ValueError("resource cgroup budget")
    samples = data["samples"]
    if not isinstance(samples, list) or len(samples) > 26:
        raise ValueError("resource sample cardinality")
    previous = None
    for row in samples:
        if not isinstance(row, dict) or set(row) != {"elapsed_ms", "panel_cpu_ticks", "node_cpu_ticks", "core_cpu_ticks"}:
            raise ValueError("resource sample shape")
        if type(row["elapsed_ms"]) is not int or not 0 <= row["elapsed_ms"] <= 118750:
            raise ValueError("resource sample time")
        for key in ("node_cpu_ticks", "core_cpu_ticks"):
            if not isinstance(row[key], list) or len(row[key]) != 65:
                raise ValueError("resource cohort size")
        values = [row["panel_cpu_ticks"]] + row["node_cpu_ticks"] + row["core_cpu_ticks"]
        if any(type(v) is not int or not 0 <= v <= MAX_SAFE for v in values) or sum(values) > MAX_SAFE:
            raise ValueError("resource CPU values")
        if previous is None:
            if row["elapsed_ms"] != 0 or any(values):
                raise ValueError("resource baseline")
        elif row["elapsed_ms"] <= previous[0] or any(v < old for v, old in zip(values, previous[1])):
            raise ValueError("resource counter order")
        previous = row["elapsed_ms"], values
    if data["complete"] and (data["reason"] != 0 or not 0 < data["clock_ticks_per_second"] <= 1000000 or not 0 < data["affinity_cpus"] <= 4096 or not 0 < data["phase_poll_bound_ms"] <= 500 or data["binding_ms"] > 500 or len(samples) < 20 or samples[-1]["elapsed_ms"] < 118000):
        raise ValueError("resource complete window")
    return {"complete": data["complete"], "reason": data["reason"], "detail": data["detail"], "samples": len(samples),
            "elapsed_ms": previous[0] if previous else 0,
            "clock_ticks_per_second": data["clock_ticks_per_second"],
            "affinity_cpus": data["affinity_cpus"],
            "panel_cpu_ticks": samples[-1]["panel_cpu_ticks"] if samples else 0,
            "node_cpu_ticks": sum(samples[-1]["node_cpu_ticks"]) if samples else 0,
            "core_cpu_ticks": sum(samples[-1]["core_cpu_ticks"]) if samples else 0,
            "cgroup": group}


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True)
    parser.add_argument("--summary", action="store_true")
    args = parser.parse_args()
    owned = Path(os.environ["RUNNER_TEMP"]).resolve(strict=True)
    if os.environ.get("GITHUB_ACTIONS") != "true" or Path(args.root).resolve(strict=True) != owned:
        raise SystemExit("owned hosted resource scope required")
    if args.summary:
        resource = owned / "destination-global-process-resources.json"
        if resource.is_symlink() or resource.stat().st_size > 100000:
            raise SystemExit("invalid resource file")
        print(json.dumps(validate_report(json.loads(resource.read_text()))))
    else:
        run(owned)
