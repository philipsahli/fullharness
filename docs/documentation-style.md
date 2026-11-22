# Documentation Style & Factory Rules

This file governs all automated and human-assisted updates to `README.md`, `CONTRIBUTING.md`, and files in `docs/`.

---

## 1. Audience & Tone
* **Target reader:** A new community member or open-source contributor seeing this project for the first time.
* **Voice:** Welcoming, practical, and direct. Write like a helpful maintainer, not a marketing brochure.
* **Anti-slop rules (strictly forbidden):**
  * Do not use AI filler words: *delve, tapestry, seamless, supercharge, leverage, robust, elevate, game-changer, empower*.
  * Do not make exaggerated claims (e.g., "blazingly fast", "revolutionary") unless backed by a linked benchmark in `benchmarks/`.
  * Use second-person ("you") for instructions and first-person plural ("we") for project conventions.

## 2. Required `README.md` Structure
Maintain these sections in this exact order. Do not remove sections unless instructed by a human maintainer.

1. **Title & One-Liner:** Project name + one crisp sentence explaining *what it does* and *who it is for*.
2. **Badges:** Keep existing CI, license, and version badges intact. Do not invent new badges.
3. **Why This Exists (2–3 sentences max):** The concrete problem this community tool solves.
4. **Quickstart:** Copy-pasteable commands to install and run the project in under 2 minutes.
5. **Configuration / Usage:** Common flags, environment variables, or examples (link to `docs/` for advanced usage).
6. **Contributing:** Brief summary + direct link to `CONTRIBUTING.md`.
7. **Community & Support:** Links to discussions, chat, or issue templates.
8. **License:** One-line reference to `LICENSE`.

## 3. Code Snippets & Command Verification ("Brakes")
* **Every shell command must be runnable.** Before opening a PR, verify that install, build, and CLI examples match actual scripts in `package.json`, `Makefile`, `pyproject.toml`, or `Cargo.toml`.
* **Specify language tags** on all fenced code blocks (`bash`, `json`, `ts`, `yaml`).
* **Use realistic placeholders:** Use `your-api-key` or `example.com`—never leave real tokens, internal URLs, or local user paths (`/Users/name/...`).
* **Output examples:** When showing a CLI command, include a short, accurate comment or output block showing what the user should expect to see.

## 4. Formatting & Markdown Rules
* **One idea per bullet point.** Keep list items scannable.
* **Active voice & imperative verbs** for steps (e.g., "Run `npm install`" instead of "You should now run `npm install`").
* **Relative links only** for internal repo files (e.g., `[Contributing Guide](./CONTRIBUTING.md)`). Verify the target file exists before linking.
* **No massive walls of text:** Keep paragraphs under 4 lines. Break up complex setups with subheadings or tables.

## 5. Factory Boundaries (Do Not Touch Without Approval)
When running autonomously, the agent must **NOT**:
* Modify `LICENSE`, `CODE_OF_CONDUCT.md`, or security disclosure policies.
* Alter sponsorship links, maintainer credits, or governance rules.
* Rewrite the entire `README.md` when only a small feature or flag changed—make surgical, minimal diffs.

## 6. Compounding Log (Agent Lessons)
*Add specific rules here whenever a human reviewer rejects or edits an agent-generated doc PR.*
* [Example] Always mention Node/Python minimum version prerequisites in Quickstart if `.nvmrc` or `.python-version` changes.
