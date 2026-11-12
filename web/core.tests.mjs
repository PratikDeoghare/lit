const assert = (c, m) => { if (!c) { console.error("FAIL", m); process.exitCode = 1; } else console.log("ok", m); };
const LIT = `#h1{Tip}

Prose.

#split{
  \`\`\`
  def split(total, people, tip=0.15):
      <<<add the tip>>>
      return round(total / people, 2)
  \`\`\`
  #add the tip{
    \`\`\`
    total = total * (1 + tip)
    \`\`\`
  }
}

#tip.py{
  \`\`\`
  <<<split>>>

  print(split(84.0, 4))
  \`\`\`
}
`;
const t = tangle(LIT);
assert(t.files["tip.py"].join("\n") === 'def split(total, people, tip=0.15):\n    total = total * (1 + tip)\n    return round(total / people, 2)\n\nprint(split(84.0, 4))', "tangle");
// absorb nested + top
let gen = t.files["tip.py"].slice(); gen[1] = "    total *= 1 + tip"; gen[4] = "print(split(120.0, 3))";
const a = absorb(LIT, "tip.py", t.files["tip.py"], gen);
assert(!a.errors.length && a.text.includes("    total *= 1 + tip") && a.text.includes("print(split(120.0, 3))"), "absorb " + a.errors);
assert(JSON.stringify(tangle(a.text).files["tip.py"]) === JSON.stringify(gen), "absorb roundtrip");
// insert/delete
const g2 = t.files["tip.py"].slice(); g2.splice(2, 0, "    # tip is a fraction");
const a2 = absorb(LIT, "tip.py", t.files["tip.py"], g2);
assert(!a2.errors.length && JSON.stringify(tangle(a2.text).files["tip.py"]) === JSON.stringify(g2), "absorb insert " + a2.errors);
const g3 = t.files["tip.py"].slice(); g3.splice(3, 1);
const a3 = absorb(LIT, "tip.py", t.files["tip.py"], g3);
assert(!a3.errors.length && JSON.stringify(tangle(a3.text).files["tip.py"]) === JSON.stringify(g3), "absorb delete " + a3.errors);
// merge: lit edit + gen edit
const lit2 = LIT.replace("tip=0.15", "tip=0.18");
const m = mergeFile(lit2, "tip.py", t.files["tip.py"], gen);
assert(m.regions.length === 3 && m.regions.map(r=>r.kind).join() === "lit,gen,gen", "regions " + m.regions.map(r=>r.kind));
assert(!m.errors.length && m.gen.join("\n").includes("tip=0.18") && m.gen.join("\n").includes("120.0") && m.newLit.includes("tip=0.18") && m.newLit.includes("120.0"), "merge both ways " + m.errors);
// conflict
const gc = t.files["tip.py"].slice(); gc[0] = gc[0].replace("0.15", "0.20");
const mc = mergeFile(lit2, "tip.py", t.files["tip.py"], gc);
assert(mc.regions.length === 1 && mc.regions[0].kind === "conflict", "conflict");
const mc2 = mergeFile(lit2, "tip.py", t.files["tip.py"], gc, { 0: "gen" });
assert(mc2.newLit.includes("0.20") && mc2.gen[0].includes("0.20"), "conflict take gen");
// errors
for (const bad of ["#h1{x}\nstray } here\n", "#h1{x}\nprose # hash\n", "#h1{x}\na { b\n", "#a{\n"]) {
  try { load(bad); assert(false, "should fail " + bad); } catch (e) { assert(e instanceof LitError, `${JSON.stringify(bad)} -> line ${e.line}: ${e.message}`); }
}
// escape
const E = "#d.txt{\n  ```\n  \\<<<name>>>\n  ```\n}\n".replace(/```/g, "```");
assert(tangle(E).files["d.txt"][0] === "<<<name>>>", "escape");
assert(weave(LIT).includes("§2"), "weave");
