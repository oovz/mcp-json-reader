import { startServer } from "./server.js";

// Re-export core types and functionalities for backward compatibility and test access
export * from "./types.js";
export * from "./cache.js";
export * from "./math.js";
export * from "./operations.js";
export { server, resolveQuery } from "./server.js";

// Run the MCP server automatically when run directly in production (not in tests)
if (process.env.NODE_ENV !== "test") {
    startServer().catch((err: unknown) => {
        console.error(err);
        process.exit(1);
    });
}
