"""Where a build is, per stage, for the web UI's stepper.

Pure functions with no training-stack imports, so they run under CI's
forge-metadata-tests job (no torch). The stage comes from forge.py's own
"=== step: X ===" lines in the job log. The ratio for generate and augment
is counted from the wake word's directory, like the rest of the UI's
state, so it stays right when something else (a `docker compose run` the
one-job lock cannot see) is writing there too; it is only shown for this
UI's own build, the one job it knows about. Training writes nothing to
disk before the .onnx, so that stage is parsed from train.py's tqdm bar.
"""

import re
from pathlib import Path

from forge import FEATURE_FILES

# Listed as train.py generates them; order is cosmetic, only counted.
CLIP_DIRS = ("positive_train", "positive_test", "negative_train", "negative_test")

# Three things in the job log move the state:
#   [forge] === step: train ===          forge.py, before each train.py run
#   Starting training sequence 2...      train.py, logging.info
#   Training:  25%|##4       | 1234/5000.0 [00:12<00:36, 101.2it/s]
# The bar is `tqdm(..., total=max_steps, desc="Training")` in openWakeWord's
# train.py (pinned SHA in the Dockerfile). auto_train runs it three times:
# `steps`, then `steps/10` twice, so the total is a float from the second
# sequence on, hence the optional fraction; the bar between the pipes holds
# digits in ASCII mode, hence anchoring on "| N/M [". The sequence markers
# are logging.info lines that only reach the log because train.py imports
# piper's generate_samples.py, whose import-time logging.basicConfig()
# configures the root logger; a bar that starts over is treated as a new
# sequence as well, so the fill does not depend on that side effect.
_EVENT = re.compile(
    r"=== step: (\w+) ==="
    r"|Starting training sequence (\d)"
    r"|Training:[^\r\n]*\|\s*(\d+)/(\d+)(?:\.\d+)?\s*\[")

# Per poll. Nobody may have had the page open since the build started, and
# a CPU run's log is tens of MB by morning: folding it all in one go would
# hold the event loop for seconds. This is a fraction of a second, and the
# backlog clears over the next few polls.
READ_LIMIT = 1 << 20


def complete_lines(buf: bytes) -> bytes:
    """`buf` up to its last line or tqdm redraw boundary.

    A marker straddling two reads would otherwise be lost, and a bar split
    mid-number would parse as a smaller step. What is left over is not
    consumed, so the next read starts from it.
    """
    return buf[:max(buf.rfind(b"\n"), buf.rfind(b"\r")) + 1]


def fold_log(text: str, state: dict | None = None) -> dict:
    """Fold a chunk of the job log into {"stage", "sequence", "step", "total"}.

    Incremental, so a poll costs the bytes written since the last one.
    `text` should end on a boundary from complete_lines().
    """
    state = dict(state or {"stage": None, "sequence": 1, "step": 0, "total": 0})
    for m in _EVENT.finditer(text):
        if m.group(1):
            state.update(stage=m.group(1), sequence=1, step=0, total=0)
        elif m.group(2):
            state.update(sequence=max(state["sequence"], int(m.group(2))), step=0, total=0)
        else:
            step = int(m.group(3))
            if step < state["step"]:
                state["sequence"] += 1
            state.update(step=step, total=int(m.group(4)))
    return state


class TrainLog:
    """Incremental fold of one job's log file; update(path) once per poll."""

    def __init__(self):
        self.state = None
        self.offset = 0
        self.lagging = False   # a backlog is still being folded: state is stale

    def update(self, path: Path) -> dict | None:
        with open(path, "rb") as f:
            f.seek(self.offset)
            chunk = f.read(READ_LIMIT)
        self.lagging = len(chunk) == READ_LIMIT
        text = complete_lines(chunk)
        if not text and len(chunk) == READ_LIMIT:
            text = chunk   # a line longer than the limit: take it, or wait forever
        self.offset += len(text)
        if text:
            self.state = fold_log(text.decode("utf-8", "replace"), self.state)
        return self.state


def train_progress(state: dict | None, steps: int) -> tuple[int, int]:
    """(done, total) across all three sequences, so the bar fills once.

    Sequence 1 is 5/6 of the work; without this the bar would hit 100%
    and then start over twice. `steps` is the config value, used until
    the bar reports its own total: the config can be edited under a
    running build, the log cannot.
    """
    if state and state["total"]:
        steps = state["total"] if state["sequence"] == 1 else state["total"] * 10
    per_sequence = [steps, steps // 10, steps // 10]
    total = sum(per_sequence)
    if not state:
        return 0, total
    sequence = min(state["sequence"], len(per_sequence))
    return min(sum(per_sequence[:sequence - 1]) + state["step"], total), total


def stage_progress(work_dir: Path, cfg: dict, state: dict | None) -> dict:
    """{"stage": generate|augment|train, "done", "total"} for one wake word.

    `work_dir` is <output_dir>/<model_name>, where train.py writes clips
    and features.
    """
    stage = (state or {}).get("stage")
    if stage == "train":
        done, total = train_progress(state, int(cfg.get("steps") or 0))
    elif stage == "augment":
        # A feature file exists from the moment train.py opens its memmap,
        # so files present minus the one in flight is what is finished.
        # Ceiling: files left from an earlier run count as finished, so a
        # re-augment (--overwrite, or forge.py's partial-set heal) over-reads
        # by that many; over a full set it sits at 3/4 throughout.
        total = len(FEATURE_FILES)
        done = max(sum((work_dir / f).exists() for f in FEATURE_FILES) - 1, 0)
    else:
        # No step announced yet, or one this code does not know: generate,
        # the first. train.py fills each clip dir to n_samples or n_samples_val;
        # counting only positive_train would show 100% with half the stage
        # still to run, and capping per dir rather than in total keeps a
        # Google TTS mix-in (extra positives) from doing the same. Ceiling:
        # train.py leaves a dir alone above 95% of its target, so a topped-up
        # word can finish generate a little short of 100%.
        stage = "generate"
        n, nv = int(cfg.get("n_samples") or 0), int(cfg.get("n_samples_val") or 0)
        targets = zip(CLIP_DIRS, (n, nv, n, nv))
        done = sum(min(sum(1 for _ in (work_dir / d).glob("*.wav")), t) for d, t in targets)
        total = 2 * (n + nv)
    return {"stage": stage, "done": done, "total": total}
