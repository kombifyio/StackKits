# StackKits WebMCP schemas

`stackkits-webmcp-v2alpha1.schema.json` is the primary versioned public
contract. Its `tools` object contains the four closed input schemas and its
`$defs` contains the shared result envelope and public data vocabulary. The
v1 schema files remain available for migration-only consumers. The TypeScript
files under `src/generated/` are generated from the v2 schemas by
`npm run generate:types`.

Unknown input properties are rejected at runtime as well as by the schemas.
