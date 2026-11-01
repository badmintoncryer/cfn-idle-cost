#!/usr/bin/env node
// Runs the platform binary installed via optionalDependencies.
const { spawnSync } = require('child_process');
const { binaryPath } = require('..');
let bin;
try {
  bin = binaryPath();
} catch (e) {
  console.error(e.message);
  process.exit(1);
}
const r = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit' });
if (r.error) {
  console.error(`cfn-idle-cost: could not run ${bin}: ${r.error.message}`);
}
process.exit(r.status ?? 1);
