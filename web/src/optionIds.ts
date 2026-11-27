// optionIds gives one DOM id per key, for aria-activedescendant and
// aria-controls. An id there must hold no whitespace (an ID reference list
// splits on it) and be unique in the page. Keys can break both: a local
// board's Feature id is a folder name that may hold a space, and an Agent may
// list one slash command name twice. Percent-encoding removes whitespace and
// keeps distinct keys distinct ("dark mode" and "dark-mode" stay apart); a
// repeated key gets "~2", "~3", … by its place among the rows with that key,
// so an id stays the same for a row while other rows come and go.
export function optionIds(prefix: string, keys: string[]): string[] {
  const seen = new Map<string, number>();
  return keys.map((key) => {
    const n = (seen.get(key) ?? 0) + 1;
    seen.set(key, n);
    // encodeURIComponent leaves "~" as it is; encode it here, so only the
    // repeat suffix holds one.
    const id = `${prefix}-${encodeURIComponent(key).replaceAll("~", "%7E")}`;
    return n === 1 ? id : `${id}~${n}`;
  });
}
