type JSONObject = { [key: string]: unknown };

function isJSONObject(value: unknown): value is JSONObject {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function isNullSchema(schema: unknown): boolean {
  return isJSONObject(schema) && schema.type === 'null' && Object.keys(schema).length === 1;
}

function acceptsNull(schema: unknown): boolean {
  if (!isJSONObject(schema)) return false;
  if (schema.type === 'null') return true;
  if (Array.isArray(schema.type) && schema.type.includes('null')) return true;
  return ['anyOf', 'oneOf'].some(key => {
    const alternatives = schema[key];
    return Array.isArray(alternatives) && alternatives.some(isNullSchema);
  });
}

function withoutNullAlternative(schema: unknown): unknown {
  if (!isJSONObject(schema)) return schema;
  if (Array.isArray(schema.type) && schema.type.includes('null')) {
    const remaining = schema.type.filter(name => name !== 'null');
    return { ...schema, type: remaining.length === 1 ? remaining[0] : remaining };
  }
  for (const key of ['anyOf', 'oneOf']) {
    const alternatives = schema[key];
    if (!Array.isArray(alternatives) || !alternatives.some(isNullSchema)) continue;
    const remaining = alternatives.filter(alternative => !isNullSchema(alternative));
    if (remaining.length !== 1) return { ...schema, [key]: remaining };
    const rest = Object.fromEntries(Object.entries(schema).filter(([name]) => name !== key));
    const only = isJSONObject(remaining[0]) ? remaining[0] : {};
    return { ...rest, ...only };
  }
  return schema;
}

function propertiesWithNullAsAbsent(properties: JSONObject): JSONObject {
  return Object.fromEntries(
    Object.entries(properties).map(([name, property]) => [name, withoutNullAlternative(schemaWithNullAsAbsent(property))]),
  );
}

export function schemaWithNullAsAbsent(schema: unknown): unknown {
  if (!isJSONObject(schema)) return schema;
  const normalized: JSONObject = { ...schema };
  for (const key of ['items', 'additionalProperties', 'not']) {
    if (key in schema) normalized[key] = schemaWithNullAsAbsent(schema[key]);
  }
  for (const key of ['anyOf', 'oneOf', 'allOf']) {
    const alternatives = schema[key];
    if (Array.isArray(alternatives)) normalized[key] = alternatives.map(schemaWithNullAsAbsent);
  }
  for (const key of ['$defs', 'definitions']) {
    const definitions = schema[key];
    if (isJSONObject(definitions)) normalized[key] = propertiesWithNullAsAbsent(definitions);
  }
  const properties = schema.properties;
  if (!isJSONObject(properties)) return normalized;
  normalized.properties = propertiesWithNullAsAbsent(properties);
  if (Array.isArray(schema.required)) {
    normalized.required = schema.required.filter(name => typeof name !== 'string' || !acceptsNull(properties[name]));
  }
  return normalized;
}

export function documentWithNullAsAbsent(document: unknown): unknown {
  if (Array.isArray(document)) return document.map(documentWithNullAsAbsent);
  if (!isJSONObject(document)) return document;
  return Object.fromEntries(
    Object.entries(document)
      .filter(([, value]) => value !== null)
      .map(([key, value]) => [key, documentWithNullAsAbsent(value)]),
  );
}
