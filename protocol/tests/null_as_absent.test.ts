import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { documentWithNullAsAbsent, schemaWithNullAsAbsent } from '../src/null_as_absent.ts';

const casesPath = fileURLToPath(new URL('../../.dependency/bluecollar/toolcontract/testdata/null-as-absent.json', import.meta.url));

type NullAsAbsentCases = {
  schemas: Array<{ schema: unknown; absentWhenNull: unknown }>;
  documents: Array<{ document: unknown; absentWhenNull: unknown }>;
};

function isNullAsAbsentCases(value: unknown): value is NullAsAbsentCases {
  return typeof value === 'object' && value !== null && 'schemas' in value && 'documents' in value;
}

describe('null is read as a field left out', () => {
  test('the way bluecollar reads it', () => {
    const cases: unknown = JSON.parse(readFileSync(casesPath, 'utf8'));
    if (!isNullAsAbsentCases(cases)) throw new Error(`${casesPath} holds no null-as-absent cases`);
    for (const schemaCase of cases.schemas) {
      expect(schemaWithNullAsAbsent(schemaCase.schema)).toEqual(schemaCase.absentWhenNull);
    }
    for (const documentCase of cases.documents) {
      expect(documentWithNullAsAbsent(documentCase.document)).toEqual(documentCase.absentWhenNull);
    }
  });
});
