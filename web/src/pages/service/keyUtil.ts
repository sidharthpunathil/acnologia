import type { EffectiveStatus, Key } from "../../lib/types";

/**
 * effective_status from the server is a snapshot. A cooling key whose
 * cooldown has passed is available again, so reflect that without a refetch.
 */
export function liveStatus(k: Key, now: number): EffectiveStatus {
  if (k.effective_status === "cooling" && k.cooldown_until && Date.parse(k.cooldown_until) <= now) return "active";
  return k.effective_status;
}

export function withLiveStatus(k: Key, now: number): Key {
  const s = liveStatus(k, now);
  return s === k.effective_status ? k : { ...k, effective_status: s };
}

// Original word lists for random key labels, written for Acnologia. Short,
// plain words that read well together and stay clear of anything rude.
const ADJECTIVES = [
  "amber", "brave", "bright", "calm", "clever", "cosmic", "crisp", "dusty",
  "eager", "fancy", "gentle", "golden", "happy", "humble", "jolly", "keen",
  "lucky", "lunar", "mellow", "misty", "noble", "polar", "proud", "quick",
  "quiet", "rapid", "rustic", "silent", "silver", "sunny", "swift", "witty",
];
const ANIMALS = [
  "badger", "beaver", "bison", "condor", "crane", "falcon", "ferret", "gecko",
  "heron", "ibis", "jackal", "koala", "lemur", "lynx", "marten", "moose",
  "newt", "ocelot", "orca", "otter", "panda", "puffin", "quokka", "raven",
  "robin", "salmon", "seal", "stoat", "tapir", "toucan", "walrus", "wombat",
];

/**
 * A short, readable key label such as "amber-falcon": an adjective and an
 * animal, lowercase, joined by a hyphen. Labels in `taken` are never returned
 * (compared without case or surrounding space). A number ("quiet-otter-7") is
 * added only once every plain pair is taken. `rand` returns a number in [0, 1),
 * like Math.random, so tests can pass a seeded source.
 */
export function randomLabel(taken: Iterable<string>, rand: () => number = Math.random): string {
  const used = new Set<string>();
  for (const t of taken) used.add(t.trim().toLowerCase());
  const pick = <T,>(list: readonly T[]) => list[Math.min(list.length - 1, Math.floor(rand() * list.length))];
  // A few blind draws find a free pair at once while the pool is sparse.
  for (let i = 0; i < 16; i++) {
    const label = `${pick(ADJECTIVES)}-${pick(ANIMALS)}`;
    if (!used.has(label)) return label;
  }
  // Crowded: choose among the pairs still free, so a number is never added early.
  const free: string[] = [];
  for (const a of ADJECTIVES) for (const b of ANIMALS) if (!used.has(`${a}-${b}`)) free.push(`${a}-${b}`);
  if (free.length > 0) return pick(free);
  const base = `${pick(ADJECTIVES)}-${pick(ANIMALS)}`;
  for (let n = 2; ; n++) {
    const label = `${base}-${n}`;
    if (!used.has(label)) return label;
  }
}
