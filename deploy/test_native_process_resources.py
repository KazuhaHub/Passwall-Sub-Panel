import copy
import json
from pathlib import Path, PurePosixPath
import tempfile
import unittest
from unittest.mock import patch

import native_process_resources as resource


class OwnedProc:
    def __init__(self):
        self.records = {41: (1, 100, 4001)}
        self.executables = {41: (1, 1)}
        self.commands = {41: ["/owned/test", "-test.run", resource.ROOT_TEST]}
        self.paths = {}
        self.exe_reads = []
        self.candidate_ids = {41}
        for i in range(65):
            node, core = 1000 + i, 2000 + i
            data = f"/owned/private-state/{i}/node"
            binary = str(PurePosixPath(data) / "cores/xray/versions/26.6.27/xray")
            self.records[node] = (41, 200 + i, 5000 + i)
            self.records[core] = (node, 300 + i, 6000 + i)
            self.executables[node], self.executables[core] = (2, 2), (3, core)
            self.commands[node] = ["/owned/node", "--data-dir", data, "--credential-file", "/private-secret"]
            self.commands[core] = [binary, "run", "-config", str(PurePosixPath(data) / "runtime/xray/current.json")]
            self.paths[str(Path(binary))] = (3, core)

    def rows(self):
        return dict(self.records)

    def stat(self, pid):
        return self.records[pid]

    def exe(self, pid):
        self.exe_reads.append(pid)
        return self.executables[pid]

    def args(self, pid):
        return self.commands[pid]

    def test_candidate(self, pid):
        return pid in self.candidate_ids


class NativeResources(unittest.TestCase):
    def bind(self, proc):
        with patch.object(resource, "file_identity", lambda path: proc.paths[str(path)]):
            return resource.bind_processes(proc, (1, 1), (2, 2))

    def test_stat_has_exact_cpu_and_birth_fields(self):
        fields = "S 7 1 1 0 -1 0 0 0 0 0 17 13 999 999 0 0 2 0 456".split()
        build = lambda values: "42 (private (core) name) " + " ".join(values)
        self.assertEqual(resource.process_stat(build(fields)), (7, 30, 456))
        for index, value in [(1, "-1"), (11, "-1"), (12, "private"), (19, "0"), (11, str(resource.MAX_SAFE))]:
            changed = fields[:]
            changed[index] = value
            with self.subTest(index=index, value=value), self.assertRaises(ValueError):
                resource.process_stat(build(changed))
        for text in ["", "42 no name", "42 (core) S 1"]:
            with self.subTest(text=text), self.assertRaises(ValueError):
                resource.process_stat(text)

    def test_bind_requires_original_test_and_65_independent_children(self):
        proc = OwnedProc()
        bindings = self.bind(proc)
        self.assertEqual(len(bindings), 131)
        self.assertEqual(len(proc.exe_reads), 131)
        self.assertEqual(len(set(proc.exe_reads)), 131)
        initial = resource.counters(proc, bindings)
        for pid, (parent, cpu, birth) in list(proc.records.items()):
            proc.records[pid] = parent, cpu + 7, birth
        current = resource.counters(proc, bindings, initial)
        data = resource.sample(5000, current, initial)
        self.assertEqual(data["panel_cpu_ticks"], 7)
        self.assertEqual(data["node_cpu_ticks"], [7] * 65)
        self.assertEqual(data["core_cpu_ticks"], [7] * 65)
        encoded = json.dumps(data)
        self.assertNotIn("private", encoded)
        self.assertEqual(set(data), {"elapsed_ms", "panel_cpu_ticks", "node_cpu_ticks", "core_cpu_ticks"})

    def test_proc_discovery_uses_comm_only_as_a_filter(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fields = "S 7 1 1 0 -1 0 0 0 0 0 17 13 0 0 0 0 2 0 456"
            for pid, name in [(41, "destination-glo"), (42, "unrelated"), (43, "fake (core)")]:
                (root / str(pid)).mkdir()
                (root / str(pid) / "stat").write_text(f"{pid} ({name}) {fields}")
            proc = resource.Proc(root)
            rows = proc.rows()
            self.assertEqual(set(rows), {41, 42, 43})
            self.assertTrue(proc.test_candidate(41))
            self.assertFalse(proc.test_candidate(42))
            self.assertFalse(proc.test_candidate(43))
        proc = OwnedProc()
        proc.executables[41] = (9, 9)
        with self.assertRaises(resource.ScopeError) as error:
            self.bind(proc)
        self.assertEqual(error.exception.code, 10)
        proc = OwnedProc()
        proc.records[42], proc.executables[42], proc.commands[42] = proc.records[41], proc.executables[41], proc.commands[41][:]
        proc.candidate_ids.add(42)
        with self.assertRaises(resource.ScopeError) as error:
            self.bind(proc)
        self.assertEqual(error.exception.code, 10)

    def test_bind_rejects_foreign_cores_and_shared_state(self):
        changes = [
            lambda p: p.records.pop(1000),
            lambda p: p.records.__setitem__(2000, (999, 300, 6000)),
            lambda p: p.commands[2000].__setitem__(1, "version"),
            lambda p: p.commands[2000].__setitem__(3, "/private/foreign.json"),
            lambda p: p.commands[1001].__setitem__(2, "/owned/private-state/0/node"),
            lambda p: p.executables.__setitem__(2000, (9, 9)),
            lambda p: p.commands[41].__setitem__(2, "^OtherTest$"),
        ]
        for i, change in enumerate(changes):
            proc = OwnedProc()
            change(proc)
            with self.subTest(case=i), self.assertRaises(ValueError):
                self.bind(proc)

    def test_bound_counters_reject_reuse_exec_reparent_and_regression(self):
        for kind in ("birth", "parent", "exec", "counter"):
            proc = OwnedProc()
            bindings = self.bind(proc)
            initial = resource.counters(proc, bindings)
            parent, cpu, birth = proc.records[2000]
            if kind == "exec":
                proc.executables[2000] = (9, 9)
            else:
                proc.records[2000] = (parent + int(kind == "parent"), cpu - int(kind == "counter"), birth + int(kind == "birth"))
            with self.subTest(kind=kind), self.assertRaises(ValueError):
                resource.counters(proc, bindings, initial)
        with self.assertRaises(ValueError):
            resource.counters(OwnedProc(), bindings, [0])

    def test_resource_schema_is_diagnostic_and_closed(self):
        data = resource.report(100)
        data["complete"], data["phase_poll_bound_ms"] = 1, 251
        data["affinity_cpus"] = 4
        for elapsed in list(range(0, 115001, 5000)) + [118100]:
            data["samples"].append({"elapsed_ms": elapsed, "panel_cpu_ticks": elapsed // 100,
                                    "node_cpu_ticks": [elapsed // 200] * 65, "core_cpu_ticks": [elapsed // 50] * 65})
        summary = resource.validate_report(data)
        self.assertEqual(summary["complete"], 1)
        self.assertEqual(summary["samples"], 25)
        self.assertEqual(summary["node_cpu_ticks"], 65 * 590)
        changes = [
            lambda d: d.__setitem__("pid", 41),
            lambda d: d.__setitem__("pressure_complete", 1),
            lambda d: d.__setitem__("reason", 7),
            lambda d: d.__setitem__("clock_ticks_per_second", 0),
            lambda d: d.__setitem__("phase_poll_bound_ms", 501),
            lambda d: d.__setitem__("binding_ms", 501),
            lambda d: d["samples"][-1].__setitem__("elapsed_ms", 119000),
            lambda d: d["samples"][-1]["node_cpu_ticks"].pop(),
            lambda d: d["samples"][-1]["core_cpu_ticks"].__setitem__(0, -1),
            lambda d: d["samples"][-1].__setitem__("panel_cpu_ticks", True),
            lambda d: d["samples"][-1].__setitem__("panel_cpu_ticks", 0),
            lambda d: d["samples"][0].__setitem__("elapsed_ms", 1),
            lambda d: d["cgroup"].__setitem__("quota_us", 100000),
        ]
        for i, change in enumerate(changes):
            invalid = copy.deepcopy(data)
            change(invalid)
            with self.subTest(case=i), self.assertRaises(ValueError):
                resource.validate_report(invalid)

    def test_incomplete_scope_never_implies_a_completed_window(self):
        data = resource.report(0)
        data["reason"] = 2
        summary = resource.validate_report(data)
        self.assertEqual((summary["complete"], summary["samples"]), (0, 0))
        data["complete"] = 1
        with self.assertRaises(ValueError):
            resource.validate_report(data)

    def test_cpu_budget_preserves_units_and_unknown_is_distinct(self):
        self.assertEqual(resource.cpu_budget("200000 100000\n"), {"available": 1, "limited": 1, "quota_us": 200000, "period_us": 100000})
        self.assertEqual(resource.cpu_budget("max 100000\n"), {"available": 1, "limited": 0, "quota_us": 0, "period_us": 100000})
        for text in ["", "max 0", "0 100000", "-1 100000", "1 -1", "1 2 3", "private 100000", f"{resource.MAX_SAFE+1} 100000"]:
            with self.subTest(text=text), self.assertRaises(ValueError):
                resource.cpu_budget(text)

    def test_sampler_lifecycle_keeps_subset_and_rejects_early_or_late_phase(self):
        for scenario in ("complete", "early_end", "late_detection"):
            with self.subTest(scenario=scenario), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                proc = OwnedProc()
                clock = [0.0]
                phase = root / "destination-global-setup.cpu.pprof"

                def sleep(seconds):
                    clock[0] += seconds * (3 if scenario == "late_detection" and not phase.exists() else 1)
                    phase.touch(exist_ok=True)
                    if scenario == "early_end" and clock[0] >= 20:
                        (root / "destination-global-setup.alloc-after.pprof").touch()

                def identity(path):
                    path = Path(path)
                    if path == phase:
                        return 8, 8
                    if path == root / "destination-global-batch.test":
                        return 1, 1
                    if path == root / "destination-node":
                        return 2, 2
                    return proc.paths[str(path)]

                result = type("Result", (), {"stdout": b"100\n"})()
                with patch.object(resource, "Proc", return_value=proc), patch.object(resource, "file_identity", identity), patch.object(resource, "cgroup_limits", return_value=resource.report(0)["cgroup"]), patch.object(resource.subprocess, "run", return_value=result), patch.object(resource.time, "monotonic", lambda: clock[0]), patch.object(resource.time, "sleep", sleep), patch.object(resource.os, "sched_getaffinity", return_value={0, 1, 2, 3}, create=True):
                    resource.run(root)
                data = json.loads((root / "destination-global-process-resources.json").read_text())
                summary = resource.validate_report(data)
                if scenario == "complete":
                    self.assertEqual((summary["complete"], summary["samples"], summary["elapsed_ms"]), (1, 25, 118000))
                else:
                    self.assertEqual(summary["complete"], 0)
                    self.assertEqual(summary["reason"], 5 if scenario == "early_end" else 4)

    def test_stopped_helper_writes_only_closed_incomplete_evidence(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "destination-global-process-resources.stop").touch()
            result = type("Result", (), {"stdout": b"100\n"})()
            with patch.object(resource.subprocess, "run", return_value=result):
                resource.run(root)
            data = json.loads((root / "destination-global-process-resources.json").read_text())
            self.assertEqual(resource.validate_report(data)["complete"], 0)
            self.assertEqual(data["reason"], 1)
            with self.assertRaises(FileExistsError):
                resource.run(root)


if __name__ == "__main__":
    unittest.main()
