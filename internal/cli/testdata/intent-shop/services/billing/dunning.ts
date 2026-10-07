// rivet:intent BIL-010
export const DUNNING_START_DAYS = 14;

export function firstReminder(due: Date): Date {
  return new Date(due.getTime() + DUNNING_START_DAYS * 86_400_000);
}
