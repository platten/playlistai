export const LOG_LEVELS = { DEBUG: -4, INFO: 0, WARN: 4, ERROR: 8 } as const;
export type LogLevel = keyof typeof LOG_LEVELS;

/** slog also emits intermediate levels such as DEBUG+2 and WARN+1. */
export function logSeverity(level: string): number {
  const match = /^(DEBUG|INFO|WARN|ERROR)([+-]\d+)?$/.exec(level);
  return match ? LOG_LEVELS[match[1] as LogLevel] + Number(match[2] ?? 0) : LOG_LEVELS.INFO;
}
