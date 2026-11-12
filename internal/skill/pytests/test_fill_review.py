#!/usr/bin/env python3
"""Tests of fill.py and review.py with fake agents (small scripts that read the prompt on stdin).

    python test_fill_review.py [SCRIPTS_DIR]
"""
import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPTS = os.path.abspath(sys.argv.pop(1)) if len(sys.argv) > 1 else os.path.join(HERE, "..", "files", "scripts")

PROG = """#h1{Counter}
Counts up to a limit.

#count.py{
  ```
  def count(n):
      <<<loop>>>

  print(count(3))
  ```
}

#loop{
  Add the numbers one by one.
  #agent-fill-in{ return the sum of 1..n with a for loop }
}

#extra{
  Second gap, in a chunk nobody asked about.
  #agent-fill-in{ say hello }
}
"""

FENCE = "`" * 3
GOOD = FENCE + "\ntotal = 0\nfor i in range(1, n + 1):\n    total += i\nreturn total\n" + FENCE
_n = [0]


def agent(body):
    """A fake agent: a command whose python source `body` sees the prompt as `p` and prints the reply."""
    _n[0] += 1
    path = os.path.join(tempfile.gettempdir(), "fake_agent_%d_%d.py" % (os.getpid(), _n[0]))
    with open(path, "w") as f:
        f.write("import sys\np = sys.stdin.read()\n" + body + "\n")
    return "%s %s" % (sys.executable, path)


def rd(*parts):
    with open(os.path.join(*parts), encoding="utf-8") as f:
        return f.read()


def say(text):
    return "print(%r)" % text


def run(name, *args, cwd):
    p = subprocess.run([sys.executable, os.path.join(SCRIPTS, name + ".py"), *args], cwd=cwd, capture_output=True, text=True)
    return p.returncode, p.stdout + p.stderr


class FillReview(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.d = self.tmp.name
        with open(os.path.join(self.d, "p.lit"), "w", encoding="utf-8") as f:
            f.write(PROG)

    def tearDown(self):
        self.tmp.cleanup()

    def lit(self):
        return rd(self.d, "p.lit")

    def test_list_numbers_the_hints_in_document_order(self):
        code, out = run("fill", "p.lit", "--list", cwd=self.d)
        self.assertEqual(code, 0, out)
        self.assertIn("«loop»", out.split("\n")[0])
        self.assertIn("«extra»", out.split("\n")[1])

    def test_the_prompt_shows_prose_use_site_and_tangled_file_with_the_gap_marked(self):
        code, out = run("fill", "p.lit", "--dry-run", cwd=self.d)
        self.assertEqual(code, 0, out)
        for want in ("Add the numbers one by one", "def count(n):", "FILL IN HERE", "Ends up in: count.py", "Language: py"):
            self.assertIn(want, out)
        self.assertNotIn("say hello", out.split("=== the chunk")[1].split("=== the tangled")[0])

    def test_fill_inserts_code_keeps_the_hint_as_prose_and_the_program_tangles(self):
        code, out = run("fill", "p.lit", "--only", "0", "--agent", agent(say(GOOD)), cwd=self.d)
        self.assertEqual(code, 0, out)
        self.assertIn("return the sum of 1..n with a for loop", self.lit())
        self.assertEqual(self.lit().count("#agent-fill-in"), 1)
        code, out = run("tangle", "p.lit", "out", cwd=self.d)
        self.assertEqual(code, 0, out)
        ns = subprocess.run([sys.executable, os.path.join(self.d, "out", "count.py")], capture_output=True, text=True)
        self.assertEqual(ns.stdout.strip(), "6", ns.stdout + ns.stderr)

    def test_a_placeholder_is_rejected_and_the_agent_gets_the_error_on_retry(self):
        body = "print(%r if 'rejected' not in p else %r)" % (FENCE + "\n# TODO\n" + FENCE, GOOD)
        code, out = run("fill", "p.lit", "--only", "0", "--agent", agent(body), cwd=self.d)
        self.assertEqual(code, 0, out)
        self.assertIn("attempt 1 failed", out)
        self.assertNotIn("TODO", self.lit())

    def test_a_syntax_error_is_never_written(self):
        code, out = run("fill", "p.lit", "--only", "0", "--retries", "0", "--agent", agent(say(FENCE + "\nreturn (\n" + FENCE)), cwd=self.d)
        self.assertNotEqual(code, 0)
        self.assertEqual(self.lit(), PROG)

    def test_cannot_leaves_the_program_alone(self):
        code, out = run("fill", "p.lit", "--only", "0", "--agent", agent(say("CANNOT: no idea")), cwd=self.d)
        self.assertNotEqual(code, 0)
        self.assertIn("no idea", out)
        self.assertEqual(self.lit(), PROG)

    def test_check_command_runs_on_the_tangled_program_and_can_veto(self):
        body = say(GOOD)
        code, out = run("fill", "p.lit", "--only", "0", "--retries", "0", "--check-each", "--check", "false", "--agent", agent(body), cwd=self.d)
        self.assertNotEqual(code, 0)
        self.assertEqual(self.lit(), PROG)
        code, out = run("fill", "p.lit", "--only", "0", "--check-each", "--check", "python3 count.py | grep -q 6", "--agent", agent(body), cwd=self.d)
        self.assertEqual(code, 0, out)

    def test_all_hints_filled_in_order_and_each_fill_sees_the_previous(self):
        body = "print('```\\nreturn 1\\n```' if 'say hello' not in p else '```\\nprint(\"hello\")\\n```')"
        code, out = run("fill", "p.lit", "--agent", agent(body), cwd=self.d)
        self.assertEqual(code, 0, out)
        self.assertIn("0 hint(s) still open", out)

    def test_review_lists_approves_and_rejects(self):
        body = say(GOOD)
        run("fill", "p.lit", "--only", "0", "--agent", agent(body), cwd=self.d)
        code, out = run("review", "p.lit", "--full", "--check", "python3 count.py", cwd=self.d)
        self.assertEqual(code, 0, out)
        self.assertIn("pending", out)
        self.assertIn("checks passed", out)
        fid = json.loads(rd(self.d, "p.lit.fills.jsonl").split("\n")[0])["id"]
        code, out = run("review", "p.lit", "--ai", "--agent", agent(say("VERDICT: reject\n- wrong")), cwd=self.d)
        self.assertEqual(code, 1, out)
        code, out = run("review", "p.lit", "--reject", fid, cwd=self.d)
        self.assertEqual(code, 0, out)
        self.assertEqual(self.lit().count("#agent-fill-in"), 2)
        self.assertEqual(self.lit(), PROG.replace("{ return the sum", "{ return the sum"))
        code, out = run("review", "p.lit", cwd=self.d)
        self.assertIn("nothing to review", out)

    def test_approve_all_marks_pending_fills(self):
        body = say(FENCE + "\nreturn 1\n" + FENCE)
        run("fill", "p.lit", "--agent", agent(body), cwd=self.d)
        code, out = run("review", "p.lit", "--approve-all", cwd=self.d)
        self.assertEqual(code, 0, out)
        code, out = run("review", "p.lit", "--json", cwd=self.d)
        self.assertEqual(json.loads(out), [])
        code, out = run("review", "p.lit", "--all", "--json", cwd=self.d)
        self.assertEqual([f["status"] for f in json.loads(out)], ["approved", "approved"])

    def test_tangle_and_weave_leave_hints_out_and_say_so(self):
        code, out = run("tangle", "p.lit", "out", cwd=self.d)
        self.assertIn("2 #agent-fill-in hint(s) still open", out)
        code, out = run("weave", "p.lit", "-o", "w", "--only", "html", cwd=self.d)
        self.assertEqual(code, 0, out)
        self.assertIn("to be filled in by an agent", rd(self.d, "w", "p.html"))


if __name__ == "__main__":
    unittest.main(verbosity=1)
