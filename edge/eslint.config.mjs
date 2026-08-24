import { dirname } from "path";
import { fileURLToPath } from "url";
import { FlatCompat } from "@eslint/eslintrc";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

const compat = new FlatCompat({
  baseDirectory: __dirname,
});

const eslintConfig = [
  ...compat.config({
    extends: [
      "next",
      "next/core-web-vitals",
      "next/typescript",
      "prettier",
      "plugin:react/recommended",
      "plugin:react-hooks/recommended",
    ],
    settings: {
      react: {
        version: "detect",
      },
    },
    rules: {
      "@typescript-eslint/no-unused-vars": "warn", // Warn about unused variables
      "no-console": "warn", // Warn about console statements
      "react/react-in-jsx-scope": "off",
    },
  }),
];

export default eslintConfig;
