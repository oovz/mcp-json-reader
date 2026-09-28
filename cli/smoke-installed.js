#!/usr/bin/env node
'use strict';

const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const readline = require('node:readline');

async function main() {
  const launcher = process.argv[2];
  assert.ok(launcher, 'pass the installed cli/mcp-json-reader.js path');
  assert.ok(fs.existsSync(launcher), `installed launcher does not exist: ${launcher}`);

  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'mcp-json-reader-smoke-'));
  const fixture = path.join(root, 'data.json');
  fs.writeFileSync(fixture, '[{"id":9007199254740993},{"id":2},{"id":3}]');

  const child = spawn(process.execPath, [launcher, '--root', root], {
    stdio: ['pipe', 'pipe', 'pipe'],
    windowsHide: true,
  });
  const output = readline.createInterface({ input: child.stdout, crlfDelay: Infinity });
  let stderr = '';
  child.stderr.setEncoding('utf8');
  child.stderr.on('data', (chunk) => { stderr += chunk; });
  const buffered = [];
  const readers = [];
  output.on('line', (line) => {
    const reader = readers.shift();
    if (reader) reader(line);
    else buffered.push(line);
  });

  let nextID = 1;
  let lastResponseText = '';
  async function request(method, params = {}) {
    const id = nextID++;
    const requestParams = {
      ...params,
      _meta: {
        'io.modelcontextprotocol/protocolVersion': '2026-07-28',
        'io.modelcontextprotocol/clientCapabilities': {},
      },
    };
    child.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', id, method, params: requestParams })}\n`);
    const deadline = Date.now() + 10000;
    while (Date.now() < deadline) {
      const line = buffered.length ? buffered.shift() : await Promise.race([
        new Promise((resolve) => readers.push(resolve)),
        new Promise((_, reject) => {
          const timer = setTimeout(() => reject(new Error(`timed out waiting for ${method} response`)), deadline - Date.now());
          timer.unref();
        }),
      ]);
      let response;
      try {
        response = JSON.parse(line);
      } catch (error) {
        throw new Error(`stdout contained a non-JSON frame: ${line}`, { cause: error });
      }
      lastResponseText = line;
      assert.equal(response.jsonrpc, '2.0', `JSON-RPC version for ${method}`);
      assert.equal(response.id, id, `response id for ${method}`);
      assert.equal(response.error, undefined, `protocol error for ${method}: ${JSON.stringify(response.error)}`);
      return response.result;
    }
    throw new Error(`timed out waiting for ${method} response`);
  }

  async function callTool(name, args) {
    return request('tools/call', { name, arguments: args });
  }

  try {
    const discovered = await request('server/discover');
    assert.deepEqual(discovered.supportedVersions, ['2026-07-28']);

    const listed = await request('tools/list');
    assert.deepEqual(listed.tools.map((tool) => tool.name).sort(), ['json_close', 'json_open', 'json_read']);

    const opened = await callTool('json_open', { path: 'data.json', format: 'json', validation: 'full' });
    assert.notEqual(opened.isError, true);
    const fileID = opened.structuredContent.file_id;
    assert.ok(fileID);

    const invalid = await callTool('json_read', {
      file_id: fileID,
      language: 'jsonpath',
      query: '$[*].id',
      unexpected: true,
    });
    assert.equal(invalid.isError, true);
    assert.equal(invalid.structuredContent.code, 'INVALID_ARGUMENT');

    const first = await callTool('json_read', {
      file_id: fileID,
      language: 'jsonpath',
      query: '$[*].id',
      max_items: 1,
    });
    assert.equal(first.structuredContent.complete, false);
    assert.equal(first.structuredContent.items[0].path, '/0/id');
    const cursor = first.structuredContent.next_cursor;
    assert.ok(cursor);
    assert.match(lastResponseText, /9007199254740993/);

    const second = await callTool('json_read', { cursor });
    assert.equal(second.structuredContent.complete, false);
    assert.equal(second.structuredContent.items[0].path, '/1/id');
    assert.equal(second.structuredContent.items[0].value, 2);
    const nextCursor = second.structuredContent.next_cursor;
    assert.ok(nextCursor);
    assert.notEqual(nextCursor, cursor);

    const third = await callTool('json_read', { cursor: nextCursor });
    assert.equal(third.structuredContent.complete, true);
    assert.equal(third.structuredContent.items[0].path, '/2/id');
    assert.equal(third.structuredContent.items[0].value, 3);

    const closed = await callTool('json_close', { file_id: fileID });
    assert.equal(closed.structuredContent.closed, true);

    child.stdin.end();
    const exit = await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error('server did not shut down after stdin closed')), 10000);
      child.once('error', reject);
      child.once('close', (code, signal) => {
        clearTimeout(timeout);
        resolve({ code, signal });
      });
    });
    assert.equal(exit.signal, null);
    assert.equal(exit.code, 0, `server exit status; stderr: ${stderr}`);
    assert.doesNotMatch(stderr, /"jsonrpc"\s*:/, 'protocol responses must stay on stdout');
    process.stdout.write('installed MCP smoke passed\n');
  } catch (error) {
    child.kill('SIGTERM');
    throw error;
  } finally {
    output.close();
    fs.rmSync(root, { recursive: true, force: true });
  }
}

main().catch((error) => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
