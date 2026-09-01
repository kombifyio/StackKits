# StackKits WebMCP schemas

`stackkits-webmcp.schema.json` is the versioned public contract. Its `tools`
object contains the four closed input schemas and its `$defs` contains the
shared result envelope and public data vocabulary. The TypeScript files under
`src/generated/` are generated from this file by `npm run generate:types`.

Unknown input properties are rejected at runtime as well as by the schemas.
