import type { ReactNode } from "react";

// isWebURL is true for http(s) URLs, the only links an Agent's text may make.
export function isWebURL(url: string): boolean {
  return /^https?:\/\//i.test(url);
}

// LinkOrText renders a URL the server computed (#86) as a link, and anything
// else as plain text, so an empty url looks as it did before links existed.
// A web link opens in a new tab, as Agent Markdown links do (#83).
// vscode://file/ opens in place: the browser hands it to VS Code. Other
// vscode:// URLs can drive editor extensions, so they stay text.
export function LinkOrText({
  url,
  title,
  children,
}: {
  url?: string;
  title?: string;
  children: ReactNode;
}) {
  if (url && isWebURL(url)) {
    return (
      <a href={url} title={title} target="_blank" rel="noopener">
        {children}
      </a>
    );
  }
  if (url && /^vscode:\/\/file\//i.test(url)) {
    return (
      <a href={url} title={title}>
        {children}
      </a>
    );
  }
  return <span title={title}>{children}</span>;
}
