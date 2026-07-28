#!/usr/bin/env node

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import {
  mkdirSync,
  readFileSync,
  renameSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";

const DAY = 24 * 60 * 60 * 1000;

function usage() {
  console.error(
    "usage: update-star-history.mjs <bootstrap|snapshot> OWNER/REPO HISTORY_FILE SVG_FILE",
  );
  console.error("       update-star-history.mjs test");
}

function githubAPI(args) {
  return JSON.parse(
    execFileSync("gh", ["api", ...args], {
      encoding: "utf8",
      maxBuffer: 64 * 1024 * 1024,
    }),
  );
}

function fetchStargazers(repository) {
  return githubAPI([
    "--paginate",
    "--slurp",
    "-H",
    "Accept: application/vnd.github.star+json",
    "-H",
    "X-GitHub-Api-Version: 2022-11-28",
    `repos/${repository}/stargazers?per_page=100`,
  ]).flat();
}

function fetchStarCount(repository) {
  const count = githubAPI([
    "-H",
    "X-GitHub-Api-Version: 2022-11-28",
    `repos/${repository}`,
  ]).stargazers_count;
  if (!Number.isInteger(count) || count < 0) {
    throw new Error("GitHub returned an invalid stargazers_count");
  }
  return count;
}

function mergePoint(points, point) {
  return [
    ...new Map(
      [...points, point].map((current) => [current.date, current]),
    ).values(),
  ].sort((left, right) => left.date.localeCompare(right.date));
}

function historyPoints(stargazers, updated) {
  const starsByDate = new Map();
  for (const stargazer of stargazers) {
    const date = stargazer.starred_at?.slice(0, 10);
    if (!/^\d{4}-\d{2}-\d{2}$/.test(date ?? "")) {
      throw new Error("GitHub returned a stargazer without starred_at");
    }
    starsByDate.set(date, (starsByDate.get(date) ?? 0) + 1);
  }

  let stars = 0;
  const points = [...starsByDate.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([date, count]) => ({ date, stars: (stars += count) }));

  return mergePoint(points, { date: updated, stars });
}

function snapshotHistory(history, updated, stars) {
  return {
    ...history,
    updated,
    points: mergePoint(history.points, { date: updated, stars }),
  };
}

function validateHistory(history, repository, historyFile) {
  if (
    history.repository !== repository ||
    !/^\d{4}-\d{2}-\d{2}$/.test(history.updated ?? "") ||
    !Array.isArray(history.points) ||
    history.points.length === 0
  ) {
    throw new Error(
      `${historyFile} does not contain valid ${repository} history`,
    );
  }

  let previousDate = "";
  for (const point of history.points) {
    if (
      !/^\d{4}-\d{2}-\d{2}$/.test(point.date ?? "") ||
      !Number.isInteger(point.stars) ||
      point.stars < 0 ||
      point.date <= previousDate
    ) {
      throw new Error(`${historyFile} contains an invalid data point`);
    }
    previousDate = point.date;
  }
  return history;
}

function readHistory(historyFile, repository) {
  return validateHistory(
    JSON.parse(readFileSync(historyFile, "utf8")),
    repository,
    historyFile,
  );
}

function niceMaximum(value) {
  if (value <= 0) {
    return 1;
  }
  const magnitude = 10 ** Math.floor(Math.log10(value));
  return [1, 2, 5, 10]
    .map((step) => step * magnitude)
    .find((candidate) => candidate >= value);
}

function escapeXML(value) {
  return value.replace(
    /[&<>"']/g,
    (character) =>
      ({
        "&": "&amp;",
        "<": "&lt;",
        ">": "&gt;",
        '"': "&quot;",
        "'": "&apos;",
      })[character],
  );
}

function renderSVG(history) {
  const width = 960;
  const height = 520;
  const left = 80;
  const right = 32;
  const top = 94;
  const bottom = 64;
  const plotWidth = width - left - right;
  const plotHeight = height - top - bottom;

  const timestamps = history.points.map(({ date }) =>
    Date.parse(`${date}T00:00:00Z`),
  );
  const minimumTime = Math.min(...timestamps);
  const maximumTime = Math.max(...timestamps);
  const timeSpan = Math.max(maximumTime - minimumTime, DAY);
  const maximumStars = niceMaximum(
    Math.max(...history.points.map(({ stars }) => stars)),
  );
  const x = (timestamp) =>
    history.points.length === 1
      ? width - right
      : left + ((timestamp - minimumTime) / timeSpan) * plotWidth;
  const y = (stars) =>
    top + plotHeight - (stars / maximumStars) * plotHeight;

  const coordinates = history.points.map((point, index) => ({
    x: x(timestamps[index]),
    y: y(point.stars),
  }));
  const line = coordinates
    .map(
      (point, index) =>
        `${index === 0 ? "M" : "L"} ${point.x.toFixed(1)} ${point.y.toFixed(1)}`,
    )
    .join(" ");
  const area = `${line} L ${coordinates.at(-1).x.toFixed(1)} ${height - bottom} L ${coordinates[0].x.toFixed(1)} ${height - bottom} Z`;

  const number = new Intl.NumberFormat("en-US");
  const date = new Intl.DateTimeFormat("en-US", {
    day: "numeric",
    month: "short",
    year: "numeric",
    timeZone: "UTC",
  });
  const horizontalGrid = Array.from({ length: 6 }, (_, index) => {
    const stars = (maximumStars * index) / 5;
    const position = y(stars);
    return `<path class="grid" d="M ${left} ${position.toFixed(1)} H ${width - right}"/><text class="label" x="${left - 12}" y="${(position + 4).toFixed(1)}" text-anchor="end">${number.format(stars)}</text>`;
  }).join("");
  const verticalGrid = Array.from({ length: 5 }, (_, index) => {
    const timestamp = minimumTime + (timeSpan * index) / 4;
    const position = left + (plotWidth * index) / 4;
    const anchor = index === 0 ? "start" : index === 4 ? "end" : "middle";
    return `<path class="grid" d="M ${position.toFixed(1)} ${top} V ${height - bottom}"/><text class="label" x="${position.toFixed(1)}" y="${height - bottom + 28}" text-anchor="${anchor}">${date.format(timestamp)}</text>`;
  }).join("");

  const latest = history.points.at(-1);
  const escapedRepository = escapeXML(history.repository);
  return `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" role="img" aria-labelledby="title description">
  <title id="title">${escapedRepository} Star History</title>
  <desc id="description">GitHub stars by date, data through ${history.updated}</desc>
  <style>
    .background { fill: #ffffff; }
    .grid { fill: none; stroke: #d0d7de; stroke-width: 1; }
    .label, .subtitle { fill: #57606a; font: 13px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    .heading, .value { fill: #1f2328; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    .heading { font-size: 20px; font-weight: 600; }
    .value { font-size: 24px; font-weight: 600; }
    .area { fill: #006eff; opacity: .12; }
    .line { fill: none; stroke: #006eff; stroke-linecap: round; stroke-linejoin: round; stroke-width: 3; }
    .point { fill: #006eff; stroke: #ffffff; stroke-width: 2; }
    @media (prefers-color-scheme: dark) {
      .background { fill: #0d1117; }
      .grid { stroke: #30363d; }
      .label, .subtitle { fill: #8b949e; }
      .heading, .value { fill: #f0f6fc; }
      .area { fill: #58a6ff; }
      .line { stroke: #58a6ff; }
      .point { fill: #58a6ff; stroke: #0d1117; }
    }
  </style>
  <rect class="background" width="${width}" height="${height}" rx="8"/>
  <text class="heading" x="${left}" y="38">${escapedRepository} Star History</text>
  <text class="subtitle" x="${left}" y="62">GitHub stars by date · data through ${history.updated}</text>
  <text class="value" x="${width - right}" y="40" text-anchor="end">★ ${number.format(latest.stars)}</text>
  ${horizontalGrid}
  ${verticalGrid}
  <path class="area" d="${area}"/>
  <path class="line" d="${line}"/>
  <circle class="point" cx="${coordinates.at(-1).x.toFixed(1)}" cy="${coordinates.at(-1).y.toFixed(1)}" r="5"/>
</svg>
`;
}

function writeAtomic(file, content) {
  mkdirSync(path.dirname(file), { recursive: true });
  const temporaryFile = `${file}.tmp`;
  writeFileSync(temporaryFile, content);
  renameSync(temporaryFile, file);
}

function serializeHistory(history) {
  const points = history.points
    .map((point) => `    ${JSON.stringify(point)}`)
    .join(",\n");
  return `{
  "repository": ${JSON.stringify(history.repository)},
  "updated": ${JSON.stringify(history.updated)},
  "points": [
${points}
  ]
}
`;
}

function writeOutputs(history, historyFile, svgFile) {
  writeAtomic(historyFile, serializeHistory(history));
  writeAtomic(svgFile, renderSVG(history));
}

function bootstrap(repository, historyFile, svgFile) {
  const updated = new Date().toISOString().slice(0, 10);
  const history = {
    repository,
    updated,
    points: historyPoints(fetchStargazers(repository), updated),
  };
  writeOutputs(history, historyFile, svgFile);
  console.log(
    `bootstrapped ${history.points.at(-1).stars} stars through ${updated}`,
  );
}

function snapshot(repository, historyFile, svgFile) {
  const history = readHistory(historyFile, repository);
  const stars = fetchStarCount(repository);
  const updated = new Date().toISOString().slice(0, 10);
  const snapshot = snapshotHistory(history, updated, stars);
  writeOutputs(snapshot, historyFile, svgFile);
  console.log(
    `snapshotted ${snapshot.points.at(-1).stars} stars through ${updated}`,
  );
}

function selfTest() {
  assert.deepEqual(
    mergePoint([{ date: "2026-07-27", stars: 2 }], {
      date: "2026-07-27",
      stars: 3,
    }),
    [{ date: "2026-07-27", stars: 3 }],
  );
  assert.deepEqual(
    historyPoints(
      [
        { starred_at: "2026-07-26T01:00:00Z" },
        { starred_at: "2026-07-27T01:00:00Z" },
        { starred_at: "2026-07-27T02:00:00Z" },
      ],
      "2026-07-28",
    ),
    [
      { date: "2026-07-26", stars: 1 },
      { date: "2026-07-27", stars: 3 },
      { date: "2026-07-28", stars: 3 },
    ],
  );
  assert.deepEqual(
    snapshotHistory(
      {
        repository: "owner/repo",
        updated: "2026-07-27",
        points: [{ date: "2026-07-27", stars: 3 }],
      },
      "2026-07-28",
      3,
    ),
    {
      repository: "owner/repo",
      updated: "2026-07-28",
      points: [
        { date: "2026-07-27", stars: 3 },
        { date: "2026-07-28", stars: 3 },
      ],
    },
  );
  const svg = renderSVG({
    repository: "owner/repo",
    updated: "2026-07-28",
    points: [{ date: "2026-07-28", stars: 0 }],
  });
  assert.match(svg, /owner\/repo Star History/);
  assert.match(svg, /data through 2026-07-28/);
  console.log("star history self-test passed");
}

const [command, repository, historyFile, svgFile] = process.argv.slice(2);
if (command === "test") {
  selfTest();
  process.exit(0);
}
if (
  !["bootstrap", "snapshot"].includes(command) ||
  !/^[^/]+\/[^/]+$/.test(repository ?? "") ||
  !historyFile ||
  !svgFile
) {
  usage();
  process.exit(2);
}

if (command === "bootstrap") {
  bootstrap(repository, historyFile, svgFile);
} else {
  snapshot(repository, historyFile, svgFile);
}
