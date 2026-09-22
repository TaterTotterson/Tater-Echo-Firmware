"""Build progress derived from the job log and from disk."""
from pathlib import Path
from types import SimpleNamespace
import inspect
import sys

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import forge
import forge_progress
from forge_progress import (CLIP_DIRS, FEATURE_FILES, TrainLog, complete_lines, fold_log,
                            stage_progress, train_progress)

# Verbatim tqdm 4.67 output for tqdm(total=5000.0, desc="Training"): ASCII
# and unicode bars, the float total, the trailing pad space, the closing
# newline. Kept literal so a regex edit or a tqdm format change fails here
# rather than leaving the bar at 0 on a real build.
TQDM_ASCII = ("\rTraining:   0%|          | 0/5000.0 [00:00<?, ?it/s]"
              "\rTraining:  25%|##4       | 1233/5000.0 [00:00<00:00, 112425583.30it/s]"
              "\rTraining: 100%|##########| 5000/5000.0 [00:00<00:00, 50546712.52it/s] "
              "\rTraining: 100%|##########| 5000/5000.0 [00:00<00:00, 30840470.59it/s]\n")
TQDM_UNICODE = ("\rTraining:   0%|          | 0/5000.0 [00:00<?, ?it/s]"
                "\rTraining:  25%|██▍       | 1233/5000.0 [00:00<00:00, 206863073.28it/s]"
                "\rTraining: 100%|██████████| 5000/5000.0 [00:00<00:00, 22568328.82it/s] "
                "\rTraining: 100%|██████████| 5000/5000.0 [00:00<00:00, 17787548.77it/s]\n")


def bar(n, m):
    return f"\rTraining:  {100 * n / float(m):3.0f}%|##4       | {n}/{m} [00:12<00:36, 101.2it/s]"


def marker(k):
    return f"INFO:root:{'#' * 50}\nStarting training sequence {k}...\n{'#' * 50}\n"


def step(name):
    return f"[forge] === step: {name} ===\n"


def test_fold_verbatim_tqdm_both_bar_styles():
    for text in (TQDM_ASCII, TQDM_UNICODE):
        assert fold_log(text) == {"stage": None, "sequence": 1, "step": 5000, "total": 5000}
        assert fold_log(text[:len(text) // 2])["step"] == 1233


def test_fold_reads_stage_marker_and_last_bar():
    text = step("train") + marker(1) + bar(10, 50000) + bar(20, 50000) + "\n" + marker(2) + bar(7, "5000.0")
    assert fold_log(text) == {"stage": "train", "sequence": 2, "step": 7, "total": 5000}


def test_fold_stage_line_resets_train_state():
    state = fold_log(step("train") + marker(2) + bar(9, "5000.0"))
    assert fold_log(step("augment"), state) == {"stage": "augment", "sequence": 1, "step": 0, "total": 0}


def test_fold_marker_after_bar_resets_step():
    state = fold_log(marker(1) + bar(49999, 50000) + "\n" + marker(2))
    assert state == {"stage": None, "sequence": 2, "step": 0, "total": 0}


def test_fold_bar_starting_over_counts_as_new_sequence():
    # The markers are logging.info lines that depend on a third-party import
    # configuring logging; a log with only warnings must still fill once.
    text = ("WARNING:root:Skipping generation\n" + bar(49999, 50000) + "\n"
            + bar(0, "5000.0") + bar(4999, "5000.0") + "\n" + bar(3, "5000.0"))
    assert fold_log(text) == {"stage": None, "sequence": 3, "step": 3, "total": 5000}
    # marker and restart together are one sequence, not two
    assert fold_log(marker(1) + bar(49999, 50000) + "\n" + marker(2) + bar(0, "5000.0"))["sequence"] == 2


def test_fold_is_incremental_across_chunks():
    state = fold_log(marker(1) + bar(100, 50000))
    state = fold_log("\n" + marker(2) + bar(5, "5000.0"), state)
    state = fold_log(bar(9, "5000.0"), state)
    assert state == {"stage": None, "sequence": 2, "step": 9, "total": 5000}


def test_fold_ignores_other_tqdm_bars_and_noise():
    text = "\rPredicting on clips:  50%|#####     | 5/10 [00:00<00:00]\n" + bar(3, 50000)
    text += "\rTraining:  10%|#         | 4" + "\n"   # truncated redraw, no total
    assert fold_log(text)["step"] == 3


def test_complete_lines_holds_partial_marker_and_bar():
    whole = (marker(1) + bar(50000, 50000) + "\n" + marker(2) + bar(12, "5000.0")).encode()
    for cut in range(len(whole)):
        # what a bounded read leaves unconsumed is re-read next time
        state, pos = None, 0
        for end in (cut, len(whole)):
            text = complete_lines(whole[pos:end])
            pos += len(text)
            if text:
                state = fold_log(text.decode(), state)
        # the last bar has no terminator yet, so it is still unread
        assert (state["sequence"], state["step"]) == (2, 0), cut
        assert fold_log(complete_lines(whole[pos:] + b"\n").decode(), state)["step"] == 12


def test_trainlog_folds_file_in_pieces(tmp_path, monkeypatch):
    log = tmp_path / "job.log"
    whole = (step("train") + marker(1) + bar(50000, 50000) + "\n" + marker(2)).encode()
    whole += b"\rTraining:  \xff bad byte |" + bar(12, "5000.0").encode()[1:] + b"\n"
    tl = TrainLog()
    log.write_bytes(b"")
    assert tl.update(log) is None
    for cut in (17, len(whole) // 2, len(whole) - 3, len(whole)):
        log.write_bytes(whole[:cut])
        tl.update(log)
    assert tl.state == {"stage": "train", "sequence": 2, "step": 12, "total": 5000}
    assert tl.offset == len(whole)
    # a backlog is folded a bounded slice per poll, never skipped, and is
    # flagged as lagging until the read comes up short
    monkeypatch.setattr(forge_progress, "READ_LIMIT", 120)
    tl = TrainLog()
    polls = 0
    while tl.offset < len(whole):
        before = tl.offset
        tl.update(log)
        polls += 1
        assert before < tl.offset <= before + 120
        assert tl.lagging == (tl.offset < len(whole))
    assert tl.state == {"stage": "train", "sequence": 2, "step": 12, "total": 5000}
    assert polls > 1
    # a line longer than the limit is consumed rather than waited for forever
    monkeypatch.setattr(forge_progress, "READ_LIMIT", 32)
    tl = TrainLog()
    log.write_bytes(b"x" * 100 + b"\n" + step("augment").encode())
    while tl.offset < log.stat().st_size:
        tl.update(log)
    assert tl.state["stage"] == "augment"


def test_step_line_matches_what_forge_logs():
    # The stage comes from forge.py's own log line; keep the regex and the
    # producer from drifting apart.
    src = inspect.getsource(forge.cmd_build)
    literal = src[src.index('"=== step: {step} ==="') + 1:].split('"')[0]
    assert fold_log(f"[forge] {literal.format(step='augment')}\n")["stage"] == "augment"


def test_train_progress_spans_three_sequences():
    assert train_progress(None, 50000) == (0, 60000)
    assert train_progress({"sequence": 1, "step": 25000, "total": 50000}, 50000) == (25000, 60000)
    assert train_progress({"sequence": 2, "step": 2500, "total": 5000}, 50000) == (52500, 60000)
    assert train_progress({"sequence": 3, "step": 5000, "total": 5000}, 50000) == (60000, 60000)
    # never past the end, whatever the log says
    assert train_progress({"sequence": 3, "step": 9999, "total": 5000}, 50000) == (60000, 60000)
    # the log's own total wins over the config once a bar has been seen
    assert train_progress({"sequence": 1, "step": 6000, "total": 50000}, 5000) == (6000, 60000)
    assert train_progress({"sequence": 2, "step": 10, "total": 5000}, 999) == (50010, 60000)
    assert train_progress({"sequence": 9, "step": 1, "total": 0}, 50000) == (55001, 60000)


CFG = {"n_samples": 30, "n_samples_val": 2, "steps": 500}


def touch(d, names):
    d.mkdir(parents=True, exist_ok=True)
    for n in names:
        (d / n).write_bytes(b"")


def test_stage_generate_counts_all_four_clip_dirs(tmp_path):
    assert stage_progress(tmp_path, CFG, None) == {"stage": "generate", "done": 0, "total": 64}
    touch(tmp_path / "positive_train", [f"{i}.wav" for i in range(30)])
    touch(tmp_path / "positive_test", ["a.wav", "b.wav", "notes.txt"])
    touch(tmp_path / "negative_train", [f"{i}.wav" for i in range(10)])
    gen = fold_log(step("generate"))
    assert stage_progress(tmp_path, CFG, gen) == {"stage": "generate", "done": 42, "total": 64}
    # a Google TTS mix-in overshoots positive_train; it must not stand in
    # for the negatives still to be generated
    touch(tmp_path / "positive_train", [f"x{i}.wav" for i in range(40)])
    assert stage_progress(tmp_path, CFG, gen)["done"] == 42
    touch(tmp_path / "negative_train", [f"x{i}.wav" for i in range(40)])
    touch(tmp_path / "negative_test", ["a.wav", "b.wav"])
    assert stage_progress(tmp_path, CFG, gen)["done"] == 64
    assert len(CLIP_DIRS) == 4


def test_stage_follows_forge_step_line_not_disk(tmp_path):
    # Features left from an earlier run while clips are being topped up:
    # the step line says generate, so that is what is shown.
    touch(tmp_path, FEATURE_FILES)
    touch(tmp_path / "positive_train", ["a.wav"])
    assert stage_progress(tmp_path, CFG, fold_log(step("generate")))["stage"] == "generate"
    assert stage_progress(tmp_path, CFG, fold_log(step("augment")))["stage"] == "augment"


def test_stage_augment_counts_finished_feature_files(tmp_path):
    aug = fold_log(step("augment"))
    assert stage_progress(tmp_path, CFG, aug) == {"stage": "augment", "done": 0, "total": 4}
    touch(tmp_path, FEATURE_FILES[:1])   # exists from the moment its memmap opens
    assert stage_progress(tmp_path, CFG, aug)["done"] == 0
    touch(tmp_path, FEATURE_FILES[:3])
    assert stage_progress(tmp_path, CFG, aug)["done"] == 2


def test_stage_train_uses_log_state(tmp_path):
    touch(tmp_path, FEATURE_FILES)
    state = fold_log(step("train"))
    assert stage_progress(tmp_path, CFG, state) == {"stage": "train", "done": 0, "total": 600}
    state = fold_log(marker(1) + bar(250, 500) + "\n", state)
    assert stage_progress(tmp_path, CFG, state) == {"stage": "train", "done": 250, "total": 600}


def test_stage_tolerates_missing_config_keys(tmp_path):
    assert stage_progress(tmp_path, {}, None) == {"stage": "generate", "done": 0, "total": 0}
    assert stage_progress(tmp_path, {}, fold_log(step("train"))) == {"stage": "train", "done": 0, "total": 0}


def test_wakewords_state_carries_progress_for_the_running_build(tmp_path, monkeypatch):
    forge_web = pytest.importorskip("forge_web")   # aiohttp
    monkeypatch.setattr(forge, "WAKEWORDS", tmp_path / "wakewords")
    monkeypatch.setattr(forge, "MODELS", tmp_path / "models")
    for name in ("hey_a", "hey_b"):
        ww = tmp_path / "wakewords" / name
        ww.mkdir(parents=True)
        (ww / "config.yml").write_text(
            f'model_name: "{name}"\ntarget_phrase: ["{name}"]\nn_samples: 10\n'
            f'n_samples_val: 2\nsteps: 100\noutput_dir: "{ww}"\n')
    touch(tmp_path / "wakewords" / "hey_a" / "hey_a" / "positive_train", ["1.wav", "2.wav"])
    log = tmp_path / "job.log"
    notes = []
    job = SimpleNamespace(kind="build", label="building 'hey_a'", poll=lambda: None,
                          log_path=log, train_log=TrainLog(), progress_warned=False,
                          _note=notes.append)
    monkeypatch.setattr(forge_web, "_job", job)
    by_name = lambda: {w["name"]: w["progress"] for w in forge_web._wakewords_state()}

    log.write_bytes(b"[forge] torch 2.7.1\n")          # no step announced yet
    assert by_name() == {"hey_a": None, "hey_b": None}
    log.write_bytes(b"[forge] torch 2.7.1\n" + step("generate").encode())
    assert by_name() == {"hey_a": {"stage": "generate", "done": 2, "total": 24}, "hey_b": None}

    job.kind = "google-tts"                           # not a build: no fill
    assert by_name()["hey_a"] is None
    job.kind = "build"

    log.unlink()                                      # the poll survives, and says why once
    assert by_name()["hey_a"] is None
    assert by_name()["hey_a"] is None
    assert len(notes) == 1 and "FileNotFoundError" in notes[0] and "hey_a" in notes[0]
