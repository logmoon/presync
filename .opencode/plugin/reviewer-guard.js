import { readFile } from "node:fs/promises";
import { join } from "node:path";

const REVIEWER = "reviewer";
const DEFAULT_STEPS = 20;
const RESERVED_REPORT_STEPS = 2;
const TIME_LIMIT_MS = 12 * 60 * 1000;
const NO_PROGRESS_LIMIT = 3;
const DUPLICATE_LIMIT = 2;

const REPORT_MESSAGE =
  "Reviewer report reserve reached. Tools are closed. Do not call another tool. " +
  "Write the final report now: VERDICT, COVERAGE, CONFIRMED FINDINGS, UNVERIFIED/DEFERRED.";

const TIME_MESSAGE =
  "Reviewer time limit reached. Tools are closed. Do not call another tool. " +
  "Write the final report now with what you have; mark the rest INCOMPLETE or UNVERIFIED.";

const NO_PROGRESS_MESSAGE =
  "Three repeated checks were blocked in a row. Stop investigating. Update the ledger " +
  "and write the final report now; list unresolved candidates as UNVERIFIED or DEFERRED.";

const DUPLICATE_READ_MESSAGE =
  "Already read this region. Do not re-read it. Use the result you have, or mark the " +
  "candidate UNVERIFIED, then continue with the ledger.";

const DUPLICATE_CALL_MESSAGE =
  "Already ran this exact check. Do not repeat it. Use the result you have, or mark the " +
  "candidate UNVERIFIED, then continue with the ledger.";

const UNSAFE_BASH_MESSAGE =
  "Bash is limited to a single git diff command. Do not use pipes, chaining, command " +
  "substitution, redirection, or other commands.";

function normalizeCommand(command) {
  return String(command ?? "").trim().replace(/\s+/g, " ");
}

function argsKey(tool, args) {
  if (!args || typeof args !== "object") return null;

  if (tool === "read") {
    if (!args.filePath) return null;
    return `${tool}\0${args.filePath}\0${args.offset ?? 1}\0${args.limit ?? 2000}`;
  }
  if (tool === "grep") {
    return `${tool}\0${args.pattern ?? ""}\0${args.path ?? ""}\0${args.include ?? ""}`;
  }
  if (tool === "glob") {
    return `${tool}\0${args.pattern ?? ""}\0${args.path ?? ""}`;
  }
  if (tool === "list") {
    return `${tool}\0${args.path ?? ""}`;
  }
  if (tool === "bash") {
    return `${tool}\0${normalizeCommand(args.command)}\0${args.workdir ?? ""}`;
  }
  return null;
}

function readRange(args) {
  if (!args?.filePath) return null;
  const start = Number.isFinite(Number(args.offset)) ? Math.max(1, Number(args.offset)) : 1;
  const limit = Number.isFinite(Number(args.limit)) ? Math.max(1, Number(args.limit)) : 2000;
  return { file: args.filePath, start, end: start + limit - 1 };
}

function isCovered(intervals, start, end) {
  return intervals.some((interval) => start >= interval.start && end <= interval.end);
}

function createState() {
  return {
    startedAt: Date.now(),
    step: -1,
    blockedStreak: 0,
    reportMode: false,
    messageID: null,
    argsCounts: new Map(),
    readIntervals: new Map(),
    pending: new Map(),
  };
}

function getState(sessions, sessionID, messageID) {
  let state = sessions.get(sessionID);
  if (!state) {
    state = createState();
    sessions.set(sessionID, state);
  }

  if (messageID && state.messageID && messageID !== state.messageID) {
    state = createState();
    sessions.set(sessionID, state);
  }
  if (messageID) state.messageID = messageID;
  return state;
}

function capReasoning(options) {
  if (!options || typeof options !== "object") return;
  if (typeof options.reasoningEffort === "string") options.reasoningEffort = "low";
  if (
    options.reasoning &&
    typeof options.reasoning === "object" &&
    typeof options.reasoning.effort === "string"
  ) {
    options.reasoning.effort = "low";
  }
}

async function readReviewerSteps(directory) {
  try {
    const config = JSON.parse(await readFile(join(directory, "opencode.json"), "utf-8"));
    const steps = config?.agent?.reviewer?.steps;
    if (Number.isFinite(steps) && steps > RESERVED_REPORT_STEPS) return steps;
  } catch {}
  return DEFAULT_STEPS;
}

export const ReviewerGuard = async ({ directory }) => {
  const steps = await readReviewerSteps(directory);
  const reportReserve = Math.max(1, steps - RESERVED_REPORT_STEPS);
  const sessions = new Map();

  function enterReportMode(state, message) {
    state.reportMode = true;
    throw new Error(message);
  }

  function blockAttempt(state, message) {
    state.blockedStreak++;
    if (state.blockedStreak >= NO_PROGRESS_LIMIT) {
      enterReportMode(state, NO_PROGRESS_MESSAGE);
    }
    throw new Error(message);
  }

  return {
    "chat.message": async (input) => {
      if (input.agent !== REVIEWER || !input.sessionID) return;
      getState(sessions, input.sessionID, input.messageID);
    },

    "chat.params": async (input, output) => {
      if (input.agent !== REVIEWER || !input.sessionID) return;

      const state = getState(sessions, input.sessionID, input.message?.id);
      state.step++;

      if (state.step >= reportReserve || Date.now() - state.startedAt >= TIME_LIMIT_MS) {
        state.reportMode = true;
      }
      if (state.reportMode) capReasoning(output.options);
    },

    "tool.execute.before": async (input, output) => {
      const state = sessions.get(input.sessionID);
      if (!state) return;

      if (state.reportMode) enterReportMode(state, REPORT_MESSAGE);
      if (Date.now() - state.startedAt >= TIME_LIMIT_MS) enterReportMode(state, TIME_MESSAGE);

      const args = output.args ?? {};
      if (input.tool === "bash") {
        const command = normalizeCommand(args.command);
        if (
          !/^git diff(\s|$)/.test(command) ||
          /(?:&&|\|\||[;|&`$()<>]|--output\b|\s-o\b)/.test(command)
        ) {
          blockAttempt(state, UNSAFE_BASH_MESSAGE);
        }
      }

      if (input.tool === "read") {
        const range = readRange(args);
        if (range) {
          const intervals = state.readIntervals.get(range.file) ?? [];
          if (isCovered(intervals, range.start, range.end)) {
            blockAttempt(state, DUPLICATE_READ_MESSAGE);
          }
        }
      }

      const key = argsKey(input.tool, args);
      if (!key) return;
      if ((state.argsCounts.get(key) ?? 0) >= DUPLICATE_LIMIT) {
        blockAttempt(state, DUPLICATE_CALL_MESSAGE);
      }

      state.pending.set(input.callID, { key, range: input.tool === "read" ? readRange(args) : null });
    },

    "tool.execute.after": async (input) => {
      const state = sessions.get(input.sessionID);
      if (!state) return;

      const pending = state.pending.get(input.callID);
      if (!pending) return;
      state.pending.delete(input.callID);
      state.blockedStreak = 0;
      state.argsCounts.set(pending.key, (state.argsCounts.get(pending.key) ?? 0) + 1);

      if (pending.range) {
        const intervals = state.readIntervals.get(pending.range.file) ?? [];
        intervals.push({ start: pending.range.start, end: pending.range.end });
        state.readIntervals.set(pending.range.file, intervals);
      }
    },

    event: async ({ event }) => {
      if (event.type !== "session.deleted") return;
      const sessionID = event.properties?.info?.id ?? event.properties?.sessionID;
      if (sessionID) sessions.delete(sessionID);
    },
  };
};
