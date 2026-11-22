// ADR 0007: typescript-eslint's recommended rules plus the React hooks rules.
// Formatting is Prettier's job (`npm run format:check`), not ESLint's.
import js from "@eslint/js";
import globals from "globals";
import reactHooks from "eslint-plugin-react-hooks";
import tseslint from "typescript-eslint";

export default tseslint.config(
  { ignores: ["dist", "node_modules", "test-results", "playwright-report"] },
  js.configs.recommended,
  tseslint.configs.recommended,
  // React rules only where React code lives; e2e/ has Playwright's `use`.
  { ...reactHooks.configs.flat.recommended, files: ["src/**/*.{ts,tsx}"] },
  {
    files: ["src/**/*.{ts,tsx}"],
    languageOptions: { globals: globals.browser },
  },
  {
    files: ["vite.config.ts", "playwright.config.ts", "eslint.config.js", "e2e/**/*.ts"],
    languageOptions: { globals: globals.node },
  },
);
