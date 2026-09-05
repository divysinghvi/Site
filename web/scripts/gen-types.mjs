// Regenerates src/lib/api/types.gen.ts from ../schema/index.schema.json.
// The Go structs in internal/model are the source of truth; `make gen` runs
// `divy schemagen` and then this script, and `make gen-check` fails on drift.
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { compile } from 'json-schema-to-typescript';

const here = dirname(fileURLToPath(import.meta.url));
const input = resolve(here, '../../schema/index.schema.json');
const output = resolve(here, '../src/lib/api/types.gen.ts');

const schema = JSON.parse(await readFile(input, 'utf8'));
// The index schema has no root shape: every $defs entry becomes an exported type.
const ts = await compile(schema, 'ApiSchema', {
	bannerComment: [
		'/* eslint-disable */',
		'// Types derived from schema/index.schema.json (Go structs in internal/model).',
		'// Do not edit by hand: change the Go structs, then run `make gen`.'
	].join('\n'),
	unreachableDefinitions: true,
	additionalProperties: false,
	strictIndexSignatures: true,
	declareExternallyReferenced: true,
	style: { singleQuote: true, useTabs: true, printWidth: 100, trailingComma: 'none' }
});
await mkdir(dirname(output), { recursive: true });
await writeFile(output, ts);
console.log(`wrote ${output} (${ts.length} bytes)`);
