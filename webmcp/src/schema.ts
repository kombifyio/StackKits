import { TOOL_DATA_SCHEMAS, TOOL_INPUT_SCHEMAS, WEBMCP_SCHEMA_DEFINITIONS } from "./generated/tool-schemas.js";
import type { ToolInputMap, ToolName, WebMcpResult } from "./types.js";

type Schema = Record<string, unknown>;

export interface SchemaIssue {
  path: string;
  keyword: string;
}

export interface SchemaValidation {
  valid: boolean;
  issues: SchemaIssue[];
}

const DEFINITIONS = WEBMCP_SCHEMA_DEFINITIONS as unknown as Record<string, Schema>;

/** The exact input schemas registered with WebMCP are used for local checks. */
export const TOOL_INPUT_SCHEMAS_PUBLIC = TOOL_INPUT_SCHEMAS as unknown as Record<ToolName, Schema>;
export const TOOL_DATA_SCHEMAS_PUBLIC = TOOL_DATA_SCHEMAS as unknown as Record<ToolName, Schema>;

export function validateToolInput<T extends ToolName>(tool: T, input: unknown): SchemaValidation {
  const schema = TOOL_INPUT_SCHEMAS_PUBLIC[tool];
  if (!schema) {
    return { valid: false, issues: [{ path: "tool", keyword: "unknown_tool" }] };
  }
  return validateSchema(schema, input, "$", new Set());
}

export function validateToolInputTyped<T extends ToolName>(tool: T, input: ToolInputMap[T]): SchemaValidation {
  return validateToolInput(tool, input);
}

/** The same public schemas validate every successful or populated result. */
export function validateToolResult<T extends ToolName>(tool: T, result: WebMcpResult<unknown>): SchemaValidation {
  const resultSchema = DEFINITIONS.result;
  if (!resultSchema) return { valid: false, issues: [{ path: "$", keyword: "missing_result_schema" }] };
  const envelope = validateSchema(resultSchema, result);
  const issues = [...envelope.issues];
  if (result.tool !== tool) issues.push({ path: "$.tool", keyword: "const" });
  const hasData = isRecord(result.data) && Object.keys(result.data).length > 0;
  if (result.outcome === "success" || hasData) {
    const dataSchema = TOOL_DATA_SCHEMAS_PUBLIC[tool];
    if (!dataSchema) issues.push({ path: "$.data", keyword: "unknown_tool" });
    else issues.push(...validateSchema(dataSchema, result.data, "$.data").issues);
  }
  return { valid: issues.length === 0, issues };
}

export function validateSchema(
  schema: Schema,
  value: unknown,
  path = "$",
  seen = new Set<string>(),
  definitions: Record<string, Schema> = DEFINITIONS,
): SchemaValidation {
  if (typeof schema.$ref === "string") {
    const prefix = "#/$defs/";
    if (!schema.$ref.startsWith(prefix)) {
      return { valid: false, issues: [{ path, keyword: "unsupported_ref" }] };
    }
    const name = schema.$ref.slice(prefix.length);
    const definition = definitions[name];
    if (!definition) {
      return { valid: false, issues: [{ path, keyword: "unknown_ref" }] };
    }
    if (seen.has(name)) {
      return { valid: true, issues: [] };
    }
    const nextSeen = new Set(seen);
    nextSeen.add(name);
    return validateSchema(definition, value, path, nextSeen, definitions);
  }

  const issues: SchemaIssue[] = [];
  const type = schema.type;
  if (typeof type === "string" && !matchesType(type, value)) {
    issues.push({ path, keyword: "type" });
    return { valid: false, issues };
  }
  if (Array.isArray(type) && !type.some((candidate) => typeof candidate === "string" && matchesType(candidate, value))) {
    issues.push({ path, keyword: "type" });
    return { valid: false, issues };
  }
  if ("const" in schema && !sameJson(schema.const, value)) {
    issues.push({ path, keyword: "const" });
  }
  if (Array.isArray(schema.enum) && !schema.enum.some((candidate) => sameJson(candidate, value))) {
    issues.push({ path, keyword: "enum" });
  }
  if (typeof value === "string") {
    if (typeof schema.minLength === "number" && value.length < schema.minLength) issues.push({ path, keyword: "minLength" });
    if (typeof schema.maxLength === "number" && value.length > schema.maxLength) issues.push({ path, keyword: "maxLength" });
    if (typeof schema.pattern === "string") {
      let matches = false;
      try {
        matches = new RegExp(schema.pattern).test(value);
      } catch {
        issues.push({ path, keyword: "pattern_schema" });
      }
      if (!matches) issues.push({ path, keyword: "pattern" });
    }
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) issues.push({ path, keyword: "finite" });
    if (typeof schema.minimum === "number" && value < schema.minimum) issues.push({ path, keyword: "minimum" });
    if (typeof schema.maximum === "number" && value > schema.maximum) issues.push({ path, keyword: "maximum" });
    if (typeof schema.exclusiveMinimum === "number" && value <= schema.exclusiveMinimum) issues.push({ path, keyword: "exclusiveMinimum" });
    if (schema.exclusiveMinimum === true && value <= 0) issues.push({ path, keyword: "exclusiveMinimum" });
    if (schema.type === "integer" && !Number.isInteger(value)) issues.push({ path, keyword: "integer" });
  }
  if (Array.isArray(value)) {
    if (typeof schema.minItems === "number" && value.length < schema.minItems) issues.push({ path, keyword: "minItems" });
    if (typeof schema.maxItems === "number" && value.length > schema.maxItems) issues.push({ path, keyword: "maxItems" });
    if (schema.uniqueItems === true) {
      for (let index = 0; index < value.length; index += 1) {
        if (value.findIndex((candidate) => sameJson(candidate, value[index])) !== index) {
          issues.push({ path, keyword: "uniqueItems" });
          break;
        }
      }
    }
    if (Array.isArray(schema.prefixItems)) {
      for (let index = 0; index < Math.min(value.length, schema.prefixItems.length); index += 1) {
        const prefixSchema = schema.prefixItems[index];
        if (isRecord(prefixSchema)) issues.push(...validateSchema(prefixSchema, value[index], `${path}[${index}]`, seen, definitions).issues);
      }
      if (schema.items === false && value.length !== schema.prefixItems.length) issues.push({ path, keyword: "items" });
    } else if (schema.items && typeof schema.items === "object") {
      for (let index = 0; index < value.length; index += 1) {
      const itemResult = validateSchema(schema.items as Schema, value[index], `${path}[${index}]`, seen, definitions);
        issues.push(...itemResult.issues);
      }
    }
  }
  if (isRecord(value)) {
    const properties = isRecord(schema.properties) ? schema.properties : {};
    const patternProperties = isRecord(schema.patternProperties) ? schema.patternProperties : {};
    const required = Array.isArray(schema.required) ? schema.required.filter((entry): entry is string => typeof entry === "string") : [];
    if (typeof schema.minProperties === "number" && Object.keys(value).length < schema.minProperties) issues.push({ path, keyword: "minProperties" });
    if (typeof schema.maxProperties === "number" && Object.keys(value).length > schema.maxProperties) issues.push({ path, keyword: "maxProperties" });
    for (const key of required) {
      if (!(key in value)) issues.push({ path: `${path}.${key}`, keyword: "required" });
    }
    if (schema.additionalProperties === false) {
      for (const key of Object.keys(value)) {
        if (!(key in properties) && matchingPatternSchemas(patternProperties, key).length === 0) {
          issues.push({ path: `${path}.${key}`, keyword: "additionalProperties" });
        }
      }
    }
    for (const [key, childSchema] of Object.entries(properties)) {
      if (key in value && isRecord(childSchema)) {
        const childResult = validateSchema(childSchema, value[key], `${path}.${key}`, seen, definitions);
        issues.push(...childResult.issues);
      }
    }
    for (const [key, childValue] of Object.entries(value)) {
      for (const childSchema of matchingPatternSchemas(patternProperties, key)) {
        issues.push(...validateSchema(childSchema, childValue, `${path}.${key}`, seen, definitions).issues);
      }
    }
    if (isRecord(schema.additionalProperties)) {
      for (const [key, childValue] of Object.entries(value)) {
        if (!(key in properties) && matchingPatternSchemas(patternProperties, key).length === 0) {
          const childResult = validateSchema(schema.additionalProperties, childValue, `${path}.${key}`, seen, definitions);
          issues.push(...childResult.issues);
        }
      }
    }
  }
  if (Array.isArray(schema.oneOf)) {
    const alternatives = schema.oneOf.filter(isRecord).map((candidate) => validateSchema(candidate, value, path, seen, definitions));
    if (!alternatives.some((candidate) => candidate.valid)) issues.push({ path, keyword: "oneOf" });
  }
  return { valid: issues.length === 0, issues };
}

function matchesType(type: string, value: unknown): boolean {
  switch (type) {
    case "object": return isRecord(value);
    case "array": return Array.isArray(value);
    case "string": return typeof value === "string";
    case "number": return typeof value === "number" && Number.isFinite(value);
    case "integer": return typeof value === "number" && Number.isInteger(value) && Number.isFinite(value);
    case "boolean": return typeof value === "boolean";
    case "null": return value === null;
    default: return true;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function sameJson(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}

function matchingPatternSchemas(patternProperties: Record<string, unknown>, key: string): Schema[] {
  return Object.entries(patternProperties).flatMap(([pattern, childSchema]) => {
    if (!isRecord(childSchema)) return [];
    try {
      return new RegExp(pattern).test(key) ? [childSchema] : [];
    } catch {
      return [];
    }
  });
}
