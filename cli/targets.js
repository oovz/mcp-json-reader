'use strict';

const TARGETS = Object.freeze([
  Object.freeze({ platform: 'linux', arch: 'x64', directory: 'linux-amd64', file: 'mcp-json-reader', goos: 'linux', goarch: 'amd64' }),
  Object.freeze({ platform: 'linux', arch: 'arm64', directory: 'linux-arm64', file: 'mcp-json-reader', goos: 'linux', goarch: 'arm64' }),
  Object.freeze({ platform: 'darwin', arch: 'x64', directory: 'darwin-amd64', file: 'mcp-json-reader', goos: 'darwin', goarch: 'amd64' }),
  Object.freeze({ platform: 'darwin', arch: 'arm64', directory: 'darwin-arm64', file: 'mcp-json-reader', goos: 'darwin', goarch: 'arm64' }),
  Object.freeze({ platform: 'win32', arch: 'x64', directory: 'windows-amd64', file: 'mcp-json-reader.exe', goos: 'windows', goarch: 'amd64' }),
  Object.freeze({ platform: 'win32', arch: 'arm64', directory: 'windows-arm64', file: 'mcp-json-reader.exe', goos: 'windows', goarch: 'arm64' }),
]);

const TARGET_BY_RUNTIME = new Map(TARGETS.map((target) => [`${target.platform}/${target.arch}`, target]));

function targetFor(platform, arch) {
  return TARGET_BY_RUNTIME.get(`${platform}/${arch}`) || null;
}

module.exports = { TARGETS, targetFor };
