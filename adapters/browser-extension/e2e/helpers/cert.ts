// Generates a throwaway self-signed cert (via openssl) covering the chat-site
// hostnames, so the replay server can answer https://claude.ai / https://chatgpt.com
// after --host-resolver-rules maps those hosts to it. Test-only; never trusted
// beyond the automation Chromium launched with --ignore-certificate-errors.

import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';

let cached: { key: string; cert: string } | undefined;

export function getTestCert(): { key: string; cert: string } {
  if (cached) return cached;
  const dir = mkdtempSync(path.join(os.tmpdir(), 'threadforge-test-cert-'));
  const keyPath = path.join(dir, 'key.pem');
  const certPath = path.join(dir, 'cert.pem');
  const configPath = path.join(dir, 'openssl.cnf');
  writeFileSync(configPath, [
    '[ req ]',
    'prompt = no',
    'distinguished_name = req_dn',
    'x509_extensions = v3_ca',
    '[ req_dn ]',
    'CN = claude.ai',
    '[ v3_ca ]',
    'basicConstraints = critical, CA:TRUE',
    'keyUsage = critical, digitalSignature, keyEncipherment, keyCertSign',
    'subjectAltName = @alt_names',
    '[ alt_names ]',
    'DNS.1 = claude.ai',
    'DNS.2 = chatgpt.com',
    'DNS.3 = chat.openai.com',
    'DNS.4 = localhost',
    'IP.1 = 127.0.0.1',
    '',
  ].join('\n'), { mode: 0o600 });
  execFileSync('openssl', [
    'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
    '-keyout', keyPath,
    '-out', certPath,
    '-days', '3',
    '-config', configPath,
  ]);
  cached = { key: readFileSync(keyPath, 'utf8'), cert: readFileSync(certPath, 'utf8') };
  return cached;
}
