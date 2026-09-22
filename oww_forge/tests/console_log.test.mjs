// Tests for the forge console's log fold in static/index.html — what a
// stream of raw job output becomes on screen.
//
//     node oww_forge/tests/console_log.test.mjs
//
// Source extraction rather than import, as controller/tests does: the page
// is a single classic script with no module boundary, so the alternative is
// a second copy that drifts.
//
// tqdm redraws its bar with \r, so a training run is thousands of
// near-identical segments per line. The fold keeps the last redraw of each
// line, and carries the line still being written across polls: a redraw can
// be split by the 64KB /api/log read anywhere, including mid-number.

import { readFileSync } from "fs";
import { fileURLToPath } from "url";
import { dirname, join } from "path";
import assert from "assert";

const HERE = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(HERE, "..", "static", "index.html"), "utf8");

function lift(re) {
  const m = src.match(re);
  if (!m) throw new Error(`could not find ${re} in index.html`);
  return m[0];
}
const { collapseCr, foldLog } = new Function(
  lift(/const collapseCr = [^\n]+/) + "\n" +
  lift(/function foldLog\(done, tail, data\) \{[\s\S]*?\n\}/) +
  "\nreturn { collapseCr, foldLog };")();

// last redraw per line wins; \n ends a line; CRLF is a line ending, not a
// redraw, and a trailing \r waits for its next character to decide
assert.equal(collapseCr("a\rb\rc\nx\r\ny\rz"), "c\nx\r\nz");
assert.equal(collapseCr("plain\n"), "plain\n");
assert.equal(collapseCr("held\r"), "held\r");

const whole =
  "[forge] === step: train ===\n" +
  "\rTraining:  0%| | 0/50000 [\rTraining:  1%| | 500/50000 [\rTraining:  2%| | 1000/50000 [00:10]\n" +
  "INFO:root:done\n" +
  "crlf line\r\n" +
  "\rTraining: 0%| | 0/5000.0 [\rTraining: 50%| | 2500/5000.0 [";
const expect =
  "[forge] === step: train ===\n" +
  "Training:  2%| | 1000/50000 [00:10]\n" +
  "INFO:root:done\n" +
  "crlf line\r\n" +
  "Training: 50%| | 2500/5000.0 [";

// the same screen whatever the poll boundary
for (let cut = 0; cut <= whole.length; cut++) {
  let [done, tail] = foldLog("", "", whole.slice(0, cut));
  [done, tail] = foldLog(done, tail, whole.slice(cut));
  assert.equal(done + tail, expect, `split at ${cut}`);
}

// a live bar never ends, and never grows the tail past one redraw
let done = "", tail = "";
for (let i = 0; i < 5000; i++) [done, tail] = foldLog(done, tail, `\rTraining: ${i}/5000 [`);
assert.equal(done, "");
assert.equal(tail, "Training: 4999/5000 [");

// the trim only ever eats committed history
[done, tail] = foldLog("x".repeat(100000), "", "y\n\rlive");
assert.equal(done.length, 100000);
assert.ok(done.endsWith("y\n"));
assert.equal(tail, "live");

console.log("console_log: ok");
