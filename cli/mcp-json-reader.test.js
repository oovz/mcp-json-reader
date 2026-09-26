const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { EventEmitter } = require('node:events');

const launcher = require('./mcp-json-reader.js');

test('maps each supported platform and architecture to a native binary', () => {
  const cases = [
    ['linux', 'x64', 'linux-amd64', 'mcp-json-reader', 'linux', 'amd64'],
    ['linux', 'arm64', 'linux-arm64', 'mcp-json-reader', 'linux', 'arm64'],
    ['darwin', 'x64', 'darwin-amd64', 'mcp-json-reader', 'darwin', 'amd64'],
    ['darwin', 'arm64', 'darwin-arm64', 'mcp-json-reader', 'darwin', 'arm64'],
    ['win32', 'x64', 'windows-amd64', 'mcp-json-reader.exe', 'windows', 'amd64'],
    ['win32', 'arm64', 'windows-arm64', 'mcp-json-reader.exe', 'windows', 'arm64'],
  ];
  for (const [platform, arch, directory, file, goos, goarch] of cases) {
    assert.deepEqual(launcher.targetFor(platform, arch), { platform, arch, directory, file, goos, goarch });
  }
});

test('reports unsupported runtimes without selecting a fallback binary', () => {
  assert.equal(launcher.targetFor('freebsd', 'x64'), null);
  assert.equal(launcher.targetFor('linux', 'ia32'), null);
});

test('builds a package-relative native path', () => {
  assert.match(launcher.binaryPath(undefined, 'win32', 'x64'), /binaries[\\/]windows-amd64[\\/]mcp-json-reader\.exe$/);
});

test('reports unsupported platforms through the launcher error path', async () => {
  let stderr = '';
  const code = await launcher.run([], { platform: 'freebsd', arch: 'x64', stderr: { write: (message) => { stderr += message; } } });
  assert.equal(code, 1);
  assert.match(stderr, /unsupported platform or architecture/);
});

test('preserves arguments and the native exit code', async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'mcp-launcher-'));
  const executable = path.join(root, 'binaries', 'windows-amd64', 'mcp-json-reader.exe');
  fs.mkdirSync(path.dirname(executable), { recursive: true });
  fs.writeFileSync(executable, 'binary');
  try {
    let received;
    const code = await launcher.run(['--root', 'C:\\path with spaces'], {
      platform: 'win32',
      arch: 'x64',
      packageRoot: root,
      spawn: (file, args, options) => {
        received = { file, args, options };
        const child = new EventEmitter();
        child.killed = false;
        child.kill = () => { child.killed = true; };
        process.nextTick(() => child.emit('close', 23, null));
        return child;
      },
    });
    assert.equal(code, 23);
    assert.deepEqual(received.args, ['--root', 'C:\\path with spaces']);
    assert.equal(received.options.stdio, 'inherit');
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test('maps native signal termination to the conventional shell exit code', async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'mcp-launcher-signal-'));
  const executable = path.join(root, 'binaries', 'windows-amd64', 'mcp-json-reader.exe');
  fs.mkdirSync(path.dirname(executable), { recursive: true });
  fs.writeFileSync(executable, 'binary');
  try {
    const code = await launcher.run([], {
      platform: 'win32',
      arch: 'x64',
      packageRoot: root,
      spawn: () => {
        const child = new EventEmitter();
        child.killed = false;
        child.kill = () => { child.killed = true; };
        process.nextTick(() => child.emit('close', null, 'SIGTERM'));
        return child;
      },
    });
    assert.equal(code, 143);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
