import { z } from 'zod';

export enum ExecutionMode {
  Device = 'device',
  Remote = 'remote',
  Auto = 'auto',
}

export const protocolIdentitySchema = z.strictObject({
  protocolVersion: z.string().trim().min(1),
  aggregateProtocolHash: z.string().regex(/^[a-f0-9]{64}$/),
});

export const jsonValueSchema = z.json();

export const nonNegativeIntegerSchema = z.number().int().nonnegative();

export const resourceScopeSchema = z.looseObject({
  kind: z.string().optional(),
  value: z.string().optional(),
});

export type JsonValue = z.infer<typeof jsonValueSchema>;
export type ProtocolIdentity = z.infer<typeof protocolIdentitySchema>;
export type ResourceScope = z.infer<typeof resourceScopeSchema>;
