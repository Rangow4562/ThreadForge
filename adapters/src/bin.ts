#!/usr/bin/env node
import { runAdapterCLI } from './cli.ts';

process.exitCode = await runAdapterCLI(process.argv.slice(2), process.stdout, process.stderr);
