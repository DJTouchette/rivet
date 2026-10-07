import { firstReminder } from "./dunning";

// rivet:intent BIL-010
test("first reminder is 14 days after due", () => {
  const due = new Date("2024-01-01");
  expect(firstReminder(due).toISOString().slice(0, 10)).toBe("2024-01-15");
});
