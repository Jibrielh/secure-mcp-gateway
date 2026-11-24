import { describe, expect, it } from "vitest";
import { percent } from "./api";

describe("percent", () => {
  it("does not present a missing rate as zero", () => {
    expect(percent(null)).toBe("Unavailable");
    expect(percent(0.5)).toBe("50%");
  });
});
