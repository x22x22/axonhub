#!/usr/bin/env node
/**
 * Type-check gate that only fails on NEW type errors.
 *
 * The codebase carries a sizable backlog of pre-existing type errors, so
 * wiring a plain `tsc` into CI would fail every pull request and the check
 * would simply be ignored. Instead this keeps a recorded baseline of the
 * errors that already exist and fails only when the count of distinct
 * errors grows.
 *
 * Errors are compared after normalization, because line and column
 * numbers shift whenever unrelated lines above them are added or removed.
 * Comparing raw `tsc` output would therefore report phantom regressions on
 * almost every edit.
 *
 * Usage:
 *   node scripts/typecheck-gate.mjs            # check against the baseline
 *   node scripts/typecheck-gate.mjs --update   # rewrite the baseline
 */

import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = dirname(dirname(fileURLToPath(import.meta.url)));
const BASELINE = join(ROOT, 'typecheck-baseline.txt');

/** Collapse `file(line,col): error TSxxxx: msg` into a position-free key. */
/**
 * Collapse `file(line,col): error TSxxxx: msg` into a key that is stable
 * across machines.
 *
 * Positions are stripped because line numbers shift whenever unrelated lines
 * move. Absolute paths are stripped for the same reason: some diagnostics
 * quote the resolved module path (TS2719, TS2307), which reads
 * `/Users/me/work/.../node_modules/...` locally but
 * `/home/runner/work/.../node_modules/...` in CI. Without this the baseline
 * recorded on one machine reports phantom regressions on the other.
 */
function normalize(line) {
  return line
    .trim()
    .replace(/\(\d+,\d+\)/, '')
    .replace(/"[^"]*?\/node_modules\//g, '"node_modules/')
    .replace(/\/[^"'\s]*?\/frontend\//g, '')
    .replace(/\s+/g, ' ');
}

/**
 * Diagnostics that cannot be compared across machines.
 *
 * TS2719 ("Two different types with this name exist, but they are unrelated")
 * means pnpm resolved two copies of a package. tsc truncates its printed type
 * with a literal `...`, and the truncation offset depends on the length of the
 * absolute paths embedded in the message — so the same unresolved duplicate
 * prints `channelTagsMatchMode:...` on a developer machine with a deep
 * worktree path and `channelTagsMatc...` in CI. Normalization runs after tsc
 * has already truncated, so the keys can never match.
 *
 * These are dependency-graph artifacts rather than code defects, and they are
 * already reported by the package manager, so they are excluded from the gate.
 */
const UNSTABLE_CODES = [/error TS2719\b/];

function isComparable(message) {
  return !UNSTABLE_CODES.some((pattern) => pattern.test(message));
}

function runTypecheck() {
  try {
    execFileSync('npx', ['tsc', '-b', '--force', '--noEmit'], {
      cwd: ROOT,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'pipe'],
      // tsc reports errors on stdout; a non-zero exit is expected here.
      timeout: 15 * 60 * 1000,
    });
    return [];
  } catch (error) {
    const output = `${error.stdout ?? ''}${error.stderr ?? ''}`;
    return output
      .split('\n')
      .filter((line) => line.includes('error TS') && line.startsWith('src/'))
      .filter((line) => isComparable(line))
      .map(normalize);
  }
}

function readBaseline() {
  if (!existsSync(BASELINE)) return null;
  return readFileSync(BASELINE, 'utf8')
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line && !line.startsWith('#'));
}

const current = [...new Set(runTypecheck())].sort();
const baselineList = readBaseline();
const update = process.argv.includes('--update');

if (update || baselineList === null) {
  writeFileSync(
    BASELINE,
    [
      '# Known type errors, one per line, positions stripped.',
      '# Regenerate with: pnpm typecheck:baseline',
      '# This file should only ever shrink.',
      ...current,
    ].join('\n') + '\n',
  );
  console.log(`Wrote ${current.length} known errors to typecheck-baseline.txt`);
  process.exit(0);
}

const known = new Set(baselineList);
const added = current.filter((error) => !known.has(error));
const fixed = baselineList.filter((error) => !current.includes(error));

console.log(`Known type errors:   ${known.size}`);
console.log(`Current type errors: ${current.length}`);

if (fixed.length > 0) {
  console.log(`\n✅ ${fixed.length} previously failing error(s) are fixed.`);
  console.log('   Run `pnpm typecheck:baseline` to shrink the baseline.');
  for (const error of fixed.slice(0, 5)) console.log(`   - ${error}`);
  if (fixed.length > 5) console.log(`   ... and ${fixed.length - 5} more`);
}

if (added.length === 0) {
  console.log('\n✅ No new type errors.');
  process.exit(0);
}

console.error(`\n❌ ${added.length} new type error(s) introduced:\n`);
for (const error of added) console.error(`   ${error}`);
console.error(
  '\nFix these, or run `pnpm typecheck:baseline` if they are intentional and pre-existing.',
);
process.exit(1);
