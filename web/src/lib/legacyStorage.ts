/**
 * The dashboard used to store its preferences under "cogito.*" keys. Move any
 * that are left to the matching "acn.*" key once, without overwriting a value
 * already saved under the new name, then remove the old key.
 */
export function migrateLegacyStorage(storage: Storage = window.localStorage) {
  try {
    const old: string[] = [];
    for (let i = 0; i < storage.length; i++) {
      const k = storage.key(i);
      if (k?.startsWith("cogito.")) old.push(k);
    }
    for (const k of old) {
      const next = "acn." + k.slice("cogito.".length);
      const v = storage.getItem(k);
      if (v !== null && storage.getItem(next) === null) storage.setItem(next, v);
      storage.removeItem(k);
    }
  } catch {
    /* storage blocked: nothing to migrate */
  }
}
