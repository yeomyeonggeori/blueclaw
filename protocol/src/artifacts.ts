import { createHash } from 'node:crypto';

import { z } from 'zod';

import { createCanonicalJSONSchemaGenerationOverride } from './json_schema.ts';
import { protocolSchemas, protocolVersion } from './registry.ts';

export type SchemaArtifact = {
  fileName: string;
  hash: string;
  name: string;
  schema: Record<string, unknown>;
};

export type CatalogArtifactReference = {
  fileName: string;
  hash: string;
};

export type ProtocolManifest = {
  aggregateHash: string;
  capabilityToolCatalog?: CatalogArtifactReference;
  protocolVersion: string;
  schemas: Array<Pick<SchemaArtifact, 'fileName' | 'hash' | 'name'>>;
};

export function buildSchemaArtifacts(): SchemaArtifact[] {
  return Object.entries(protocolSchemas)
    .sort(([left], [right]) => compareCodeUnits(left, right))
    .map(([name, schema]) => buildSchemaArtifact(name, schema));
}

// The manifest is the protocol's identity, so how a set of artifacts hashes into
// one is written here even when a product authors artifacts of its own. A
// product that carries a tool catalog passes it in and gets a manifest the same
// reader accepts.
export function buildProtocolManifest(
  schemas: SchemaArtifact[],
  capabilityToolCatalog?: CatalogArtifactReference,
): ProtocolManifest {
  const artifactHashes = schemas.map(({ name, fileName, hash }) => `${name}:${fileName}:${hash}`);
  if (capabilityToolCatalog) {
    artifactHashes.push(`capability-tool-catalog:${capabilityToolCatalog.fileName}:${capabilityToolCatalog.hash}`);
  }
  return {
    aggregateHash: calculateArtifactHash(artifactHashes.join('\n')),
    ...(capabilityToolCatalog ? { capabilityToolCatalog } : {}),
    protocolVersion,
    schemas: schemas.map(withoutSchema),
  };
}

export function buildProtocolArtifacts() {
  const schemas = buildSchemaArtifacts();
  return { manifest: buildProtocolManifest(schemas), schemas };
}

export function serializeArtifact(value: unknown): string {
  return `${JSON.stringify(sortValue(value), null, 2)}\n`;
}

export function calculateArtifactHash(value: string): string {
  return createHash('sha256').update(value).digest('hex');
}

function buildSchemaArtifact(name: string, schema: z.ZodType): SchemaArtifact {
  const jsonSchema = z.toJSONSchema(schema, {
    override: createCanonicalJSONSchemaGenerationOverride(),
  }) as Record<string, unknown>;
  const document = {
    ...jsonSchema,
    $id: `https://schemas.blueclaw.dev/${protocolVersion}/${name}.schema.json`,
  };
  return {
    fileName: `${name}.schema.json`,
    hash: calculateArtifactHash(serializeArtifact(document)),
    name,
    schema: document,
  };
}

function withoutSchema({ fileName, hash, name }: SchemaArtifact) {
  return { fileName, hash, name };
}

function sortValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(sortValue);
  if (!value || typeof value !== 'object') return value;
  return Object.fromEntries(
    Object.entries(value)
      .sort(([left], [right]) => compareCodeUnits(left, right))
      .map(([key, entryValue]) => [key, sortValue(entryValue)]),
  );
}

function compareCodeUnits(left: string, right: string): number {
  if (left < right) return -1;
  if (left > right) return 1;
  return 0;
}
