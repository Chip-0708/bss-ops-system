import { readdir, mkdir } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import { build } from "esbuild";

const files = await readdir("tests");
await mkdir(".qa/tests", { recursive: true });
const scenarios = files.filter(file => file.endsWith(".scenario.ts"));
for (const file of scenarios) {
  await build({ entryPoints: [`tests/${file}`], outfile: `.qa/tests/${file.replace(/\.ts$/, ".mjs")}`,
    bundle: true, platform: "node", format: "esm", packages: "external",
    define: { "import.meta.env": JSON.stringify({ DEV: true, VITE_AUTH_ENABLED: file === "auth.scenario.ts" ? "true" : "false", VITE_MOCK_ENABLED: "false", VITE_API_BASE_URL: "http://localhost/api" }) },
  });
}
const result = spawnSync(process.execPath, ["--experimental-strip-types", "--test",
  ...files.filter(file => file.endsWith(".test.ts")).map(file => `tests/${file}`),
  ...scenarios.map(file => `.qa/tests/${file.replace(/\.ts$/, ".mjs")}`),
], { stdio: "inherit" });
process.exitCode = result.status ?? 1;
