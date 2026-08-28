import { readdir, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const STATIC_COPIES = new Map([
  ["site/styles.css", "dist/styles.css"],
  ["site/favicon.svg", "dist/favicon.svg"],
  ["site/robots.txt", "dist/robots.txt"],
  ["site/sitemap.xml", "dist/sitemap.xml"],
  ["site/assets/fonts/SpaceMono-Regular.ttf", "dist/assets/fonts/SpaceMono-Regular.ttf"],
  ["site/assets/fonts/OFL.txt", "dist/assets/fonts/OFL.txt"],
  ["data/latest.json", "dist/data/latest.json"],
]);

const EXPECTED_DIST_FILES = [
  "assets/fonts/OFL.txt",
  "assets/fonts/SpaceMono-Regular.ttf",
  "data/latest.json",
  "favicon.svg",
  "index.html",
  "methodology/index.html",
  "robots.txt",
  "sitemap.xml",
  "styles.css",
];

const PAGE_MARKERS = new Map([
  ["dist/index.html", [
    "Urbit Development Institute",
    'id="mission"',
    'id="network"',
    'id="reports"',
    'id="contribute"',
  ]],
  ["dist/methodology/index.html", [
    "How the numbers are counted.",
    'class="method-page"',
    "Methodology v",
  ]],
]);

function compareLists(actual, expected) {
  return actual.length === expected.length && actual.every((value, index) => value === expected[index]);
}

async function listFiles(directory, prefix = "") {
  let entries;
  try {
    entries = await readdir(directory, { withFileTypes: true });
  } catch (error) {
    throw new Error(`read artifact directory ${directory}: ${error.message}`);
  }

  const files = [];
  for (const entry of entries.sort((left, right) => left.name.localeCompare(right.name))) {
    const relative = prefix ? `${prefix}/${entry.name}` : entry.name;
    const absolute = path.join(directory, entry.name);
    if (entry.isSymbolicLink()) {
      throw new Error(`artifact must not contain symbolic link: ${relative}`);
    }
    if (entry.isDirectory()) {
      files.push(...await listFiles(absolute, relative));
      continue;
    }
    if (!entry.isFile()) {
      throw new Error(`artifact contains unsupported entry: ${relative}`);
    }
    files.push(relative);
  }
  return files.sort();
}

async function requiredFile(root, relative) {
  const absolute = path.join(root, relative);
  try {
    return await readFile(absolute);
  } catch (error) {
    throw new Error(`read required file ${relative}: ${error.message}`);
  }
}

function parseSnapshot(contents, relative) {
  let snapshot;
  try {
    snapshot = JSON.parse(contents.toString("utf8"));
  } catch (error) {
    throw new Error(`parse ${relative}: ${error.message}`);
  }
  if (snapshot.schemaVersion !== 1) {
    throw new Error(`${relative} has unsupported schemaVersion ${JSON.stringify(snapshot.schemaVersion)}`);
  }
  if (snapshot.status !== "complete" || snapshot.coverage?.complete !== true) {
    throw new Error(`${relative} is not a complete publishable snapshot`);
  }
  if (!snapshot.generatedAt || Number.isNaN(Date.parse(snapshot.generatedAt))) {
    throw new Error(`${relative} has invalid generatedAt ${JSON.stringify(snapshot.generatedAt)}`);
  }
  return snapshot;
}

/**
 * Verify that the committed Vercel artifact is complete, publishable, and in
 * sync with every source file copied without rendering.
 */
export async function verifyDist(root) {
  const resolvedRoot = path.resolve(root);
  const artifactRoot = path.join(resolvedRoot, "dist");
  const files = await listFiles(artifactRoot);
  if (!compareLists(files, EXPECTED_DIST_FILES)) {
    throw new Error(`artifact manifest mismatch\nexpected: ${EXPECTED_DIST_FILES.join(", ")}\nactual:   ${files.join(", ")}`);
  }

  for (const [source, destination] of STATIC_COPIES) {
    const [sourceContents, destinationContents] = await Promise.all([
      requiredFile(resolvedRoot, source),
      requiredFile(resolvedRoot, destination),
    ]);
    if (!sourceContents.equals(destinationContents)) {
      throw new Error(`generated artifact ${destination} does not match ${source}`);
    }
  }

  const snapshotContents = await requiredFile(resolvedRoot, "dist/data/latest.json");
  const snapshot = parseSnapshot(snapshotContents, "dist/data/latest.json");

  for (const [page, markers] of PAGE_MARKERS) {
    const contents = (await requiredFile(resolvedRoot, page)).toString("utf8");
    if (contents.includes("{{")) {
      throw new Error(`${page} contains unresolved template syntax`);
    }
    for (const marker of markers) {
      if (!contents.includes(marker)) {
        throw new Error(`${page} is missing expected content ${JSON.stringify(marker)}`);
      }
    }
  }

  return {
    files: files.length,
    generatedAt: snapshot.generatedAt,
    status: snapshot.status,
  };
}

const scriptPath = fileURLToPath(import.meta.url);
const invokedPath = process.argv[1] ? path.resolve(process.argv[1]) : "";
if (invokedPath === scriptPath) {
  const defaultRoot = path.resolve(path.dirname(scriptPath), "..");
  const root = path.resolve(process.argv[2] ?? defaultRoot);
  try {
    const result = await verifyDist(root);
    console.log(`verified Vercel static artifact: files=${result.files} status=${result.status} generated=${result.generatedAt}`);
  } catch (error) {
    console.error(`Vercel static artifact verification failed for ${root}: ${error.message}`);
    process.exitCode = 1;
  }
}
