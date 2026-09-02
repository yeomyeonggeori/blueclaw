import { mkdir, mkdtemp, readFile, readdir, rename, rm, writeFile } from 'node:fs/promises';
import { basename, dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { buildProtocolArtifacts, serializeArtifact } from '../src/artifacts.ts';

const generatedDirectory = fileURLToPath(new URL('../generated/', import.meta.url));
const preservedGeneratedPaths = ['catalog.go'];

export async function generateProtocolArtifacts(targetDirectory = generatedDirectory): Promise<void> {
  const preservedDocuments = await readPreservedDocuments(targetDirectory);
  await replaceGeneratedDirectory(targetDirectory, async temporaryDirectory => {
    await writeProtocolArtifacts(temporaryDirectory);
    await writeDocuments(temporaryDirectory, preservedDocuments);
  });
}

export async function checkProtocolArtifacts(targetDirectory = generatedDirectory): Promise<void> {
  const expectedDocuments = buildProtocolArtifactDocuments();
  const actualPaths = (await listRelativeFilePaths(targetDirectory))
    .filter(relativePath => !preservedGeneratedPaths.includes(relativePath));
  const expectedPaths = [...expectedDocuments.keys()].sort();
  if (JSON.stringify(actualPaths) !== JSON.stringify(expectedPaths)) {
    throw new Error(`generated protocol paths differ: expected ${expectedPaths.join(', ')}, got ${actualPaths.join(', ')}`);
  }
  for (const [relativePath, expectedDocument] of expectedDocuments) {
    const actualDocument = await readFile(join(targetDirectory, relativePath), 'utf8');
    if (actualDocument !== expectedDocument) {
      throw new Error(`generated protocol artifact is stale: ${relativePath}`);
    }
  }
}

export async function replaceGeneratedDirectory(
  targetDirectory: string,
  populateDirectory: (temporaryDirectory: string) => Promise<void>,
): Promise<void> {
  const parentDirectory = dirname(targetDirectory);
  await mkdir(parentDirectory, { recursive: true });
  const temporaryDirectory = await mkdtemp(join(parentDirectory, `.${basename(targetDirectory)}-staging-`));
  const previousDirectory = `${temporaryDirectory}-previous`;
  let hasPreviousDirectory = false;

  try {
    await populateDirectory(temporaryDirectory);
    try {
      await rename(targetDirectory, previousDirectory);
      hasPreviousDirectory = true;
    } catch (errorValue) {
      if (!isNotFoundError(errorValue)) throw errorValue;
    }
    try {
      await rename(temporaryDirectory, targetDirectory);
    } catch (errorValue) {
      if (hasPreviousDirectory) await rename(previousDirectory, targetDirectory);
      throw errorValue;
    }
    if (hasPreviousDirectory) await rm(previousDirectory, { recursive: true });
  } finally {
    await rm(temporaryDirectory, { force: true, recursive: true });
  }
}

async function writeProtocolArtifacts(targetDirectory: string): Promise<void> {
  await writeDocuments(targetDirectory, buildProtocolArtifactDocuments());
}

async function writeDocuments(targetDirectory: string, documents: Map<string, string>): Promise<void> {
  await Promise.all([...documents].map(async ([relativePath, document]) => {
    const artifactPath = join(targetDirectory, relativePath);
    await mkdir(dirname(artifactPath), { recursive: true });
    await writeFile(artifactPath, document);
  }));
}

async function readPreservedDocuments(targetDirectory: string): Promise<Map<string, string>> {
  const documents = await Promise.all(preservedGeneratedPaths.map(async relativePath => {
    try {
      return [relativePath, await readFile(join(targetDirectory, relativePath), 'utf8')] as const;
    } catch (errorValue) {
      if (isNotFoundError(errorValue)) return undefined;
      throw errorValue;
    }
  }));
  return new Map(documents.filter(document => document !== undefined));
}

function buildProtocolArtifactDocuments(): Map<string, string> {
  const { manifest, schemas } = buildProtocolArtifacts();
  return new Map([
    ...schemas.map(({ fileName, schema }) => [join('json-schema', fileName), serializeArtifact(schema)] as const),
    ['manifest.json', serializeArtifact(manifest)],
  ]);
}

async function listRelativeFilePaths(targetDirectory: string, relativeDirectory = ''): Promise<string[]> {
  const directoryPath = join(targetDirectory, relativeDirectory);
  const entries = await readdir(directoryPath, { withFileTypes: true });
  const paths = await Promise.all(entries.map(async entry => {
    const relativePath = join(relativeDirectory, entry.name);
    if (!entry.isDirectory()) return [relativePath];
    return listRelativeFilePaths(targetDirectory, relativePath);
  }));
  return paths.flat().sort();
}

function isNotFoundError(errorValue: unknown): boolean {
  if (!errorValue || typeof errorValue !== 'object') return false;
  return 'code' in errorValue && errorValue.code === 'ENOENT';
}

function argumentValue(name: string): string | undefined {
  const argumentIndex = Bun.argv.indexOf(name);
  if (argumentIndex < 0) return undefined;
  return Bun.argv[argumentIndex + 1];
}

if (import.meta.main) {
  const targetDirectory = argumentValue('--target') ?? generatedDirectory;
  if (Bun.argv.includes('--check')) {
    await checkProtocolArtifacts(targetDirectory);
  } else {
    await generateProtocolArtifacts(targetDirectory);
  }
}
