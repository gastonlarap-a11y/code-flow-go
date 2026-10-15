import { describe, expect, it } from "vitest";
import { parseTopics, pushTopic } from "./topics";

describe("recent topics", () => {
  it("puts the newest first, once, per kind", () => {
    let recent = pushTopic({}, "sequence", "checkout");
    recent = pushTopic(recent, "sequence", " login ");
    recent = pushTopic(recent, "sequence", "checkout");
    recent = pushTopic(recent, "workflow", "release");
    expect(recent).toEqual({ sequence: ["checkout", "login"], workflow: ["release"] });
  });

  it("keeps six and ignores what is blank or a paragraph", () => {
    let recent = {};
    for (const topic of ["a", "b", "c", "d", "e", "f", "g"]) recent = pushTopic(recent, "dataflow", topic);
    expect(recent).toEqual({ dataflow: ["g", "f", "e", "d", "c", "b"] });
    expect(pushTopic(recent, "dataflow", "   ")).toBe(recent);
    expect(pushTopic(recent, "dataflow", "x".repeat(301))).toBe(recent);
  });

  it("reads back only what it wrote", () => {
    expect(parseTopics(JSON.stringify({ sequence: ["a", 2, "b"], erd: ["x"], lifecycle: "no" }))).toEqual({ sequence: ["a", "b"] });
    expect(parseTopics(null)).toEqual({});
    expect(parseTopics("{broken")).toEqual({});
    expect(parseTopics("[1]")).toEqual({});
  });
});
