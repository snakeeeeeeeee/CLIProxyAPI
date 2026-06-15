import { copyFileSync, existsSync } from "node:fs";
import { resolve } from "node:path";

const built = resolve("dist/index.html");
const target = resolve("../../internal/resourcepool/console.html");

if (!existsSync(built)) {
  throw new Error(`missing built console: ${built}`);
}

copyFileSync(built, target);
console.log(`resource console copied to ${target}`);
