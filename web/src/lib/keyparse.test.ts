import { describe, expect, it } from "vitest";
// The cases the Go parser (server/internal/keyparse) runs too: both must agree.
import cases from "../../../server/internal/keyparse/testdata/cases.json";
import { MAX_KEYS, cleanToken, isKeyLike, maskLine, maskSecret, mergeParsed, parseKeys } from "./keyparse";

interface SharedCase {
  name: string;
  input: string;
  keys: { label?: string; secret: string }[];
  ignored: string[];
}

describe("parseKeys shared cases", () => {
  it("has the shared cases", () => {
    expect((cases as SharedCase[]).length).toBeGreaterThanOrEqual(15);
  });

  // The Go parser writes an unlabeled key as label "", this one leaves label out.
  const norm = (keys: { label?: string; secret: string }[]) => keys.map((k) => ({ label: k.label ?? "", secret: k.secret }));

  for (const c of cases as SharedCase[]) {
    it(c.name, () => {
      const got = parseKeys(c.input);
      expect(norm(got.keys)).toEqual(norm(c.keys));
      expect(got.ignored).toEqual(c.ignored);
    });
  }
});

describe("isKeyLike", () => {
  // Same table as TestKeyLike in keyparse_test.go.
  it.each([
    ["sk_live_0123456789", true],
    ["short1", false],
    ["abcdefghijklmnopqrstuvw", false], // 23 letters, no digit
    ["abcdefghijklmnopqrstuvwx", true],
    ["https://example.com/12345678", false],
    ["has space 1234567890", false],
    ["bad*char0123456789", false],
    ["a1".repeat(256), true],
    ["a1".repeat(257), false],
  ])("%s", (s, want) => {
    expect(isKeyLike(s)).toBe(want);
  });
});

describe("parseKeys", () => {
  it("labels a key with the word before it and dedupes in input order", () => {
    const got = parseKeys("prod sk_test_0000000000000001, sk_test_0000000000000002\nbackup sk_test_0000000000000001");
    expect(got.keys).toEqual([
      { label: "prod", secret: "sk_test_0000000000000001" },
      { secret: "sk_test_0000000000000002" },
    ]);
    expect(got.duplicates).toBe(1);
  });

  it("lends a duplicate's label only to a key that had none", () => {
    const got = parseKeys("sk_test_0000000000000001\nstaging sk_test_0000000000000001");
    expect(got.keys).toEqual([{ label: "staging", secret: "sk_test_0000000000000001" }]);
    expect(got.duplicates).toBe(1);
  });

  it("caps the keys at MAX_KEYS and counts the rest as overflow", () => {
    const lines = Array.from({ length: MAX_KEYS + 3 }, (_, i) => `sk_test_${String(i).padStart(16, "0")}`);
    const got = parseKeys(lines.join("\n"));
    expect(got.keys).toHaveLength(MAX_KEYS);
    expect(got.overflow).toBe(3);
  });
});

describe("mergeParsed", () => {
  it("dedupes across parts with the same rules as one parse", () => {
    const a = parseKeys("sk_test_0000000000000001");
    const b = parseKeys("main sk_test_0000000000000001\nsk_test_0000000000000002");
    const got = mergeParsed([a, b]);
    expect(got.keys).toEqual([
      { label: "main", secret: "sk_test_0000000000000001" },
      { secret: "sk_test_0000000000000002" },
    ]);
    expect(got.duplicates).toBe(1);
  });
});

describe("masking", () => {
  it("cleanToken strips quotes, brackets and trailing punctuation", () => {
    expect(cleanToken(`("sk_test_0000000000000001"),`)).toBe("sk_test_0000000000000001");
  });

  it("maskSecret shows only the last four characters", () => {
    expect(maskSecret("sk_test_0000000000009f3a")).toBe("…9f3a");
  });

  it("maskLine hides long tokens but keeps URLs and cuts long lines", () => {
    expect(maskLine("KEY=abcdefghijklmnop https://example.com/path")).toBe("…mnop https://example.com/path");
    expect(maskLine("word ".repeat(30), 20)).toBe(`${"word ".repeat(4)}…`);
  });
});
