export type GroupMemberChange = { email: string; join: boolean };

export function parseGroupNames(value: string): string[] {
  return [...new Set(value.split(",").map((name) => name.trim()).filter(Boolean))].sort();
}

export function groupMemberChanges(current: readonly string[], picked: readonly string[]): GroupMemberChange[] {
  const existing = new Set(current);
  const selected = new Set(picked);
  return [
    ...[...new Set(picked)].filter((email) => !existing.has(email)).map((email) => ({ email, join: true })),
    ...[...new Set(current)].filter((email) => !selected.has(email)).map((email) => ({ email, join: false })),
  ];
}

export function membershipAfterChange(current: readonly string[], group: string, join: boolean): string[] {
  return [...new Set(join ? [...current, group] : current.filter((name) => name !== group))].sort();
}
