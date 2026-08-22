import { defineConfig } from "vitest/config";

// Only src is unit-tested. The e2e directory holds Playwright specs, which
// share the .spec.ts suffix vitest would otherwise claim.
export default defineConfig({
	test: {
		include: ["src/**/*.test.ts", "src/**/*.test.tsx"],
		environment: "node",
	},
});
