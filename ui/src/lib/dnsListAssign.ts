export type DomainListSummary = {
  id: string;
  name?: string;
  source?: string;
  category?: string;
  entry_count?: number;
};

export type AssignedListRow = {
  id: string;
  name: string;
  source: string;
  category: string;
  entry_count: number;
};

export function assignedListRows(
  ids: string[],
  lists: DomainListSummary[],
): AssignedListRow[] {
  const byId = new Map(lists.map((list) => [list.id, list]));
  return ids.map((id) => {
    const list = byId.get(id);
    return {
      id,
      name: list?.name || id,
      source: list?.source || "—",
      category: list?.category || "",
      entry_count: list?.entry_count || 0,
    };
  });
}

// Allow Lists cannot offer a list that is already a Block List.
// Each dialog also hides lists already assigned to itself.
export function listsAvailableToAssign(
  mode: "block" | "allow",
  lists: DomainListSummary[],
  blockIds: string[],
  allowIds: string[],
): DomainListSummary[] {
  const taken = new Set<string>(
    mode === "block" ? blockIds : [...blockIds, ...allowIds],
  );
  return lists.filter((list) => list.id && !taken.has(list.id));
}
