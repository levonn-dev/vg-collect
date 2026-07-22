// Regenerates the brand PNGs from the SVG sources in this directory.
// Run from anywhere: node docs/brand/render.mjs
// Needs frontend/ npm deps installed plus the playwright chromium
// browser (cd frontend && npm ci && npx playwright install chromium).
import { createRequire } from "node:module";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const brandDir = dirname(fileURLToPath(import.meta.url));
const publicDir = join(brandDir, "..", "..", "frontend", "public");

const require = createRequire(join(publicDir, "..", "package.json"));
const { chromium } = require("@playwright/test");

const browser = await chromium.launch();

// Square-tile renders keep the corners outside the tile transparent.
async function renderIcon(svgPath, outPath, size) {
  const page = await browser.newPage({ viewport: { width: size, height: size } });
  await page.goto("file://" + svgPath);
  await page.screenshot({ path: outPath, omitBackground: true });
  await page.close();
}

// Lockup renders sit on an explicit surface color with padding; the
// wordmark uses currentColor, so the surface sets its ink.
async function renderLockup(outPath, background, color) {
  const svg = readFileSync(join(brandDir, "lockup.svg"), "utf8").replace(
    "<svg ",
    '<svg style="display:block;width:400px;height:90px" ',
  );
  const page = await browser.newPage({
    viewport: { width: 440, height: 130 },
    deviceScaleFactor: 4,
  });
  await page.setContent(
    `<body style="margin:0;background:${background};color:${color}"><div style="padding:20px">${svg}</div></body>`,
  );
  await page.screenshot({ path: outPath });
  await page.close();
}

await renderIcon(join(brandDir, "logo-square.svg"), join(brandDir, "icon-512.png"), 512);
await renderIcon(join(publicDir, "favicon.svg"), join(publicDir, "favicon-32.png"), 32);
await renderIcon(join(publicDir, "favicon.svg"), join(publicDir, "apple-touch-icon.png"), 180);
await renderLockup(join(brandDir, "lockup-light.png"), "#ffffff", "#111827");
await renderLockup(join(brandDir, "lockup-dark.png"), "#030712", "#f9fafb");

await browser.close();
console.log("brand PNGs regenerated");
