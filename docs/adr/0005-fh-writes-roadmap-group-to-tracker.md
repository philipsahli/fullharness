# fullharness writes the Roadmap group to the Issue tracker, and nothing else

Amends ADR-0004, under which fullharness only read Issue trackers and every write happened inside a Session through a skill. Moving a Feature on the Roadmap now writes its Roadmap group straight to the tracker: a `roadmap:now|next|later` label on the Spec issue (fh creates the labels the first time it needs them), or a `Roadmap:` line in `spec.md` for local markdown. We made this exception because starting a Session for every drag-and-drop is too slow, and because recording the group on the Spec lets people and Agents see it without fh. Every other tracker write still goes through a skill in a Session.

## Consequences

- The order of Features inside a group, and the group of an Idea (which has no Spec yet), live only in fh's local state, because no single tracker can hold an order that spans repos.
- On a public repo the `roadmap:*` labels are public.
