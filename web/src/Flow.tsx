import { useEffect, useState, type KeyboardEvent, type ReactNode } from "react";
import { get } from "./api";
import { reasonName } from "./Autopilot";
import type { AutopilotReason, AutopilotStatus } from "./events";

// The flow graph (#206, ADR-0013), as the mockup draws it: the Stages on a
// main line, each Ticket's Worktree as a branch that merges back, and a dot
// for each skill Autopilot ran, coloured by how it went. fh works the graph
// out (internal/autopilot/flow); this only lays it out and draws it. One
// SVG scaled by its viewBox, so the Loop view (#114) can show it
// full-screen.

// These types match internal/autopilot/flow.
type NodeKind = "stage" | "stop" | "skill";
type NodeState = "todo" | "passed" | "running" | "waiting" | "failed";
type FlowNode = {
  kind: NodeKind;
  name: string; // a Stage, a Stop point's reason, or a skill
  state: NodeState;
  skill?: string;
  reason?: string;
  session?: string;
  error?: string;
  current?: boolean;
};
type FlowLane = {
  ticket: string;
  title: string;
  nodes: FlowNode[];
  joined?: boolean;
  blockedBy?: string[];
};
export type Graph = { main: FlowNode[]; lanes: FlowLane[] };

const stageCaps: Record<string, string> = {
  idea: "Idea",
  spec: "Spec",
  "spec-approval": "Approve spec",
  tickets: "Tickets",
  implementing: "Implementing",
  review: "Review",
  shipped: "Shipped",
};

// FeatureFlow reads a Feature's flow graph and reads it again whenever its
// Autopilot status or its board changes, which the event channel and the
// board's own reads bring: the graph moves live, without a reload.
export function FeatureFlow({
  project,
  feature,
  title,
  status,
  boardKey,
  onOpenSession,
}: {
  project: string;
  feature: string;
  title: string;
  status?: AutopilotStatus;
  boardKey: string; // changes when the board read shows the Feature changed
  onOpenSession: (id: string) => void;
}) {
  const [graph, setGraph] = useState<Graph | undefined>();
  const statusKey = JSON.stringify(status ?? null);
  useEffect(() => {
    let live = true;
    const url = `/api/autopilot/flow?project=${encodeURIComponent(project)}&feature=${encodeURIComponent(feature)}`;
    void get<Graph>(url).then((g) => {
      if (live && g) setGraph(g);
    });
    return () => {
      live = false;
    };
  }, [project, feature, statusKey, boardKey]);
  if (!graph) return null;
  return (
    <section className="flow-panel" aria-label="Flow graph">
      <div className="flow-wrap">
        <FlowSvg graph={graph} title={title} onOpen={onOpenSession} />
      </div>
      <FlowLegend />
    </section>
  );
}

// Layout, in viewBox units, as the mockup's: the main line at MY, a lane
// every LANE below it; lanes branch off just after Tickets, the last lane
// first, so their curves don't cross.
const MY = 34;
const LANE = 32;
const STEP = 44;
const X = { idea: 40, spec: 125, approve: 202, tickets: 280 };

function stopLabel(n: FlowNode): string {
  const r = n.kind === "stop" ? n.name : (n.reason ?? "");
  return reasonName[r as AutopilotReason] ?? r;
}

function nodeWords(n: FlowNode, ticket?: string): string {
  const what =
    n.kind === "stop"
      ? `Stop point ${stopLabel(n)}`
      : n.kind === "stage" && !n.skill
        ? stageCaps[n.name]
        : `/${n.skill ?? n.name}`;
  const state = n.state === "waiting" && n.kind !== "stop" ? `waiting at ${stopLabel(n)}` : n.state;
  return `${ticket ? `#${ticket} ` : ""}${what}: ${state}${n.current ? ", Autopilot is here" : ""}${n.error ? ` (${n.error})` : ""}`;
}

// Clickable wraps a node that ran in a Session: clicking it opens that
// Session; it is a button for the keyboard and screen readers too.
function Clickable({
  node,
  ticket,
  onOpen,
  children,
}: {
  node: FlowNode;
  ticket?: string;
  onOpen: (id: string) => void;
  children: ReactNode;
}) {
  const words = nodeWords(node, ticket);
  const data = {
    className: "fl-node",
    "data-name": node.name,
    "data-state": node.state,
    "data-current": node.current || undefined,
    "data-ticket": ticket,
  };
  if (!node.session) {
    return (
      <g {...data} role="img" aria-label={words}>
        <title>{words}</title>
        {children}
      </g>
    );
  }
  const session = node.session;
  return (
    <g
      {...data}
      role="button"
      tabIndex={0}
      aria-label={`${words}. Open its Session`}
      onClick={() => onOpen(session)}
      onKeyDown={(e: KeyboardEvent) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onOpen(session);
        }
      }}
    >
      <title>{words}</title>
      {children}
    </g>
  );
}

// Mark draws a node where Autopilot is now, or that ran, failed or waits:
// a glowing dot while running, ◉ at a waiting Stop point, ✖ for a failure.
// Keyed on its state by the caller, so a change springs it in again.
function Mark({ n, x, y, label }: { n: FlowNode; x: number; y: number; label: boolean }) {
  const ap = n.state === "passed" || n.state === "todo" ? undefined : n.state;
  const text = n.state === "waiting" ? stopLabel(n) : `/${n.skill ?? n.name}`;
  return (
    <g data-ap={ap} className={n.current ? "fl-here" : undefined}>
      {n.current && <circle className="fl-halo" cx={x} cy={y} r={11} />}
      {n.state === "running" && (
        <>
          <circle className="fl-ring" cx={x} cy={y} r={6} />
          <circle className="fl-ring r2" cx={x} cy={y} r={6} />
          <circle className="fl-cur" cx={x} cy={y} r={6} />
        </>
      )}
      {n.state === "waiting" &&
        (n.current ? (
          <text className="glyph" x={x} y={y + 4.5} textAnchor="middle">
            ◉
          </text>
        ) : (
          <>
            <circle className="fl-stop" cx={x} cy={y} r={6.5} />
            <circle className="fl-stop-dot" cx={x} cy={y} r={2.6} />
          </>
        ))}
      {n.state === "failed" && (
        <path
          className="fl-fail"
          d={`M${x - 4},${y - 4} L${x + 4},${y + 4} M${x + 4},${y - 4} L${x - 4},${y + 4}`}
        />
      )}
      {label && (n.current || n.state === "waiting") && (
        <text className={n.current ? "curlbl" : "lbl"} x={x + 14} y={y + 4}>
          {text}
          {n.state === "waiting" && !n.current && n.name === "merge" ? " · yours" : ""}
        </text>
      )}
    </g>
  );
}

function Diamond({ x, y, size, done }: { x: number; y: number; size: number; done: boolean }) {
  return (
    <rect
      className={`fl-stop${done ? " done" : ""}`}
      x={x - size / 2}
      y={y - size / 2}
      width={size}
      height={size}
      rx={1.5}
      transform={`rotate(45 ${x} ${y})`}
    />
  );
}

// LaneNode draws one dot of a Ticket's lane.
function LaneNode({ n, x, y }: { n: FlowNode; x: number; y: number }) {
  if (n.state === "passed" && n.kind === "stop") return <Diamond x={x} y={y} size={8} done />;
  if (n.state === "passed" || n.state === "todo")
    return (
      <circle className={`fl-run${n.state === "passed" ? " done" : ""}`} cx={x} cy={y} r={4.5} />
    );
  return <Mark n={n} x={x} y={y} label />;
}

// MainNode draws one Stage of the main line, or the Approve spec Stop point.
function MainNode({ n, x, current }: { n: FlowNode; x: number; current: boolean }) {
  if (n.kind === "stop") {
    if (n.state === "waiting") return <Mark n={n} x={x} y={MY} label={n.current ?? false} />;
    return <Diamond x={x} y={MY} size={10} done={n.state === "passed"} />;
  }
  if (n.state === "running" || n.state === "failed" || n.state === "waiting")
    return <Mark n={n} x={x} y={MY} label />;
  const cls = n.state === "passed" ? " done" : current ? "" : " todo";
  return <circle className={`fl-stage${cls}`} cx={x} cy={MY} r={current ? 7 : 5.5} />;
}

// FlowSvg draws a flow graph. It is the one piece the Loop view (#114) will
// reuse full-screen: its size comes from the viewBox alone.
export function FlowSvg({
  graph,
  title,
  onOpen,
}: {
  graph: Graph;
  title: string;
  onOpen: (id: string) => void;
}) {
  const lanes = graph.lanes;
  const n = lanes.length;
  const laneX = (i: number) => X.tickets + 20 + (n - 1 - i) * 26;
  const laneY = (i: number) => MY + LANE * (i + 1);
  // A lane's first node is pushed right when it is where Autopilot is, so
  // the live line and the Ticket number have room (the mockup).
  const nodeX = (l: FlowLane, i: number, k: number) =>
    laneX(i) + STEP + STEP * k + (l.nodes[0]?.current ? 30 : 0);
  const laneEnd = (l: FlowLane, i: number) =>
    l.nodes.length ? nodeX(l, i, l.nodes.length - 1) : laneX(i) + 70;
  const widest = lanes.reduce(
    (m, l, i) => Math.max(m, laneEnd(l, i) + (l.nodes.length ? 130 : 90)),
    0,
  );
  const xs: Record<string, number> = {
    ...X,
    implementing: 470,
    review: Math.max(650, widest),
  };
  xs.shipped = xs.review + 210;
  const W = xs.shipped + 60;
  const H = MY + LANE * Math.max(n, 1) + 30;

  const main = graph.main;
  const xOfMain = (m: FlowNode) => (m.kind === "stop" ? xs.approve : xs[m.name]);
  // The first Stage not passed yet is the Feature's own, drawn larger.
  const firstOpen = main.findIndex((m) => m.state !== "passed");

  let mergedTo = 0;
  const lanePaths: ReactNode[] = [];
  const laneLabels: ReactNode[] = [];
  const laneNodes: ReactNode[] = [];
  lanes.forEach((l, i) => {
    const x0 = laneX(i);
    const y = laneY(i);
    const last = laneEnd(l, i);
    const ci = l.nodes.findIndex((m) => m.current && m.state === "running");
    const solidTo = ci > 0 ? nodeX(l, i, ci - 1) : ci === 0 ? x0 + 22 : last;
    let d = `M${x0},${MY} C${x0 + 14},${MY} ${x0 + 6},${y} ${x0 + 22},${y} H${solidTo}`;
    if (l.joined) {
      d += ` C${last + 18},${y} ${last + 10},${MY} ${last + 30},${MY}`;
      mergedTo = Math.max(mergedTo, last + 30);
    }
    const cls = l.joined ? "fl-done" : l.nodes.length ? "fl-open" : "fl-todo";
    lanePaths.unshift(
      <g key={l.ticket}>
        <path className="fl-bridge" d={d} />
        <path className={`fl-line ${cls}`} d={d} />
        {ci >= 0 && (
          <g data-ap="running">
            <path className="fl-line fl-live" d={`M${solidTo},${y} H${nodeX(l, i, ci)}`} />
          </g>
        )}
      </g>,
    );
    laneLabels.push(
      <g key={`n${l.ticket}`}>
        <title>{`#${l.ticket} ${l.title}`}</title>
        <text className="tn" x={x0 + 24} y={y - 7}>
          #{l.ticket}
        </text>
      </g>,
    );
    if (!l.nodes.length) {
      laneLabels.push(
        <text key={`e${l.ticket}`} className="lbl" x={x0 + 52} y={y - 7}>
          {l.blockedBy?.length ? `blocked by #${l.blockedBy.join(", #")}` : "not started"}
        </text>,
      );
    }
    l.nodes.forEach((m, k) => {
      const x = nodeX(l, i, k);
      laneNodes.push(
        <Clickable
          key={`${l.ticket}:${k}:${m.state}:${m.current}`}
          node={m}
          ticket={l.ticket}
          onOpen={onOpen}
        >
          <LaneNode n={m} x={x} y={y} />
        </Clickable>,
      );
    });
  });

  // The main line is solid up to the last Stage reached or the last merge,
  // dotted after.
  const reached = main.filter((m) => m.state !== "todo" && (m.kind !== "stage" || drawnOnMain(m)));
  const solid = Math.max(X.idea, mergedTo, ...reached.map(xOfMain));
  const hereNode = [...main, ...lanes.flatMap((l) => l.nodes)].find((m) => m.current);
  return (
    <svg
      className="flow"
      viewBox={`0 0 ${W} ${H}`}
      role="group"
      aria-label={`Flow graph of ${title}: ${n} Ticket${n === 1 ? "" : "s"}${hereNode ? `, Autopilot ${hereNode.state}` : ""}`}
    >
      {main.map((m) => (
        <text key={`c${m.name}`} className="cap" x={xOfMain(m)} y={MY - 16} textAnchor="middle">
          {stageCaps[m.name]}
        </text>
      ))}
      <path className="fl-line fl-done" d={`M${X.idea},${MY} H${solid}`} />
      <path className="fl-line fl-todo" d={`M${solid},${MY} H${xs.shipped}`} />
      {lanePaths}
      {laneLabels}
      {main.map((m, i) =>
        drawnOnMain(m) ? (
          <Clickable key={`m${m.name}:${m.state}:${m.current}`} node={m} onOpen={onOpen}>
            <MainNode n={m} x={xOfMain(m)} current={i === firstOpen} />
          </Clickable>
        ) : null,
      )}
      {laneNodes}
    </svg>
  );
}

// Implementing and Review happen on the lanes; their Stage gets a node only
// when Autopilot ran a skill on the whole Feature there (implement-spec).
function drawnOnMain(m: FlowNode): boolean {
  return !((m.name === "implementing" || m.name === "review") && !m.skill);
}

function FlowLegend() {
  return (
    <div className="flow-legend" aria-hidden="true">
      <span>
        <svg viewBox="0 0 14 14">
          <circle className="lg-run" cx="7" cy="7" r="4" />
        </svg>
        skill run
      </span>
      <span>
        <svg viewBox="0 0 14 14">
          <rect
            className="lg-stop"
            x="3.5"
            y="3.5"
            width="7"
            height="7"
            rx="1"
            transform="rotate(45 7 7)"
          />
        </svg>
        Stop point passed
      </span>
      <span>
        <b className="lg-wait">◉</b> waiting at a Stop point
      </span>
      <span>
        <b className="lg-fail">✖</b> failed
      </span>
      <span>
        <svg viewBox="0 0 14 14">
          <circle className="lg-here" cx="7" cy="7" r="5" />
        </svg>
        Autopilot is here now
      </span>
      <span>
        <svg viewBox="0 0 22 14">
          <path className="lg-todo" d="M2 7 H20" />
        </svg>
        not started
      </span>
    </div>
  );
}
