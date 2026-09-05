package stackkitmcp

// DefaultLocalServerURL is the loopback-only default for a local stackkit-server
// when STACKKITS_SERVER_URL and --server-url are unset. Standard Mode binds MCP
// and the API to the developer machine; it is not a hosted production origin.
const DefaultLocalServerURL = "http://localhost:8082"
