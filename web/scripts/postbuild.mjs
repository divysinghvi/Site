// adapter-static empties ../internal/web/dist before writing; restore the
// committed placeholder so a build never shows up as a deleted file in git.
import { writeFile, access } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const dist = resolve(dirname(fileURLToPath(import.meta.url)), '../../internal/web/dist');
const keep = resolve(dist, '.gitkeep');
try {
	await access(keep);
} catch {
	await writeFile(keep, '');
}
