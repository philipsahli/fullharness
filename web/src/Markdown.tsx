import { memo, useEffect, useRef, useState } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { Highlighted, languageOf } from "./Code";
import { isWebURL, LinkOrText } from "./LinkOrText";

// AgentMarkdown renders an Agent message as Markdown (GFM). Raw HTML in the
// text shows as text, never as markup. While a message streams, text changes
// on every chunk; parsing is held to at most once per animation frame.
export function AgentMarkdown({ text }: { text: string }) {
  return <Rendered text={useOncePerFrame(text)} />;
}

// Rendered is memoised so a parent re-render (another chunk, another
// message) doesn't parse text that hasn't changed.
const Rendered = memo(function Rendered({ text }: { text: string }) {
  return (
    <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
      {text}
    </ReactMarkdown>
  );
});

const components: Components = {
  // A fence's info string becomes class language-<info>; inline code has
  // none. A language fh doesn't bundle stays plain.
  code: ({ className, children }) => {
    const lang = languageOf(/\blanguage-(\S+)/.exec(className ?? "")?.[1]);
    return (
      <code className={className}>
        {lang && typeof children === "string" ? (
          <Highlighted language={lang} text={children} />
        ) : (
          children
        )}
      </code>
    );
  },
  // Only http(s) links are links, and they open in a new tab. Anything else
  // (javascript:, file:, relative paths fh can't resolve) is plain text.
  // isWebURL also keeps vscode:// out, which LinkOrText would allow.
  a: ({ href, children }) => (
    <LinkOrText url={href && isWebURL(href) ? href : undefined}>{children}</LinkOrText>
  ),
  // An image would make the page fetch its URL without a click, so an
  // http(s) image is a link to it, named by its alt text, and any other image
  // is its alt text.
  img: ({ src, alt }) =>
    typeof src === "string" && isWebURL(src) ? (
      <LinkOrText url={src}>{alt || src}</LinkOrText>
    ) : (
      <span>{alt}</span>
    ),
};

// useOncePerFrame returns value, updated at most once per animation frame.
// Unlike cancelling and rescheduling a frame on each change, it keeps the
// first pending frame, so a stream faster than 60 chunks/s still renders.
function useOncePerFrame(value: string): string {
  const [shown, setShown] = useState(value);
  const latest = useRef(value);
  const frame = useRef(0);
  useEffect(() => {
    latest.current = value;
    if (frame.current) return;
    frame.current = requestAnimationFrame(() => {
      frame.current = 0;
      setShown(latest.current);
    });
  }, [value]);
  // Reset the ref as well as cancelling: StrictMode (dev) runs this cleanup
  // and then the effects again, and a stale frame ID would make the effect
  // above wait for a frame that never comes, freezing the message.
  useEffect(
    () => () => {
      cancelAnimationFrame(frame.current);
      frame.current = 0;
    },
    [],
  );
  return shown;
}
