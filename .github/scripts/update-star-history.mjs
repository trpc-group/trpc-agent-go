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

function niceTickStep(value) {
  if (value <= 0) {
    return 1;
  }
  const rawStep = value / 9;
  const magnitude = 10 ** Math.floor(Math.log10(rawStep));
  const normalized = rawStep / magnitude;
  const multiplier = [1, 2, 2.5, 5, 10].find(
    (candidate) => candidate >= normalized,
  );
  return Math.max(1, multiplier * magnitude);
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
  const height = 650;
  const left = 112;
  const right = 48;
  const top = 142;
  const bottom = 88;
  const plotWidth = width - left - right;
  const plotHeight = height - top - bottom;

  const timestamps = history.points.map(({ date }) =>
    Date.parse(`${date}T00:00:00Z`),
  );
  const minimumTime = Math.min(...timestamps);
  const maximumTime = Math.max(...timestamps);
  const timeSpan = Math.max(maximumTime - minimumTime, DAY);
  const latestStars = Math.max(
    ...history.points.map(({ stars }) => stars),
  );
  const tickStep = niceTickStep(latestStars);
  const maximumStars = Math.max(
    tickStep,
    Math.ceil(latestStars / tickStep) * tickStep,
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

  const number = new Intl.NumberFormat("en-US");
  const month = new Intl.DateTimeFormat("en-US", {
    month: "short",
    timeZone: "UTC",
  });
  const year = new Intl.DateTimeFormat("en-US", {
    year: "numeric",
    timeZone: "UTC",
  });
  const horizontalTicks = Array.from(
    { length: Math.floor(maximumStars / tickStep) },
    (_, index) => {
      const stars = tickStep * (index + 1);
      const position = y(stars);
      return `<path class="tick" d="M ${left - 7} ${position.toFixed(1)} H ${left}"/><text class="label" x="${left - 18}" y="${(position + 6).toFixed(1)}" text-anchor="end">${number.format(stars)}</text>`;
    },
  ).join("");
  const verticalTicks = Array.from({ length: 5 }, (_, index) => {
    const timestamp = minimumTime + (timeSpan * index) / 4;
    const position = left + (plotWidth * index) / 4;
    const date = new Date(timestamp);
    let label = month.format(date);
    if (index === 0) {
      label = `${label} ${year.format(date)}`;
    } else if (date.getUTCMonth() === 0) {
      label = year.format(date);
    }
    return `<path class="tick" d="M ${position.toFixed(1)} ${height - bottom} V ${height - bottom + 7}"/><text class="label" x="${position.toFixed(1)}" y="${height - bottom + 32}" text-anchor="middle">${label}</text>`;
  }).join("");

  const latest = history.points.at(-1);
  const escapedRepository = escapeXML(history.repository);
  const legendWidth = Math.min(420, 72 + history.repository.length * 11);
  return `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" role="img" aria-labelledby="title description">
  <title id="title">${escapedRepository} Star History</title>
  <desc id="description">GitHub stars by date, data through ${history.updated}</desc>
  <defs>
    <filter id="rough" x="-4%" y="-4%" width="108%" height="108%">
      <feTurbulence type="fractalNoise" baseFrequency="0.012 0.045" numOctaves="1" seed="17" result="noise"/>
      <feDisplacementMap in="SourceGraphic" in2="noise" scale="1.8" xChannelSelector="R" yChannelSelector="G"/>
    </filter>
  </defs>
  <style>
    .background { fill: #ffffff; }
    .axis, .tick, .legend-box { fill: none; stroke: #151515; }
    .axis { stroke-linecap: round; stroke-linejoin: round; stroke-width: 3.5; }
    .tick { stroke-width: 2; }
    .legend-box { fill: #ffffff; stroke-width: 2.5; }
    .series { fill: none; stroke: #e64b2f; stroke-linecap: round; stroke-linejoin: round; stroke-width: 4; }
    .swatch { fill: #e64b2f; }
    .title, .label, .axis-title, .legend {
      fill: #151515;
      font-family: "Comic Sans MS", "Bradley Hand", "Segoe Print", "Chalkboard SE", cursive;
    }
    .title { font-size: 30px; font-weight: 700; }
    .label { font-size: 17px; }
    .axis-title { font-size: 21px; font-weight: 600; }
    .legend { font-size: 19px; }
    .meta {
      fill: #6e7781;
      font: 13px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    @media (prefers-color-scheme: dark) {
      .background { fill: #0d1117; }
      .axis, .tick, .legend-box { stroke: #f0f6fc; }
      .legend-box { fill: #161b22; }
      .title, .label, .axis-title, .legend { fill: #f0f6fc; }
      .series { stroke: #ff6b4a; }
      .swatch { fill: #ff6b4a; }
      .meta { fill: #8b949e; }
    }
  </style>
  <rect class="background" width="${width}" height="${height}"/>
  <text class="title" x="${width / 2}" y="54" text-anchor="middle">Star History</text>
  <g filter="url(#rough)">
    <path class="axis" d="M ${left} ${top} V ${height - bottom} H ${width - right}"/>
    ${horizontalTicks}
    ${verticalTicks}
    <rect class="legend-box" x="${left + 16}" y="${top + 18}" width="${legendWidth}" height="52" rx="6"/>
    <rect class="swatch" x="${left + 32}" y="${top + 36}" width="14" height="14" rx="2"/>
    <path class="series" d="${line}"/>
  </g>
  <text class="legend" x="${left + 58}" y="${top + 54}">${escapedRepository}</text>
  <text class="axis-title" x="${left + plotWidth / 2}" y="${height - 22}" text-anchor="middle">Date</text>
  <text class="axis-title" x="${-(top + plotHeight / 2)}" y="38" text-anchor="middle" transform="rotate(-90)">GitHub Stars</text>
  <text class="meta" x="${width - right}" y="${height - 22}" text-anchor="end">updated ${history.updated} · ${number.format(latest.stars)} stars</text>
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
  assert.equal(niceTickStep(1601), 200);
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
  assert.match(svg, /filter id="rough"/);
  assert.match(svg, /class="series"/);
  assert.doesNotMatch(svg, /class="area"/);
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
