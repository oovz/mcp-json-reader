#!/usr/bin/env node
'use strict';

const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');

const { targetFor } = require('./targets.js');

const SIGNAL_EXIT_CODES = Object.freeze({ SIGINT: 130, SIGTERM: 143, SIGHUP: 129 });

function binaryPath(packageRoot = __dirname, platform = process.platform, arch = process.arch) {
  const target = targetFor(platform, arch);
  return target ? path.join(packageRoot, 'binaries', target.directory, target.file) : null;
}

function writeError(message, stderr = process.stderr) {
  stderr.write(`mcp-json-reader: ${message}\n`);
}

function run(args = process.argv.slice(2), options = {}) {
  const platform = options.platform || process.platform;
  const arch = options.arch || process.arch;
  const target = targetFor(platform, arch);
  const stderr = options.stderr || process.stderr;
  if (!target) {
    writeError(`unsupported platform or architecture: ${platform}/${arch}`, stderr);
    return Promise.resolve(1);
  }

  const packageRoot = options.packageRoot || __dirname;
  const executable = binaryPath(packageRoot, platform, arch);
  try {
    const stat = fs.statSync(executable);
    if (!stat.isFile()) {
      throw new Error('not a regular file');
    }
    if (platform !== 'win32' && (stat.mode & 0o111) === 0) {
      throw new Error('not executable');
    }
  } catch (error) {
    writeError(`native executable is unavailable for ${platform}/${arch}: ${error.message}`, stderr);
    return Promise.resolve(1);
  }

  const spawnProcess = options.spawn || spawn;
  let child;
  try {
    child = spawnProcess(executable, args, { stdio: 'inherit', windowsHide: true });
  } catch (error) {
    writeError(`cannot start native executable: ${error.message}`, stderr);
    return Promise.resolve(1);
  }

  return new Promise((resolve) => {
    let settled = false;
    const signals = process.platform === 'win32' ? ['SIGINT', 'SIGTERM'] : ['SIGINT', 'SIGTERM', 'SIGHUP'];
    const forward = (signal) => {
      if (!child.killed) {
        child.kill(signal);
      }
    };
    const cleanup = () => {
      for (const signal of signals) {
        process.removeListener(signal, forward);
      }
    };
    const finish = (code) => {
      if (settled) {
        return;
      }
      settled = true;
      cleanup();
      resolve(code);
    };
    for (const signal of signals) {
      process.on(signal, forward);
    }
    child.once('error', (error) => {
      writeError(`native executable failed to start: ${error.message}`, stderr);
      finish(1);
    });
    child.once('close', (code, signal) => {
      finish(signal ? (SIGNAL_EXIT_CODES[signal] || 1) : (Number.isInteger(code) ? code : 1));
    });
  });
}

async function main(args = process.argv.slice(2)) {
  process.exitCode = await run(args);
}

if (require.main === module) {
  main().catch((error) => {
    writeError(error.message);
    process.exitCode = 1;
  });
}

module.exports = { binaryPath, run, targetFor };
