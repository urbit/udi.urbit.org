import assert from "node:assert/strict";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { verifyDist } from "./verify-dist.mjs";

const STATIC_FILES = new Map([
  ["styles.css", "body { color: #3f3f3f; }\n"],
  ["favicon.svg", "<svg></svg>\n"],
  ["robots.txt", "User-agent: *\nAllow: /\n"],
  ["sitemap.xml", "<urlset></urlset>\n"],
  ["assets/fonts/SpaceMono-Regular.ttf", Buffer.from([0, 1, 2, 3])],
  ["assets/fonts/OFL.txt", "SIL Open Font License\n"],
]);

async function write(root, relative, contents) {
  const destination = path.join(root, relative);
  await mkdir(path.dirname(destination), { recursive: true });
  await writeFile(destination, contents);
}

async function fixture(t) {
  const root = await mkdtemp(path.join(os.tmpdir(), "udi-dist-verifier-"));
  t.after(() => rm(root, { recursive: true, force: true }));

  const snapshot = `${JSON.stringify({
    schemaVersion: 1,
    status: "complete",
    generatedAt: "2026-08-13T17:43:22Z",
    coverage: { complete: true },
  }, null, 2)}\n`;
  await write(root, "data/latest.json", snapshot);
  await write(root, "dist/data/latest.json", snapshot);

  for (const [relative, contents] of STATIC_FILES) {
    await write(root, `site/${relative}`, contents);
    await write(root, `dist/${relative}`, contents);
  }

  await write(root, "dist/index.html", `<!doctype html>
<title>Urbit Development Institute</title>
<main><section id="mission"></section><section id="network"></section><section id="reports"></section><section id="contribute"></section></main>
`);
  await write(root, "dist/methodology/index.html", `<!doctype html>
<body class="method-page"><h1>How the numbers are counted.</h1><p>Methodology v1</p></body>
`);
  return root;
}

test("verifyDist accepts a complete synchronized artifact", async (t) => {
  const root = await fixture(t);
  const result = await verifyDist(root);
  assert.deepEqual(result, {
    files: 9,
    generatedAt: "2026-08-13T17:43:22Z",
    status: "complete",
  });
});

test("verifyDist rejects unexpected artifact files", async (t) => {
  const root = await fixture(t);
  await write(root, "dist/private.txt", "not public\n");
  await assert.rejects(verifyDist(root), /artifact manifest mismatch/);
});

test("verifyDist rejects copied files that drift from source", async (t) => {
  const root = await fixture(t);
  await write(root, "dist/styles.css", "body { color: red; }\n");
  await assert.rejects(verifyDist(root), /dist\/styles\.css does not match site\/styles\.css/);
});

test("verifyDist rejects unresolved templates", async (t) => {
  const root = await fixture(t);
  await write(root, "dist/index.html", "Urbit Development Institute {{.Snapshot}}\n");
  await assert.rejects(verifyDist(root), /unresolved template syntax/);
});

test("verifyDist rejects incomplete snapshots", async (t) => {
  const root = await fixture(t);
  const snapshot = `${JSON.stringify({
    schemaVersion: 1,
    status: "draft",
    generatedAt: "2026-08-13T17:43:22Z",
    coverage: { complete: false },
  }, null, 2)}\n`;
  await write(root, "data/latest.json", snapshot);
  await write(root, "dist/data/latest.json", snapshot);
  await assert.rejects(verifyDist(root), /not a complete publishable snapshot/);
});
