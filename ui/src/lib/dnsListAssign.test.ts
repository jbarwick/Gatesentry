import { expect, test } from "vitest";
import {
  assignedListRows,
  listsAvailableToAssign,
  type DomainListSummary,
} from "./dnsListAssign";

const lists: DomainListSummary[] = [
  { id: "ads", name: "Ads", source: "url", category: "ads", entry_count: 10 },
  { id: "malware", name: "Malware", source: "local", entry_count: 2 },
  { id: "social", name: "Social", source: "url", entry_count: 4 },
];

test("allow dialog hides lists already on the block list", () => {
  const available = listsAvailableToAssign("allow", lists, ["ads"], ["social"]);
  expect(available.map((list) => list.id)).toEqual(["malware"]);
});

test("block dialog hides lists already on the block list", () => {
  const available = listsAvailableToAssign("block", lists, ["ads"], ["social"]);
  expect(available.map((list) => list.id)).toEqual(["malware", "social"]);
});

test("assigned rows follow the current ids", () => {
  expect(assignedListRows(["ads", "missing"], lists)).toEqual([
    {
      id: "ads",
      name: "Ads",
      source: "url",
      category: "ads",
      entry_count: 10,
    },
    {
      id: "missing",
      name: "missing",
      source: "—",
      category: "",
      entry_count: 0,
    },
  ]);
});
