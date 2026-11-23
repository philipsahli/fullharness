import { memo, type ReactNode } from "react";
import { createLowlight } from "lowlight";
import bash from "highlight.js/lib/languages/bash";
import diff from "highlight.js/lib/languages/diff";
import go from "highlight.js/lib/languages/go";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import markdown from "highlight.js/lib/languages/markdown";
import python from "highlight.js/lib/languages/python";
import typescript from "highlight.js/lib/languages/typescript";
import yaml from "highlight.js/lib/languages/yaml";

// Only these languages are bundled; anything else shows as plain monospace.
// lowlight runs highlight.js core and returns a syntax tree, which renders as
// React elements, so highlighted code never goes through innerHTML.
const lowlight = createLowlight({
  bash,
  diff,
  go,
  javascript,
  json,
  markdown,
  python,
  typescript,
  yaml,
});
// The grammars bring their own aliases (ts, py, yml, sh, md, patch, ...).
lowlight.registerAlias({ bash: ["shell"] });

// languageOf returns the language a fence's info string or a file extension
// names, or undefined if it isn't bundled.
export function languageOf(name: string | undefined): string | undefined {
  const lang = name?.toLowerCase();
  return lang && lowlight.registered(lang) ? lang : undefined;
}

// languageOfPath returns the language of a file by its extension. A file
// without one (Makefile, Dockerfile) has none and stays plain.
export function languageOfPath(path: string | undefined): string | undefined {
  const m = path && /\.([^./\\]+)$/.exec(path);
  return m ? languageOf(m[1]) : undefined;
}

// Highlighted is text as highlighted spans (class hljs-*) in language, which
// must be one languageOf returned. Memoised: a tool card re-renders on every
// chunk of a streaming reply, and a read can be a whole file.
export const Highlighted = memo(function Highlighted({
  language,
  text,
}: {
  language: string;
  text: string;
}) {
  return <>{lowlight.highlight(language, text).children.map(toReact)}</>;
});

// unfence returns text without the one fence around all of it, if there is
// one. Claude Code's adapter sends a read's file text in a fence (opened by 3
// or more backticks, closed by as many), so that a client rendering Markdown
// shows it as code.
export function unfence(text: string): string {
  const m = /^(`{3,})[^`\n]*\n([\s\S]*?)\n?\1[ \t]*\n?$/.exec(text);
  return m ? m[2] : text;
}

type HastNode = ReturnType<typeof lowlight.highlight>["children"][number];

function toReact(node: HastNode, key: number): ReactNode {
  if (node.type === "text") return node.value;
  if (node.type !== "element") return null;
  const cls = node.properties.className;
  return (
    <span key={key} className={Array.isArray(cls) ? cls.join(" ") : undefined}>
      {node.children.map(toReact)}
    </span>
  );
}
