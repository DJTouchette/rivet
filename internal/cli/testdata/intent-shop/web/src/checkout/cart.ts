export function itemCount(lines: number[]): number {
  return lines.reduce((a, b) => a + b, 0);
}
